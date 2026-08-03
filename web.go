package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"
)

const defaultWebAddr = "127.0.0.1:9944"

func serveWeb(args []string) error {
	if len(args) > 1 {
		return errors.New("usage: uppr web [path]")
	}

	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := ensureProjectFiles(absRoot); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", defaultWebAddr)
	if err != nil {
		return err
	}
	address := "http://" + listener.Addr().String()
	app := &webApp{root: absRoot, authRequired: false}
	server := &http.Server{Handler: app.routes()}

	fmt.Printf("uppr web UI: %s\n", address)
	if err := openBrowser(address); err != nil {
		fmt.Printf("open %s in your browser\n", address)
	}
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func serveServer(args []string) error {
	normalizedArgs := reorderServeArgs(args)
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "", "address to listen on")
	if err := fs.Parse(normalizedArgs); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: uppr serve [path] [--addr 0.0.0.0:9944]")
	}
	root := "."
	if fs.NArg() == 1 {
		root = fs.Arg(0)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := ensureServerFiles(absRoot); err != nil {
		return err
	}
	cfg, err := loadServerConfig(filepath.Join(absRoot, envFile))
	if err != nil {
		return err
	}
	if strings.TrimSpace(*addr) != "" {
		cfg.Addr = normalizeListenAddr(*addr)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := initAuthDB(db); err != nil {
		return err
	}
	app := &webApp{
		root:         absRoot,
		cfg:          cfg,
		db:           db,
		sessions:     newSessionStore(),
		authRequired: true,
	}
	log.Printf("uppr listening on %s", cfg.Addr)
	return http.ListenAndServe(cfg.Addr, app.routes())
}

func reorderServeArgs(args []string) []string {
	out := make([]string, 0, len(args))
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--addr" && i+1 < len(args) {
			out = append(out, arg, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(arg, "--addr=") {
			out = append(out, arg)
			continue
		}
		positional = append(positional, arg)
	}
	return append(out, positional...)
}

func ensureProjectFiles(root string) error {
	if err := requireEnvFile(root); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		return err
	}
	if err := writeFileIfMissing(filepath.Join(root, reposFile), nil); err != nil {
		return err
	}
	if err := ensureAuthDBFile(root); err != nil {
		return err
	}
	return nil
}

type webApp struct {
	root         string
	serverRoot   string
	basePath     string
	cfg          serverConfig
	db           *sql.DB
	sessions     *sessionStore
	authRequired bool
}

type workspacesPage struct {
	Root       string
	BasePath   string
	ActiveRepo string
	Workspaces []Workspace
	NewName    string
	Message    string
	Error      string
}

type indexPage struct {
	Root         string
	BasePath     string
	ActiveRepo   string
	Repos        []Repo
	NewRepo      Repo
	ProjectFiles []projectFile
	Message      string
	Error        string
}

type repoPage struct {
	Root       string
	BasePath   string
	Repo       Repo
	ActiveRepo string
	Index      int
	AppEnvPath string
	AppEnv     []envField
	ShellPath  string
	ShellError string
	Message    string
	Error      string
}

type filePage struct {
	Root       string
	BasePath   string
	ActiveRepo string
	File       projectFile
	Content    string
	Missing    bool
	Message    string
	Error      string
}

type syncPage struct {
	Root       string
	BasePath   string
	ActiveRepo string
	Repos      []Repo
	Items      []syncItem
	Message    string
	Error      string
}

type syncItem struct {
	Workspace string
	Root      string
	BasePath  string
	Index     int
	Repo      Repo
}

type launchPage struct {
	Root, BasePath, ActiveRepo, Message, Error string
	Command                                    string
	Notice                                     string
}

type terminalPage struct {
	Root, BasePath, ActiveRepo, Message, Error string
}

type credentialsPage struct {
	Root       string
	BasePath   string
	ActiveRepo string
	Username   string
	Password   string
	Message    string
	Error      string
}

type envField struct {
	Key         string
	Value       string
	Description string
	Example     string
	Required    bool
}

type projectFile struct {
	ID          string
	Name        string
	Path        string
	Description string
	Generated   bool
}

func (app *webApp) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", app.handleLogin)
	mux.HandleFunc("/logout", app.handleLogout)
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/workspaces", app.handleWorkspaces)
	mux.HandleFunc("/workspaces/", app.handleWorkspaceRoute)
	mux.HandleFunc("/files", app.handleFiles)
	mux.HandleFunc("/files/", app.handleFile)
	mux.HandleFunc("/terminal", app.handleTerminal)
	mux.HandleFunc("/terminal/shell", app.handleTerminalShell)
	mux.HandleFunc("/repos", app.handleAddRepo)
	mux.HandleFunc("/repos/", app.handleRepo)
	mux.HandleFunc("/generate", app.handleGenerate)
	mux.HandleFunc("/env/download", app.handleEnvDownload)
	mux.HandleFunc("/sync", app.handleSync)
	mux.HandleFunc("/sync/pull", app.handleSyncPull)
	mux.HandleFunc("/sync/push", app.handleSyncPush)
	mux.HandleFunc("/sync/prepare", app.handleSyncPrepare)
	mux.HandleFunc("/launch", app.handleLaunch)
	mux.HandleFunc("/launch/shell", app.handleLaunchShell)
	mux.HandleFunc("/credentials", app.handleCredentials)
	return securityHeaders(app.requireAuth(mux))
}

func (app *webApp) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if app.isServerMode() {
		app.renderWorkspaces(w, workspacesPage{Message: r.URL.Query().Get("message")})
		return
	}
	http.Redirect(w, r, app.routePath("/repos"), http.StatusSeeOther)
}

func (app *webApp) isServerMode() bool {
	return fileExists(filepath.Join(app.root, workspacesFile))
}

func (app *webApp) routePath(path string) string {
	if app.basePath == "" {
		return path
	}
	return app.basePath + path
}

func (app *webApp) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if !app.isServerMode() {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		app.renderWorkspaces(w, workspacesPage{Message: r.URL.Query().Get("message")})
	case http.MethodPost:
		name := strings.TrimSpace(r.FormValue("name"))
		workspace, err := createWorkspace(app.root, name)
		if err != nil {
			app.renderWorkspaces(w, workspacesPage{NewName: name, Error: err.Error()})
			return
		}
		http.Redirect(w, r, "/workspaces/"+url.PathEscape(workspace.Name)+"/repos?message=workspace+created", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *webApp) renderWorkspaces(w http.ResponseWriter, page workspacesPage) {
	workspaces, err := readWorkspaces(filepath.Join(app.root, workspacesFile))
	page.Root = app.root
	page.BasePath = ""
	page.Workspaces = workspaces
	if err != nil && page.Error == "" {
		page.Error = err.Error()
	}
	if err := workspacesTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (app *webApp) handleWorkspaceRoute(w http.ResponseWriter, r *http.Request) {
	if !app.isServerMode() {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/workspaces/")
	parts := strings.SplitN(strings.Trim(rest, "/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	name, err := url.PathUnescape(parts[0])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "delete" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := deleteWorkspace(app.root, name); err != nil {
			app.renderWorkspaces(w, workspacesPage{Error: err.Error()})
			return
		}
		http.Redirect(w, r, "/workspaces?message=workspace+deleted", http.StatusSeeOther)
		return
	}
	_, workspaceRoot, err := resolveWorkspace(app.root, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	childPath := "/"
	if len(parts) == 2 {
		childPath = "/" + parts[1]
	}
	workspaceApp := *app
	workspaceApp.root = workspaceRoot
	workspaceApp.serverRoot = app.root
	workspaceApp.basePath = "/workspaces/" + url.PathEscape(normalizeWorkspaceName(name))
	childReq := r.Clone(r.Context())
	childURL := *r.URL
	childURL.Path = childPath
	childURL.RawPath = ""
	childReq.URL = &childURL
	workspaceApp.workspaceRoutes().ServeHTTP(w, childReq)
}

func (app *webApp) workspaceRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleWorkspaceIndex)
	mux.HandleFunc("/files", app.handleFiles)
	mux.HandleFunc("/files/", app.handleFile)
	mux.HandleFunc("/terminal", app.handleTerminal)
	mux.HandleFunc("/terminal/shell", app.handleTerminalShell)
	mux.HandleFunc("/repos", app.handleAddRepo)
	mux.HandleFunc("/repos/", app.handleRepo)
	mux.HandleFunc("/generate", app.handleGenerate)
	mux.HandleFunc("/env/download", app.handleEnvDownload)
	mux.HandleFunc("/sync", app.handleSync)
	mux.HandleFunc("/sync/pull", app.handleSyncPull)
	mux.HandleFunc("/sync/push", app.handleSyncPush)
	mux.HandleFunc("/sync/prepare", app.handleSyncPrepare)
	mux.HandleFunc("/launch", app.handleLaunch)
	mux.HandleFunc("/launch/shell", app.handleLaunchShell)
	mux.HandleFunc("/credentials", app.handleCredentials)
	return mux
}

func (app *webApp) handleWorkspaceIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, app.routePath("/repos"), http.StatusSeeOther)
}

func (app *webApp) renderRepos(w http.ResponseWriter, page indexPage) {
	repos, err := readRepos(filepath.Join(app.root, reposFile))
	page.Root = app.root
	page.BasePath = app.basePath
	page.Repos = repos
	if err != nil && page.Error == "" {
		page.Error = err.Error()
	}
	if err := indexTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (app *webApp) handleAddRepo(w http.ResponseWriter, r *http.Request) {
	if app.isServerMode() && app.basePath == "" {
		http.Redirect(w, r, "/workspaces?message=create+or+open+a+workspace+to+manage+repositories", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		app.renderRepos(w, indexPage{Message: r.URL.Query().Get("message")})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repos, err := readRepos(filepath.Join(app.root, reposFile))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	repo, err := repoFromForm(r)
	if err != nil {
		app.renderRepos(w, indexPage{NewRepo: repo, Error: err.Error()})
		return
	}
	if err := validateNewRepo(repo, repos); err != nil {
		app.renderRepos(w, indexPage{NewRepo: repo, Error: err.Error()})
		return
	}
	repos = append(repos, repo)
	if err := writeRepos(filepath.Join(app.root, reposFile), repos); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, app.routePath("/repos?message=repo+added"), http.StatusSeeOther)
}

func (app *webApp) handleRepo(w http.ResponseWriter, r *http.Request) {
	index, action, ok := parseRepoRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		app.handleEditRepo(w, r, index)
	case action == "save" && r.Method == http.MethodPost:
		app.handleSaveRepo(w, r, index)
	case action == "delete" && r.Method == http.MethodPost:
		app.handleDeleteRepo(w, r, index)
	case action == "pull" && r.Method == http.MethodPost:
		app.handlePullRepo(w, r, index)
	case action == "push" && r.Method == http.MethodPost:
		app.handlePushRepo(w, r, index)
	case action == "shell" && r.Method == http.MethodPost:
		app.handleRepoShellCommand(w, r, index)
	case action == "shell" && r.Method == http.MethodGet:
		app.handleRepoShell(w, r, index)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func parseRepoRoute(path string) (int, string, bool) {
	rest := strings.TrimPrefix(path, "/repos/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		return 0, "", false
	}
	index, err := strconv.Atoi(parts[0])
	if err != nil || index < 0 {
		return 0, "", false
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	return index, action, true
}

func (app *webApp) handleEditRepo(w http.ResponseWriter, r *http.Request, index int) {
	repos, err := readRepos(filepath.Join(app.root, reposFile))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if index >= len(repos) {
		http.NotFound(w, r)
		return
	}
	app.renderRepo(w, repoPage{Repo: repos[index], Index: index, Message: r.URL.Query().Get("message")})
}

func (app *webApp) renderRepo(w http.ResponseWriter, page repoPage) {
	page.Root = app.root
	page.BasePath = app.basePath
	if page.ActiveRepo == "" {
		page.ActiveRepo = repoLabel(page.Repo)
	}
	path, err := repoShellPath(app.root, page.Index)
	page.ShellPath = path
	if err != nil {
		page.ShellError = err.Error()
	}
	if len(page.AppEnv) == 0 {
		appEnv, appEnvPath, err := repoAppEnvFields(app.root, page.Repo)
		page.AppEnv = appEnv
		page.AppEnvPath = appEnvPath
		if err != nil && page.Error == "" {
			page.Error = err.Error()
		}
	}
	if err := repoTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (app *webApp) handleSaveRepo(w http.ResponseWriter, r *http.Request, index int) {
	repos, err := readRepos(filepath.Join(app.root, reposFile))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if index >= len(repos) {
		http.NotFound(w, r)
		return
	}
	repo, err := repoFromForm(r)
	if err != nil {
		app.renderRepo(w, repoPage{Repo: repo, Index: index, Error: err.Error()})
		return
	}
	candidates := append([]Repo(nil), repos[:index]...)
	candidates = append(candidates, repos[index+1:]...)
	if err := validateNewRepo(repo, candidates); err != nil {
		app.renderRepo(w, repoPage{Repo: repo, Index: index, Error: err.Error()})
		return
	}
	repos[index] = repo
	if err := writeRepos(filepath.Join(app.root, reposFile), repos); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := saveRepoAppEnvFromForm(app.root, repo, r); err != nil {
		app.renderRepo(w, repoPage{Repo: repo, Index: index, Error: err.Error()})
		return
	}
	http.Redirect(w, r, app.routePath(fmt.Sprintf("/repos/%d?message=config+saved", index)), http.StatusSeeOther)
}

func (app *webApp) handleDeleteRepo(w http.ResponseWriter, r *http.Request, index int) {
	repos, err := readRepos(filepath.Join(app.root, reposFile))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if index >= len(repos) {
		http.NotFound(w, r)
		return
	}
	repos = append(repos[:index], repos[index+1:]...)
	if err := writeRepos(filepath.Join(app.root, reposFile), repos); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, app.routePath("/repos?message=repo+deleted"), http.StatusSeeOther)
}

func (app *webApp) handlePullRepo(w http.ResponseWriter, r *http.Request, index int) {
	repos, err := readRepos(filepath.Join(app.root, reposFile))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if index >= len(repos) {
		http.NotFound(w, r)
		return
	}
	if err := pullRepoAt(app.root, index); err != nil {
		app.renderRepo(w, repoPage{Repo: repos[index], Index: index, Error: err.Error()})
		return
	}
	http.Redirect(w, r, app.routePath(fmt.Sprintf("/repos/%d?message=repo+pulled", index)), http.StatusSeeOther)
}

func (app *webApp) handlePushRepo(w http.ResponseWriter, r *http.Request, index int) {
	repos, err := readRepos(filepath.Join(app.root, reposFile))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if index >= len(repos) {
		http.NotFound(w, r)
		return
	}
	message := strings.TrimSpace(r.FormValue("message"))
	if message == "" {
		app.renderRepo(w, repoPage{Repo: repos[index], Index: index, Error: "commit message cannot be empty"})
		return
	}
	if err := pushRepoAt(app.root, index, message); err != nil {
		app.renderRepo(w, repoPage{Repo: repos[index], Index: index, Error: err.Error()})
		return
	}
	http.Redirect(w, r, app.routePath(fmt.Sprintf("/repos/%d?message=repo+pushed", index)), http.StatusSeeOther)
}

func (app *webApp) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if app.isServerMode() && app.basePath == "" {
			http.Redirect(w, r, "/workspaces", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, app.routePath("/files"), http.StatusSeeOther)
		return
	}
	if app.isServerMode() && app.basePath == "" {
		if err := generateServerFilesAt(app.root); err != nil {
			app.renderWorkspaces(w, workspacesPage{Error: err.Error()})
			return
		}
		http.Redirect(w, r, "/workspaces?message=files+generated", http.StatusSeeOther)
		return
	}
	if app.serverRoot != "" {
		if err := generateServerFilesAt(app.serverRoot); err != nil {
			app.renderFiles(w, fileListPage{Error: err.Error()})
			return
		}
		http.Redirect(w, r, app.routePath("/files?message=files+generated"), http.StatusSeeOther)
		return
	}
	if err := generateProjectFilesAt(app.root); err != nil {
		app.renderFiles(w, fileListPage{Error: err.Error()})
		return
	}
	http.Redirect(w, r, app.routePath("/files?message=files+generated"), http.StatusSeeOther)
}

func (app *webApp) handleEnvDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	contents, err := exportWorkspaceEnv(app.root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="uppr-env-export.env"`)
	_, _ = w.Write([]byte(contents))
}

type fileListPage struct {
	Root         string
	BasePath     string
	ActiveRepo   string
	ProjectFiles []projectFile
	Message      string
	Error        string
}

func (app *webApp) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/files" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	app.renderFiles(w, fileListPage{Message: r.URL.Query().Get("message")})
}

func (app *webApp) renderFiles(w http.ResponseWriter, page fileListPage) {
	page.Root = app.root
	page.BasePath = app.basePath
	page.ProjectFiles = app.projectFiles()
	if err := filesTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (app *webApp) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/terminal" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page := terminalPage{Root: app.root, BasePath: app.basePath, Message: r.URL.Query().Get("message")}
	if err := terminalTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (app *webApp) handleTerminalShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	app.handleShell(w, r, app.root)
}

func (app *webApp) handleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	file, ok := projectFileByID(app.projectFiles(), strings.Trim(strings.TrimPrefix(r.URL.Path, "/files/"), "/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	page := filePage{Root: app.root, File: file}
	contents, err := os.ReadFile(filepath.Join(app.root, file.Path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && file.Generated {
			page.Missing = true
		} else if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		} else {
			page.Error = err.Error()
		}
	} else {
		page.Content = string(contents)
	}
	app.renderFile(w, page)
}

func (app *webApp) renderFile(w http.ResponseWriter, page filePage) {
	page.Root = app.root
	page.BasePath = app.basePath
	if err := fileTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (app *webApp) handleSync(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/sync" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	app.renderSync(w, syncPage{Message: r.URL.Query().Get("message")})
}

func (app *webApp) renderSync(w http.ResponseWriter, page syncPage) {
	var repos []Repo
	var err error
	if !(app.isServerMode() && app.basePath == "") {
		repos, err = readRepos(filepath.Join(app.root, reposFile))
	}
	page.Root = app.root
	page.BasePath = app.basePath
	page.Repos = repos
	for index, repo := range repos {
		page.Items = append(page.Items, syncItem{Root: app.root, BasePath: app.basePath, Index: index, Repo: repo})
	}
	if app.isServerMode() && app.basePath == "" {
		page.Repos = nil
		page.Items = nil
		workspaces, workspaceErr := readWorkspaces(filepath.Join(app.root, workspacesFile))
		if workspaceErr != nil && page.Error == "" { page.Error = workspaceErr.Error() }
		for _, workspace := range workspaces {
			_, root, resolveErr := resolveWorkspace(app.root, workspace.Name)
			if resolveErr != nil { if page.Error == "" { page.Error = resolveErr.Error() }; continue }
			workspaceRepos, readErr := readRepos(filepath.Join(root, reposFile))
			if readErr != nil { if page.Error == "" { page.Error = readErr.Error() }; continue }
			for index, repo := range workspaceRepos {
				page.Items = append(page.Items, syncItem{Workspace: workspace.Name, Root: root, BasePath: "/workspaces/" + url.PathEscape(workspace.Name), Index: index, Repo: repo})
			}
		}
	}
	if err != nil && page.Error == "" {
		page.Error = err.Error()
	}
	if err := syncTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (app *webApp) handleSyncPrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, app.routePath("/sync"), http.StatusSeeOther)
		return
	}
	var roots []string
	if app.isServerMode() && app.basePath == "" {
		workspaces, err := readWorkspaces(filepath.Join(app.root, workspacesFile))
		if err != nil {
			app.renderSync(w, syncPage{Error: err.Error()})
			return
		}
		for _, workspace := range workspaces {
			_, root, err := resolveWorkspace(app.root, workspace.Name)
			if err != nil {
				app.renderSync(w, syncPage{Error: err.Error()})
				return
			}
			roots = append(roots, root)
		}
	} else {
		roots = []string{app.root}
	}
	for _, root := range roots {
		repos, err := readRepos(filepath.Join(root, reposFile))
		if err != nil {
			app.renderSync(w, syncPage{Error: err.Error()})
			return
		}
		resolveRepoPaths(root, repos)
		for _, repo := range repos {
			if err := prepareRepoEnv(repo); err != nil {
				app.renderSync(w, syncPage{Error: err.Error()})
				return
			}
		}
	}
	http.Redirect(w, r, app.routePath("/sync?message=application+files+prepared"), http.StatusSeeOther)
}

func (app *webApp) handleLaunch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	root := app.root
	command := "docker compose down --remove-orphans && docker compose up --build"
	notice := "Launch clears the existing Compose stack and orphaned containers before rebuilding it. Named volumes are preserved. Use Ctrl+C in the terminal to stop it."
	if app.serverRoot != "" {
		// A remotely managed workspace is part of the server's master Compose
		// project. Reconcile that project in place so the Uppr container does not
		// stop itself before it can start the newly generated services.
		root = app.serverRoot
		command = "services=$(docker compose config --services | grep -v '^uppr$') && docker compose up --build -d --remove-orphans $services"
		notice = "Launch rebuilds and reconciles the remotely managed services while keeping Uppr available. Removed services are cleaned up and named volumes are preserved."
	}
	page := launchPage{Root: root, BasePath: app.basePath, Message: r.URL.Query().Get("message"), Command: command, Notice: notice}
	if err := launchTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (app *webApp) handleLaunchShell(w http.ResponseWriter, r *http.Request) {
	root := app.root
	if app.serverRoot != "" {
		root = app.serverRoot
	}
	app.handleShell(w, r, root)
}

func (app *webApp) handleSyncPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, app.routePath("/sync"), http.StatusSeeOther)
		return
	}
	if app.isServerMode() && app.basePath == "" {
		workspaces, err := readWorkspaces(filepath.Join(app.root, workspacesFile))
		if err != nil {
			app.renderSync(w, syncPage{Error: err.Error()})
			return
		}
		for _, workspace := range workspaces {
			_, root, err := resolveWorkspace(app.root, workspace.Name)
			if err == nil {
				err = pullReposAt(root)
			}
			if err != nil {
				app.renderSync(w, syncPage{Error: workspace.Name + ": " + err.Error()})
				return
			}
		}
	} else if err := pullReposAt(app.root); err != nil {
		app.renderSync(w, syncPage{Error: err.Error()})
		return
	}
	http.Redirect(w, r, app.routePath("/sync?message=all+repos+pulled"), http.StatusSeeOther)
}

func (app *webApp) handleSyncPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, app.routePath("/sync"), http.StatusSeeOther)
		return
	}
	message := strings.TrimSpace(r.FormValue("message"))
	if message == "" {
		app.renderSync(w, syncPage{Error: "commit message cannot be empty"})
		return
	}
	if app.isServerMode() && app.basePath == "" {
		workspaces, err := readWorkspaces(filepath.Join(app.root, workspacesFile))
		if err != nil {
			app.renderSync(w, syncPage{Error: err.Error()})
			return
		}
		for _, workspace := range workspaces {
			_, root, err := resolveWorkspace(app.root, workspace.Name)
			if err == nil {
				err = pushReposAt(root, message)
			}
			if err != nil {
				app.renderSync(w, syncPage{Error: workspace.Name + ": " + err.Error()})
				return
			}
		}
	} else if err := pushReposAt(app.root, message); err != nil {
		app.renderSync(w, syncPage{Error: err.Error()})
		return
	}
	http.Redirect(w, r, app.routePath("/sync?message=all+repos+pushed"), http.StatusSeeOther)
}

func (app *webApp) handleCredentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		app.renderCredentials(w, credentialsPage{Message: r.URL.Query().Get("message")})
	case http.MethodPost:
		username := strings.TrimSpace(r.FormValue("username"))
		password := strings.TrimSpace(r.FormValue("password"))
		if err := writeGitHubCredentials(filepath.Join(app.root, envFile), username, password); err != nil {
			app.renderCredentials(w, credentialsPage{Username: username, Password: password, Error: err.Error()})
			return
		}
		http.Redirect(w, r, app.routePath("/credentials?message=credentials+saved"), http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *webApp) renderCredentials(w http.ResponseWriter, page credentialsPage) {
	page.Root = app.root
	page.BasePath = app.basePath
	if page.Username == "" && page.Password == "" {
		env, err := readDotEnv(filepath.Join(app.root, envFile))
		if err != nil {
			if page.Error == "" {
				page.Error = err.Error()
			}
		} else {
			page.Username = env["GITHUB_USERNAME"]
			page.Password = env["GITHUB_PASSWORD"]
		}
	}
	if err := credentialsTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func projectFiles() []projectFile {
	return []projectFile{
		{ID: "repos", Name: reposFile, Path: reposFile, Description: "Repository definitions and runtime settings."},
		{ID: "env", Name: envFile, Path: envFile, Description: "Git credentials used by pull and push."},
		{ID: "makefile", Name: makeFile, Path: makeFile, Description: "Local deployment commands generated by uppr.", Generated: true},
		{ID: "docker-compose", Name: dockerComposeFile, Path: dockerComposeFile, Description: "Generated Docker services and volumes.", Generated: true},
		{ID: "caddy-dockerfile", Name: caddyDockerFile, Path: caddyDockerFile, Description: "Generated caddyx image build with rate-limit support.", Generated: true},
		{ID: "caddyfile", Name: caddyFile, Path: caddyFile, Description: "Generated Caddy reverse proxy config.", Generated: true},
	}
}

func (app *webApp) projectFiles() []projectFile {
	files := projectFiles()
	if app.serverRoot == "" {
		return files
	}
	filtered := files[:0]
	for _, file := range files {
		if !file.Generated {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

func projectFileByID(files []projectFile, id string) (projectFile, bool) {
	for _, file := range files {
		if file.ID == id {
			return file, true
		}
	}
	return projectFile{}, false
}

func repoFromForm(r *http.Request) (Repo, error) {
	if err := r.ParseForm(); err != nil {
		return Repo{}, err
	}
	urlValue := strings.TrimSpace(r.FormValue("url"))
	githubOwner := strings.Trim(strings.TrimSpace(r.FormValue("github_owner")), "/")
	githubRepo := strings.Trim(strings.TrimSpace(r.FormValue("github_repo")), "/")
	githubRepo = strings.TrimSuffix(githubRepo, ".git")
	if urlValue == "" && (githubOwner != "" || githubRepo != "") {
		if githubOwner == "" {
			return Repo{}, errors.New("GitHub user is required")
		}
		if githubRepo == "" {
			return Repo{}, errors.New("GitHub repo is required")
		}
		urlValue = githubRepoURL(githubOwner, githubRepo)
	}
	domains := splitLines(r.FormValue("domains"))
	if legacyDomain := strings.TrimSpace(r.FormValue("domain")); legacyDomain != "" {
		domains = append([]string{legacyDomain}, domains...)
	}
	repo := Repo{
		Name:    strings.TrimSpace(r.FormValue("name")),
		URL:     urlValue,
		Path:    strings.TrimSpace(r.FormValue("path")),
		Branch:  strings.TrimSpace(r.FormValue("branch")),
		Domains: uniqueStrings(domains),
		Env:     splitLines(r.FormValue("env")),
		Volumes: splitLines(r.FormValue("volumes")),
	}
	if len(repo.Domains) > 0 {
		repo.Domain = repo.Domains[0]
	}
	port, err := parseOptionalPort("port", r.FormValue("port"))
	if err != nil {
		return repo, err
	}
	containerPort, err := parseOptionalPort("container_port", r.FormValue("container_port"))
	if err != nil {
		return repo, err
	}
	rateLimitEvents, err := parseOptionalPositiveInt("rate_limit_events", r.FormValue("rate_limit_events"))
	if err != nil {
		return repo, err
	}
	repo.Port = port
	repo.ContainerPort = containerPort
	repo.RateLimit = normalizeRateLimit(RateLimit{
		Enabled: r.FormValue("rate_limit_enabled") != "",
		Zone:    strings.TrimSpace(r.FormValue("rate_limit_zone")),
		Events:  rateLimitEvents,
		Window:  strings.TrimSpace(r.FormValue("rate_limit_window")),
	})
	if repo.URL == "" {
		return repo, errors.New("repository url is required")
	}
	if repo.Name == "" {
		repo.Name = repoNameFromURL(repo.URL)
	}
	if repo.Path == "" {
		repo.Path = defaultRepoPath(repo)
	}
	return repo, nil
}

func repoAppEnvFields(root string, repo Repo) ([]envField, string, error) {
	repos := []Repo{repo}
	resolveRepoPaths(root, repos)
	repo = repos[0]
	variables, err := readRepoEnvSchema(repo.Path)
	if err != nil {
		return nil, filepath.Join(repo.Path, envFile), err
	}
	envPath := filepath.Join(repo.Path, envFile)
	if len(variables) == 0 {
		return nil, envPath, nil
	}
	values, err := readDotEnvIfExists(envPath)
	if err != nil {
		return nil, envPath, err
	}
	fields := make([]envField, 0, len(variables))
	for _, variable := range variables {
		fields = append(fields, envField{
			Key:         variable.Key,
			Value:       values[variable.Key],
			Description: variable.Description,
			Example:     variable.Example,
			Required:    variable.Required,
		})
	}
	return fields, envPath, nil
}

func exportWorkspaceEnv(root string) (string, error) {
	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		return "", err
	}
	resolveRepoPaths(root, repos)
	var builder strings.Builder
	builder.WriteString("# Exported by uppr. Keep this file private.\n")
	for _, repo := range repos {
		builder.WriteString("\n")
		fmt.Fprintf(&builder, "# [%s]\n", repoLabel(repo))
		fmt.Fprintf(&builder, "# path: %s\n", repo.Path)
		values, err := readDotEnvIfExists(filepath.Join(repo.Path, envFile))
		if err != nil {
			return "", err
		}
		variables, err := readRepoEnvSchema(repo.Path)
		if err != nil {
			return "", err
		}
		written := map[string]bool{}
		for _, variable := range variables {
			fmt.Fprintf(&builder, "%s=%s\n", variable.Key, quoteDotEnvValue(values[variable.Key]))
			written[variable.Key] = true
		}
		for _, entry := range repo.Env {
			key, value, ok := strings.Cut(entry, "=")
			key = strings.TrimSpace(key)
			if !ok || !isEnvKey(key) || written[key] {
				continue
			}
			fmt.Fprintf(&builder, "%s=%s\n", key, quoteDotEnvValue(value))
			written[key] = true
		}
		for key, value := range values {
			if written[key] {
				continue
			}
			fmt.Fprintf(&builder, "%s=%s\n", key, quoteDotEnvValue(value))
		}
	}
	return builder.String(), nil
}

func saveRepoAppEnvFromForm(root string, repo Repo, r *http.Request) error {
	keys := r.Form["app_env_key"]
	values := r.Form["app_env_value"]
	if len(keys) == 0 {
		return nil
	}
	if len(keys) != len(values) {
		return errors.New("environment form is incomplete")
	}
	repos := []Repo{repo}
	resolveRepoPaths(root, repos)
	repo = repos[0]
	envPath := filepath.Join(repo.Path, envFile)
	envValues := make(map[string]string, len(keys))
	orderedKeys := make([]string, 0, len(keys))
	for i, key := range keys {
		key = strings.TrimSpace(key)
		if !isEnvKey(key) {
			return fmt.Errorf("invalid environment variable name %q", key)
		}
		if _, exists := envValues[key]; !exists {
			orderedKeys = append(orderedKeys, key)
		}
		envValues[key] = values[i]
	}
	return writeDotEnvValues(envPath, orderedKeys, envValues)
}

func readDotEnvIfExists(path string) (map[string]string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	values, err := readDotEnv(path)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func writeDotEnvValues(path string, orderedKeys []string, values map[string]string) error {
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	lines := []string{}
	if len(contents) > 0 {
		lines = strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	seen := map[string]bool{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value, exists := values[key]
		if !exists {
			continue
		}
		lines[i] = key + "=" + quoteDotEnvValue(value)
		seen[key] = true
	}
	for _, key := range orderedKeys {
		if !seen[key] {
			lines = append(lines, key+"="+quoteDotEnvValue(values[key]))
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func githubRepoURL(owner, repo string) string {
	return "https://github.com/" + owner + "/" + repo
}

func parseOptionalPort(key, value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s must be a port between 1 and 65535", key)
	}
	return port, nil
}

func parseOptionalPositiveInt(key, value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return number, nil
}

func splitLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func writeGitHubCredentials(path, username, password string) error {
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	lines := []string{}
	if len(contents) > 0 {
		lines = strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	values := map[string]string{
		"GITHUB_USERNAME": username,
		"GITHUB_PASSWORD": password,
	}
	seen := map[string]bool{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value, exists := values[key]
		if !exists {
			continue
		}
		lines[i] = key + "=" + quoteDotEnvValue(value)
		seen[key] = true
	}
	for _, key := range []string{"GITHUB_USERNAME", "GITHUB_PASSWORD"} {
		if !seen[key] {
			lines = append(lines, key+"="+quoteDotEnvValue(values[key]))
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func quoteDotEnvValue(value string) string {
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, " \t\n\r\"'\\#") {
		return strconv.Quote(value)
	}
	return value
}

type shellCommandResponse struct {
	Command string `json:"command"`
	Output  string `json:"output"`
	CWD     string `json:"cwd"`
	Exit    int    `json:"exit"`
	Error   string `json:"error,omitempty"`
}

type terminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

var terminalUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		return err == nil && u.Host == r.Host
	},
}

func (app *webApp) handleRepoShell(w http.ResponseWriter, r *http.Request, index int) {
	path, err := repoShellPath(app.root, index)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	app.handleShell(w, r, path)
}

func (app *webApp) handleShell(w http.ResponseWriter, r *http.Request, path string) {
	conn, err := terminalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	cmd := interactiveShellCommand(r.Context(), path)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = conn.WriteJSON(terminalMessage{Type: "error", Data: err.Error()})
		return
	}
	defer closeTerminal(ptmx, cmd)

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if writeErr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				_ = conn.WriteJSON(terminalMessage{Type: "ended"})
				return
			}
		}
	}()

	for {
		typ, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch typ {
		case websocket.BinaryMessage:
			if _, err := ptmx.Write(msg); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				return
			}
		case websocket.TextMessage:
			var message terminalMessage
			if err := json.Unmarshal(msg, &message); err == nil && message.Type != "" {
				switch message.Type {
				case "input":
					if _, err := ptmx.Write([]byte(message.Data)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
						return
					}
				case "resize":
					if message.Cols > 0 && message.Rows > 0 {
						_ = pty.Setsize(ptmx, &pty.Winsize{Cols: message.Cols, Rows: message.Rows})
					}
				case "close":
					return
				}
			} else if _, err := ptmx.Write(msg); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				return
			}
		}
		select {
		case <-done:
			return
		default:
		}
	}
}

func (app *webApp) handleRepoShellCommand(w http.ResponseWriter, r *http.Request, index int) {
	path, err := repoShellPath(app.root, index)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	response, err := runShellCommand(path, r.FormValue("cwd"), strings.TrimSpace(r.FormValue("command")))
	if err != nil {
		response.Error = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func interactiveShellCommand(ctx context.Context, dir string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		cmd := exec.CommandContext(ctx, "cmd.exe")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "UPPR_WEB_SHELL=1")
		return cmd
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-l")
	cmd.Dir = dir
	cmd.Env = terminalEnv(os.Environ())
	return cmd
}

func terminalEnv(env []string) []string {
	out := make([]string, 0, len(env)+3)
	for _, entry := range env {
		if strings.HasPrefix(entry, "TERM=") || strings.HasPrefix(entry, "COLORTERM=") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor", "UPPR_WEB_SHELL=1")
}

func closeTerminal(ptmx io.Closer, cmd *exec.Cmd) {
	_ = ptmx.Close()
	if process := cmd.Process; process != nil {
		_ = process.Signal(os.Interrupt)
	}

	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		if process := cmd.Process; process != nil {
			_ = process.Kill()
		}
		<-waitCh
	}
}

func repoShellPath(root string, index int) (string, error) {
	repos, err := readRepos(filepath.Join(root, reposFile))
	if err != nil {
		return "", err
	}
	if index < 0 || index >= len(repos) {
		return "", fmt.Errorf("repo %d not found", index)
	}
	resolveRepoPaths(root, repos)
	repoPath := repos[index].Path
	info, err := os.Stat(repoPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return repoPath, fmt.Errorf("%s does not exist; pull the repository first", repoPath)
		}
		return repoPath, err
	}
	if !info.IsDir() {
		return repoPath, fmt.Errorf("%s is not a directory", repoPath)
	}
	return repoPath, nil
}

func runShellCommand(repoPath, cwd, command string) (shellCommandResponse, error) {
	response := shellCommandResponse{Command: command, CWD: repoPath}
	if command == "" {
		return response, errors.New("command cannot be empty")
	}
	startPath, err := cleanShellCWD(repoPath, cwd)
	if err != nil {
		return response, err
	}
	execCommand := command
	if runtime.GOOS != "windows" {
		execCommand = wrapShellCommand(command)
	}
	name, args := shellCommandExec(execCommand)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = startPath
	cmd.Env = append(os.Environ(), "TERM=dumb", "UPPR_WEB_SHELL=1")
	output, err := cmd.CombinedOutput()
	response.CWD = startPath
	if ctx.Err() == context.DeadlineExceeded {
		response.Output = string(bytes.ReplaceAll(output, []byte("\r\n"), []byte("\n")))
		response.Exit = 124
		return response, errors.New("command timed out after 2 minutes")
	}
	if runtime.GOOS != "windows" {
		response.Output, response.CWD, response.Exit = parseShellCommandOutput(output, startPath)
		if _, err := cleanShellCWD(repoPath, response.CWD); err != nil {
			response.CWD = startPath
			return response, errors.New("working directory must stay inside the repository")
		}
		return response, nil
	}
	response.Output = string(bytes.ReplaceAll(output, []byte("\r\n"), []byte("\n")))
	if exitErr, ok := err.(*exec.ExitError); ok {
		response.Exit = exitErr.ExitCode()
		return response, nil
	}
	return response, err
}

func cleanShellCWD(repoPath, cwd string) (string, error) {
	if cwd == "" {
		return repoPath, nil
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(repoPath, cwd)
	}
	cwd = filepath.Clean(cwd)
	repoPath = filepath.Clean(repoPath)
	rel, err := filepath.Rel(repoPath, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("working directory must stay inside the repository")
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", cwd)
	}
	return cwd, nil
}

func shellCommandExec(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/C", command}
	}
	return "/bin/sh", []string{"-lc", command}
}

const shellStateMarker = "__UPPR_SHELL_STATE__"

func wrapShellCommand(command string) string {
	return command + "\n" +
		"status=$?\n" +
		"printf '\\n" + shellStateMarker + "EXIT:%s\\n" + shellStateMarker + "PWD:%s\\n' \"$status\" \"$PWD\"\n" +
		"exit 0"
}

func parseShellCommandOutput(output []byte, fallbackCWD string) (string, string, int) {
	text := string(bytes.ReplaceAll(output, []byte("\r\n"), []byte("\n")))
	exitIndex := strings.LastIndex(text, "\n"+shellStateMarker+"EXIT:")
	if exitIndex == -1 {
		return text, fallbackCWD, 0
	}
	userOutput := text[:exitIndex]
	state := text[exitIndex+1:]
	lines := strings.Split(strings.TrimSpace(state), "\n")
	exitCode := 0
	cwd := fallbackCWD
	for _, line := range lines {
		if value, ok := strings.CutPrefix(line, shellStateMarker+"EXIT:"); ok {
			if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				exitCode = parsed
			}
		}
		if value, ok := strings.CutPrefix(line, shellStateMarker+"PWD:"); ok {
			cwd = strings.TrimSpace(value)
		}
	}
	return userOutput, cwd, exitCode
}

func openBrowser(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	}
	return cmd.Run()
}

var webFuncs = template.FuncMap{
	"joinLines": func(values []string) string {
		return strings.Join(values, "\n")
	},
	"domainsText": func(repo Repo) string {
		return strings.Join(repoDomains(repo), "\n")
	},
	"rateLimit":   normalizeRateLimit,
	"githubOwner": githubOwnerFromURL,
	"githubRepo":  githubRepoFromURL,
	"absPath": func(root, path string) string {
		if path == "" {
			return ""
		}
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(root, path)
	},
	"branchLabel": func(branch string) string {
		if strings.TrimSpace(branch) == "" {
			return "default"
		}
		return branch
	},
	"repoStatus": repoStatus,
	"repoLastSync": func(root string, repo Repo) string {
		repoPath := repo.Path
		if repoPath == "" {
			repoPath = defaultRepoPath(repo)
		}
		if !filepath.IsAbs(repoPath) {
			repoPath = filepath.Join(root, repoPath)
		}
		info, err := os.Stat(filepath.Join(repoPath, ".git", "FETCH_HEAD"))
		if err != nil {
			info, err = os.Stat(repoPath)
		}
		if err != nil {
			return "Never"
		}
		return relativeTime(info.ModTime())
	},
	"fileModified": func(root string, file projectFile) string {
		info, err := os.Stat(filepath.Join(root, file.Path))
		if err != nil {
			return "Missing"
		}
		return relativeTime(info.ModTime())
	},
	"maskSecret": func(value string) string {
		if strings.TrimSpace(value) == "" {
			return ""
		}
		return strings.Repeat("•", 28)
	},
	"messageText": func(message string) string {
		switch message {
		case "config saved":
			return "Configuration saved."
		case "credentials saved":
			return "GitHub credentials saved."
		case "files generated":
			return "Runtime files generated."
		case "repo added":
			return "Repository added."
		case "repo deleted":
			return "Repository deleted."
		case "repo pulled":
			return "Repository pulled."
		case "repo pushed":
			return "Repository pushed."
		case "all repos pulled":
			return "All repositories pulled."
		case "all repos pushed":
			return "All repositories pushed."
		case "workspace created":
			return "Workspace created."
		case "workspace deleted":
			return "Workspace deleted."
		default:
			return message
		}
	},
}

type repoStatusInfo struct {
	Key   string
	Label string
}

func repoStatus(root string, repo Repo) repoStatusInfo {
	repoPath := repo.Path
	if repoPath == "" {
		repoPath = defaultRepoPath(repo)
	}
	if !filepath.IsAbs(repoPath) {
		repoPath = filepath.Join(root, repoPath)
	}
	if _, err := os.Stat(repoPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return repoStatusInfo{Key: "unknown", Label: "Not cloned"}
		}
		return repoStatusInfo{Key: "error", Label: "Error"}
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return repoStatusInfo{Key: "unknown", Label: "Unknown"}
	}
	cmd := exec.Command("git", gitArgsWithSafeDirectory("-C", repoPath, "status", "--porcelain")...)
	output, err := cmd.Output()
	if err != nil {
		return repoStatusInfo{Key: "error", Label: "Error"}
	}
	if strings.TrimSpace(string(output)) != "" {
		return repoStatusInfo{Key: "changes", Label: "Changes"}
	}
	return repoStatusInfo{Key: "synced", Label: "Synced"}
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "Just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	}
	if d < 14*24*time.Hour {
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
	return t.Format("Jan 2, 2006")
}

func githubOwnerFromURL(raw string) string {
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.TrimSuffix(raw, ".git")
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

func githubRepoFromURL(raw string) string {
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.TrimSuffix(raw, ".git")
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

var workspacesTemplate = template.Must(template.New("workspaces").Funcs(webFuncs).Parse(pageChrome(workspacesBody)))
var indexTemplate = template.Must(template.New("index").Funcs(webFuncs).Parse(pageChrome(indexBody)))
var filesTemplate = template.Must(template.New("files").Funcs(webFuncs).Parse(pageChrome(filesBody)))
var repoTemplate = template.Must(template.New("repo").Funcs(webFuncs).Parse(pageChrome(repoBody + terminalAssets)))
var fileTemplate = template.Must(template.New("file").Funcs(webFuncs).Parse(pageChrome(fileBody)))
var syncTemplate = template.Must(template.New("sync").Funcs(webFuncs).Parse(pageChrome(syncBody)))
var terminalTemplate = template.Must(template.New("terminal").Funcs(webFuncs).Parse(pageChrome(terminalBody + terminalAssets)))
var launchTemplate = template.Must(template.New("launch").Funcs(webFuncs).Parse(pageChrome(launchBody + terminalAssets)))
var credentialsTemplate = template.Must(template.New("credentials").Funcs(webFuncs).Parse(pageChrome(credentialsBody)))

func pageChrome(body string) string {
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>uppr</title>
<script>
(function () {
  try {
    var theme = window.localStorage.getItem('uppr-theme');
    if (theme === 'dark' || theme === 'light') {
      document.documentElement.setAttribute('data-theme', theme);
    }
  } catch (err) {}
})();
</script>
<style>
:root {
  color-scheme: light dark;
  --page-bg:#f4f1ea;
  --sidebar-bg:#ebe7dd;
  --surface:#fbfaf7;
  --surface-muted:#f0ede6;
  --surface-hover:#e6e1d7;
  --text:#1f252d;
  --text-muted:#657080;
  --text-soft:#8a94a3;
  --border:#d6d0c5;
  --border-strong:#bdb5a8;
  --accent:#2563eb;
  --accent-hover:#1d4ed8;
  --accent-soft:rgba(37,99,235,.11);
  --success:#11845b;
  --warning:#b7791f;
  --danger:#c94a55;
  --danger-soft:#fbeaec;
  --success-soft:#e7f6ef;
  --warning-soft:#fff5db;
  --placeholder:#9299a5;
  --focus-ring:rgba(37,99,235,.18);
  --shadow:0 1px 2px rgba(24,29,37,.06), 0 16px 40px rgba(24,29,37,.08);
  --radius-sm:8px;
  --radius-md:10px;
  --content-width:1320px;
  --sidebar-width:260px;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --page-bg:#0b0d10;
    --sidebar-bg:#101318;
    --surface:#14181f;
    --surface-muted:#191e26;
    --surface-hover:#202631;
    --text:#f3f6fa;
    --text-muted:#aab3c2;
    --text-soft:#737e8f;
    --border:#2a313d;
    --border-strong:#374151;
    --accent:#5b9cff;
    --accent-hover:#76acff;
    --accent-soft:rgba(91,156,255,.12);
    --success:#44c78a;
    --warning:#f2b84b;
    --danger:#ef6a73;
    --danger-soft:rgba(239,106,115,.12);
    --success-soft:rgba(68,199,138,.12);
    --warning-soft:rgba(242,184,75,.12);
    --placeholder:#737e8f;
    --focus-ring:rgba(91,156,255,.24);
    --shadow:0 18px 50px rgba(0,0,0,.32);
  }
}
:root[data-theme="dark"] {
  --page-bg:#0b0d10;
  --sidebar-bg:#101318;
  --surface:#14181f;
  --surface-muted:#191e26;
  --surface-hover:#202631;
  --text:#f3f6fa;
  --text-muted:#aab3c2;
  --text-soft:#737e8f;
  --border:#2a313d;
  --border-strong:#374151;
  --accent:#5b9cff;
  --accent-hover:#76acff;
  --accent-soft:rgba(91,156,255,.12);
  --success:#44c78a;
  --warning:#f2b84b;
  --danger:#ef6a73;
  --danger-soft:rgba(239,106,115,.12);
  --success-soft:rgba(68,199,138,.12);
  --warning-soft:rgba(242,184,75,.12);
  --placeholder:#737e8f;
  --focus-ring:rgba(91,156,255,.24);
  --shadow:0 18px 50px rgba(0,0,0,.32);
}
* { box-sizing:border-box; }
body { margin:0; font:14px/1.45 Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color:var(--text); background:var(--page-bg); }
button, input, textarea, select { font:inherit; }
button, .button { appearance:none; border:1px solid var(--border); background:var(--surface-muted); color:var(--text); min-height:38px; padding:8px 12px; border-radius:var(--radius-sm); cursor:pointer; text-decoration:none; display:inline-flex; align-items:center; justify-content:center; gap:8px; font-weight:650; white-space:nowrap; }
button:hover, .button:hover { border-color:var(--border-strong); background:var(--surface-hover); }
button:focus-visible, .button:focus-visible, input:focus, textarea:focus, select:focus { border-color:var(--accent); box-shadow:0 0 0 3px var(--focus-ring); outline:none; }
button:disabled { cursor:not-allowed; opacity:.68; }
.button--primary { background:var(--accent); border-color:var(--accent); color:#fff; }
.button--primary:hover { background:var(--accent-hover); border-color:var(--accent-hover); }
.button--danger { border-color:transparent; color:var(--danger); background:transparent; }
.button--danger:hover { border-color:var(--danger); background:var(--danger-soft); }
.button--small { min-height:32px; padding:6px 9px; font-size:13px; }
h1, h2, h3, p { margin:0; }
h1 { font-size:30px; line-height:1.12; letter-spacing:0; font-weight:720; }
h2 { font-size:19px; line-height:1.2; }
h3 { font-size:15px; line-height:1.25; }
.app-layout { min-height:100vh; display:grid; grid-template-columns:var(--sidebar-width) minmax(0, 1fr); }
.sidebar { position:sticky; top:0; height:100vh; border-right:1px solid var(--border); background:var(--sidebar-bg); padding:18px 14px; display:flex; flex-direction:column; gap:18px; }
.brand { display:flex; gap:11px; align-items:center; padding:4px 8px 12px; }
.brand-mark { width:34px; height:34px; border-radius:8px; display:grid; place-items:center; color:#fff; background:linear-gradient(135deg, var(--accent), #44c78a); font-weight:850; }
.brand-name { font-size:17px; font-weight:760; }
.active-context { margin:-10px 8px 0; padding:9px 10px; border:1px solid var(--border); border-radius:var(--radius-sm); background:var(--surface-muted); min-width:0; }
.active-context .eyebrow { margin-bottom:3px; }
.active-context__name { display:block; font-weight:750; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.subtitle, .page-description, .section-heading p, .help, .empty-state p, .repo-meta, .muted { color:var(--text-muted); }
.subtitle { font-size:12px; }
.nav-links { display:grid; gap:4px; }
.nav-links a { position:relative; min-height:40px; display:flex; align-items:center; gap:10px; color:var(--text-muted); text-decoration:none; font-weight:650; padding:9px 10px 9px 13px; border-radius:var(--radius-sm); }
.nav-links a:hover { background:var(--surface-hover); color:var(--text); }
.nav-links a.is-active { background:var(--accent-soft); color:var(--text); }
.nav-links a.is-active:before { content:""; position:absolute; left:0; top:9px; bottom:9px; width:3px; border-radius:99px; background:var(--accent); }
.nav-icon { width:18px; height:18px; display:grid; place-items:center; color:var(--text-soft); }
.nav-links a.is-active .nav-icon { color:var(--accent); }
.sidebar-footer { margin-top:auto; display:grid; gap:8px; padding-top:12px; border-top:1px solid var(--border); }
.mobile-bar { display:none; min-height:58px; align-items:center; justify-content:space-between; gap:12px; border-bottom:1px solid var(--border); background:var(--sidebar-bg); padding:10px 14px; position:sticky; top:0; z-index:50; }
.hamburger { width:40px; min-width:40px; padding:0; }
.hamburger-lines { width:18px; display:grid; gap:4px; }
.hamburger-lines span { height:2px; border-radius:99px; background:currentColor; }
.mobile-nav-backdrop, .mobile-nav-panel { display:none; }
.page-shell { width:min(100% - 48px, var(--content-width)); margin:0 auto; padding:30px 0 72px; }
.page-header { display:flex; align-items:flex-start; justify-content:space-between; gap:20px; margin-bottom:22px; }
.page-description { margin-top:6px; font-size:15px; max-width:680px; }
.alert { margin-bottom:14px; padding:11px 13px; border-radius:var(--radius-sm); display:flex; align-items:flex-start; gap:10px; border:1px solid var(--border); background:var(--surface); font-weight:650; }
.alert__icon { flex:0 0 auto; font-weight:800; }
.alert--success { background:var(--success-soft); border-color:rgba(68,199,138,.35); color:var(--text); }
.alert--error { background:var(--danger-soft); border-color:rgba(239,106,115,.38); color:var(--text); }
.panel { background:var(--surface); border:1px solid var(--border); border-radius:var(--radius-md); box-shadow:var(--shadow); padding:20px; margin-bottom:18px; }
.section { margin-top:28px; }
.flat-section { margin-bottom:26px; }
.eyebrow { display:block; color:var(--text-soft); font-size:12px; font-weight:700; margin-bottom:5px; }
code, .mono { font-family:"SFMono-Regular", Consolas, "Liberation Mono", monospace; }
.project-root-bar { min-height:44px; display:flex; align-items:center; justify-content:space-between; gap:12px; padding:9px 12px; border:1px solid var(--border); border-radius:var(--radius-sm); background:var(--surface-muted); }
.project-root { color:var(--text); overflow-wrap:anywhere; font-size:13px; }
.section-heading { display:flex; align-items:flex-start; justify-content:space-between; gap:16px; margin-bottom:16px; }
.toolbar { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-bottom:12px; }
.toolbar-controls { display:flex; align-items:center; gap:8px; flex-wrap:wrap; }
.field-grid { display:grid; gap:14px; }
.grid-2 { grid-template-columns:repeat(2, minmax(0, 1fr)); }
.grid-3 { grid-template-columns:repeat(3, minmax(0, 1fr)); }
.field { min-width:0; }
label { display:block; color:var(--text); font-size:13px; font-weight:650; margin-bottom:6px; }
input, textarea, select { width:100%; min-width:0; min-height:40px; border:1px solid var(--border); border-radius:var(--radius-sm); padding:8px 10px; background:var(--surface); color:var(--text); }
input::placeholder, textarea::placeholder { color:var(--placeholder); opacity:1; }
textarea { min-height:124px; resize:vertical; white-space:pre-wrap; }
.help { margin-top:5px; font-size:12px; line-height:1.35; }
.field + .field, .field-grid + .field, .field + .field-grid, .field-grid + .field-grid { margin-top:14px; }
.inline-actions, .form-actions { display:flex; align-items:center; gap:8px; }
.repo-table { width:100%; max-width:100%; table-layout:fixed; border-collapse:separate; border-spacing:0; overflow:hidden; border:1px solid var(--border); border-radius:var(--radius-md); background:var(--surface); }
.repo-table th { height:38px; padding:0 14px; text-align:left; color:var(--text-soft); font-size:12px; font-weight:700; border-bottom:1px solid var(--border); background:var(--surface-muted); }
.repo-table td { height:72px; padding:12px 14px; border-bottom:1px solid var(--border); vertical-align:middle; min-width:0; overflow-wrap:anywhere; }
.repo-table td:last-child, .repo-table th:last-child { width:190px; }
.repo-table tr:last-child td { border-bottom:0; }
.repo-table tbody tr:hover { background:var(--surface-muted); }
.repo-row__main { min-width:0; display:grid; gap:4px; color:var(--text); text-decoration:none; }
.repo-row__main:hover .repo-name { color:var(--accent); }
.repo-name { display:block; font-weight:750; line-height:1.25; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.repo-meta { display:block; font-size:12px; line-height:1.35; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.repo-card-grid { display:grid; grid-template-columns:repeat(2, minmax(0, 1fr)); gap:14px; }
.repo-card { position:relative; min-width:0; min-height:220px; display:flex; flex-direction:column; gap:18px; padding:18px; border:1px solid var(--border); border-radius:var(--radius-md); background:var(--surface); box-shadow:var(--shadow); transition:border-color .15s ease, transform .15s ease; }
.repo-card:hover, .repo-card:focus-within { z-index:2; border-color:var(--border-strong); transform:translateY(-1px); }
.repo-card__header { display:flex; align-items:flex-start; justify-content:space-between; gap:16px; min-width:0; }
.repo-card__header .repo-row__main { flex:1 1 auto; }
.repo-card .repo-name { font-size:16px; white-space:normal; overflow-wrap:anywhere; }
.repo-card .repo-meta { white-space:normal; overflow-wrap:anywhere; }
.repo-card__details { display:grid; grid-template-columns:minmax(0, 1.4fr) minmax(110px, .8fr) minmax(110px, .8fr); gap:14px; }
.repo-card__detail { min-width:0; }
.repo-card__detail .eyebrow { margin-bottom:6px; }
.repo-card__actions { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-top:auto; padding-top:14px; border-top:1px solid var(--border); }
.repo-card__actions > .muted { font-size:12px; }
.repo-card__menu { position:relative; }
.repo-card__menu summary { list-style:none; }
.repo-card__menu summary::-webkit-details-marker { display:none; }
.repo-card__menu-panel { position:absolute; right:0; top:calc(100% + 6px); z-index:5; min-width:190px; display:grid; gap:6px; margin:0; padding:8px; }
.branch-badge, .badge { border:1px solid var(--border); background:var(--surface-muted); color:var(--text-muted); border-radius:999px; padding:3px 8px; font-size:12px; font-family:"SFMono-Regular", Consolas, "Liberation Mono", monospace; display:inline-flex; align-items:center; gap:5px; }
.badge--generated { color:var(--accent); background:var(--accent-soft); border-color:rgba(91,156,255,.28); }
.status { display:inline-flex; align-items:center; gap:8px; font-weight:650; }
.status-dot { width:8px; height:8px; border-radius:50%; background:var(--text-soft); box-shadow:0 0 0 3px rgba(115,126,143,.12); }
.status--synced .status-dot { background:var(--success); box-shadow:0 0 0 3px rgba(68,199,138,.14); }
.status--changes .status-dot { background:var(--warning); box-shadow:0 0 0 3px rgba(242,184,75,.16); }
.status--error .status-dot { background:var(--danger); box-shadow:0 0 0 3px rgba(239,106,115,.16); }
.file-browser { display:grid; grid-template-columns:300px minmax(0, 1fr); gap:18px; align-items:start; }
.file-sidebar { border-right:1px solid var(--border); padding-right:14px; }
.file-group + .file-group { margin-top:18px; }
.file-group-title { color:var(--text-soft); font-size:12px; font-weight:700; margin-bottom:6px; }
.file-row { display:flex; align-items:center; gap:9px; padding:9px 10px; border-radius:var(--radius-sm); text-decoration:none; color:var(--text); min-width:0; }
.file-row:hover, .file-row.is-active { background:var(--accent-soft); color:var(--text); }
.file-name { display:block; font-weight:750; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.file-description { display:block; color:var(--text-muted); font-size:12px; margin-top:3px; }
.file-viewer { margin:0; overflow:auto; max-height:calc(100vh - 330px); min-height:360px; border:1px solid var(--border); border-radius:var(--radius-sm); background:var(--surface-muted); padding:14px; white-space:pre; }
.notice { border:1px solid var(--border); background:var(--surface-muted); border-radius:var(--radius-sm); padding:11px 12px; margin-bottom:12px; color:var(--text-muted); }
.terminal { height:560px; min-height:420px; border:1px solid var(--border-strong); border-radius:var(--radius-sm); overflow:hidden; background:#000; }
.terminal--compact { height:300px; min-height:260px; }
.terminal-toolbar { min-height:40px; display:flex; align-items:center; justify-content:space-between; gap:12px; padding:8px 10px; border-bottom:1px solid #222; background:#0a0a0a; color:#d4d4d4; }
.terminal-status { color:#9ca3af; font-size:12px; }
.terminal-viewport { width:100%; height:calc(100% - 40px); min-height:0; padding:4px; background:#000; }
.terminal-viewport .xterm { height:100%; }
.terminal-viewport .xterm-viewport { background:#000 !important; scrollbar-width:thin; scrollbar-color:#3a3a3a transparent; }
.terminal-viewport .xterm-viewport::-webkit-scrollbar { width:8px; height:8px; }
.terminal-viewport .xterm-viewport::-webkit-scrollbar-thumb { background:#3a3a3a; border-radius:999px; }
.empty-state { border:1px dashed var(--border-strong); background:var(--surface-muted); border-radius:var(--radius-md); padding:34px 18px; text-align:center; }
.empty-state h3 { font-size:16px; margin-bottom:4px; }
.drawer-backdrop { position:fixed; inset:0; z-index:40; background:rgba(0,0,0,.48); opacity:0; pointer-events:none; transition:opacity .18s ease; }
.drawer { position:fixed; z-index:41; top:0; right:0; width:min(480px, 100vw); height:100vh; background:var(--surface); border-left:1px solid var(--border); box-shadow:var(--shadow); transform:translateX(100%); transition:transform .18s ease; padding:22px; overflow:auto; }
body.drawer-open .drawer-backdrop { opacity:1; pointer-events:auto; }
body.drawer-open .drawer { transform:translateX(0); }
.preview-box { display:grid; gap:10px; border:1px solid var(--border); border-radius:var(--radius-sm); background:var(--surface-muted); padding:12px; }
.env-fields { display:grid; gap:12px; }
.env-row { display:grid; grid-template-columns:minmax(180px, 1fr) minmax(320px, 2fr); gap:14px; align-items:start; }
.env-row > div { min-width:0; }
.env-key { margin:11px 0 0; overflow-wrap:anywhere; color:var(--text-muted); }
.split-actions { display:grid; grid-template-columns:repeat(2, minmax(0, 1fr)); gap:14px; }
.danger-panel { border-color:rgba(239,106,115,.35); }
.technical-details { max-width:680px; }
[hidden] { display:none !important; }
@media (max-width: 900px) {
  .app-layout { display:block; }
  .sidebar { display:none; }
  .mobile-bar { display:flex; }
  .mobile-nav-backdrop { position:fixed; inset:0; z-index:60; display:block; background:rgba(0,0,0,.5); opacity:0; pointer-events:none; transition:opacity .18s ease; }
  .mobile-nav-panel { position:fixed; top:0; left:0; right:0; z-index:61; display:grid; gap:14px; background:var(--sidebar-bg); border-bottom:1px solid var(--border); box-shadow:var(--shadow); padding:10px 14px 16px; transform:translateY(-100%); transition:transform .18s ease; }
  .mobile-nav-header { min-height:38px; display:flex; align-items:center; justify-content:space-between; gap:12px; }
  .mobile-nav { display:grid; }
  body.mobile-nav-open { overflow:hidden; }
  body.mobile-nav-open .mobile-nav-backdrop { opacity:1; pointer-events:auto; }
  body.mobile-nav-open .mobile-nav-panel { transform:translateY(0); }
  .page-shell { width:min(100% - 28px, var(--content-width)); padding-top:22px; }
  .page-header, .section-heading, .form-actions, .toolbar, .split-actions { align-items:stretch; flex-direction:column; grid-template-columns:1fr; }
  .toolbar-controls, .inline-actions { width:100%; }
  .toolbar-controls > *, .inline-actions > *, .inline-actions form, .inline-actions button, .inline-actions .button { flex:1 1 auto; }
  .panel { padding:14px; }
  .grid-2, .grid-3 { grid-template-columns:1fr; }
  .repo-table { display:block; overflow:auto; }
  .repo-card-grid { grid-template-columns:1fr; }
  .file-browser { grid-template-columns:1fr; }
  .file-sidebar { border-right:0; border-bottom:1px solid var(--border); padding-right:0; padding-bottom:14px; }
  .terminal { height:460px; min-height:360px; }
  .terminal--compact { height:280px; min-height:240px; }
  .env-row { grid-template-columns:1fr; gap:6px; }
  .env-key { margin:0; }
}
@media (max-width: 560px) {
  .repo-card__header, .repo-card__actions { align-items:stretch; flex-direction:column; }
  .repo-card__details { grid-template-columns:1fr; }
  .repo-card__actions .inline-actions { display:grid; grid-template-columns:repeat(3, minmax(0, 1fr)); }
  .repo-card__menu-panel { right:auto; left:0; }
}
</style>
</head>
<body>
<div class="mobile-bar">
  <div class="brand">
    <div class="brand-mark">U</div>
    <div><div class="brand-name">Uppr</div><p class="subtitle">{{if .ActiveRepo}}Repo: {{.ActiveRepo}}{{else}}Infrastructure workspace{{end}}</p></div>
  </div>
  <button class="button button--small hamburger" type="button" data-mobile-menu aria-label="Open navigation" aria-controls="mobile-navigation" aria-expanded="false"><span class="hamburger-lines" aria-hidden="true"><span></span><span></span><span></span></span></button>
</div>
<div class="mobile-nav-backdrop" data-mobile-close></div>
<div class="mobile-nav-panel" id="mobile-navigation">
  <div class="mobile-nav-header">
    <div class="brand">
      <div class="brand-mark">U</div>
      <div><div class="brand-name">Uppr</div><p class="subtitle">Infrastructure workspace</p></div>
    </div>
    <button class="button button--small" type="button" data-mobile-close>Close</button>
  </div>
  <nav class="nav-links mobile-nav" aria-label="Mobile primary">
    <a href="/workspaces" data-nav-link data-mobile-close><span class="nav-icon">##</span>Workspaces</a>
    <a href="{{.BasePath}}/repos" data-nav-link data-mobile-close><span class="nav-icon">[]</span>Repositories</a>
    <a href="{{.BasePath}}/credentials" data-nav-link data-mobile-close><span class="nav-icon">--</span>Credentials</a>
    {{if .BasePath}}<a href="/files" data-nav-link data-mobile-close><span class="nav-icon">{}</span>Top-level files</a>{{end}}
    <a href="{{.BasePath}}/files" data-nav-link data-mobile-close><span class="nav-icon">{}</span>{{if .BasePath}}Workspace files{{else}}Files{{end}}</a>
    <a href="{{.BasePath}}/terminal" data-nav-link data-mobile-close><span class="nav-icon">$_</span>Terminal</a>
    <a href="{{.BasePath}}/sync" data-nav-link data-mobile-close><span class="nav-icon">&lt;&gt;</span>Sync</a>
    <a href="{{.BasePath}}/launch" data-nav-link data-mobile-close><span class="nav-icon">&gt;</span>Launch</a>
  </nav>
</div>
<div class="app-layout">
  <aside class="sidebar">
    <div class="brand">
      <div class="brand-mark">U</div>
      <div><div class="brand-name">Uppr</div><p class="subtitle">Infrastructure workspace</p></div>
    </div>
    {{if .ActiveRepo}}
    <div class="active-context">
      <span class="eyebrow">Current repo</span>
      <span class="active-context__name mono" title="{{.ActiveRepo}}">{{.ActiveRepo}}</span>
    </div>
    {{end}}
    <nav class="nav-links" aria-label="Primary">
      <a href="/workspaces" data-nav-link><span class="nav-icon">##</span>Workspaces</a>
      <a href="{{.BasePath}}/repos" data-nav-link><span class="nav-icon">[]</span>Repositories</a>
      <a href="{{.BasePath}}/credentials" data-nav-link><span class="nav-icon">--</span>Credentials</a>
      {{if .BasePath}}<a href="/files" data-nav-link><span class="nav-icon">{}</span>Top-level files</a>{{end}}
      <a href="{{.BasePath}}/files" data-nav-link><span class="nav-icon">{}</span>{{if .BasePath}}Workspace files{{else}}Files{{end}}</a>
      <a href="{{.BasePath}}/terminal" data-nav-link><span class="nav-icon">$_</span>Terminal</a>
      <a href="{{.BasePath}}/sync" data-nav-link><span class="nav-icon">&lt;&gt;</span>Sync</a>
      <a href="{{.BasePath}}/launch" data-nav-link><span class="nav-icon">&gt;</span>Launch</a>
    </nav>
    <div class="sidebar-footer">
      <button class="button theme-toggle" type="button" data-theme-toggle aria-pressed="false">Dark Mode</button>
    </div>
  </aside>
  <main class="page-shell">
  {{if .Message}}<div class="alert alert--success" role="status"><span class="alert__icon" aria-hidden="true">✓</span><span>{{messageText .Message}}</span></div>{{end}}
  {{if .Error}}<div class="alert alert--error" role="alert"><span class="alert__icon" aria-hidden="true">!</span><span>{{.Error}}</span></div>{{end}}
  ` + body + `
  </main>
</div>
<script>
(function () {
  var themeToggle = document.querySelector('[data-theme-toggle]');
  var mobileMenu = document.querySelector('[data-mobile-menu]');
  var mobileNavPanel = document.querySelector('#mobile-navigation');

  function setMobileNav(open) {
    document.body.classList.toggle('mobile-nav-open', open);
    if (mobileMenu) {
      mobileMenu.setAttribute('aria-expanded', open ? 'true' : 'false');
      mobileMenu.setAttribute('aria-label', open ? 'Close navigation' : 'Open navigation');
    }
    if (open && mobileNavPanel) {
      var first = mobileNavPanel.querySelector('a, button');
      if (first) { setTimeout(function () { first.focus(); }, 80); }
    }
  }

  function preferredTheme() {
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  function activeTheme() {
    return document.documentElement.getAttribute('data-theme') || preferredTheme();
  }

  function updateThemeToggle() {
    if (!themeToggle) {
      return;
    }
    var isDark = activeTheme() === 'dark';
    themeToggle.textContent = isDark ? 'Light Mode' : 'Dark Mode';
    themeToggle.setAttribute('aria-pressed', isDark ? 'true' : 'false');
  }

  function setTheme(theme) {
    document.documentElement.setAttribute('data-theme', theme);
    try {
      window.localStorage.setItem('uppr-theme', theme);
    } catch (err) {}
    updateThemeToggle();
  }

  document.querySelectorAll('[data-nav-link]').forEach(function (link) {
    var target = link.getAttribute('href');
    if (target === '/repos' && (location.pathname === '/repos' || location.pathname.indexOf('/repos/') === 0)) {
      link.classList.add('is-active');
    } else if (target !== '/repos' && location.pathname.indexOf(target) === 0) {
      link.classList.add('is-active');
    }
  });

  if (mobileMenu) {
    mobileMenu.addEventListener('click', function () {
      setMobileNav(!document.body.classList.contains('mobile-nav-open'));
    });
  }

  document.querySelectorAll('[data-mobile-close]').forEach(function (control) {
    control.addEventListener('click', function () {
      setMobileNav(false);
    });
  });

  document.addEventListener('keydown', function (event) {
    if (event.key === 'Escape') {
      setMobileNav(false);
    }
  });

  if (window.matchMedia) {
    var desktopQuery = window.matchMedia('(min-width: 901px)');
    var closeMobileNavOnDesktop = function (event) {
      if (event.matches) {
        setMobileNav(false);
      }
    };
    if (desktopQuery.addEventListener) {
      desktopQuery.addEventListener('change', closeMobileNavOnDesktop);
    } else if (desktopQuery.addListener) {
      desktopQuery.addListener(closeMobileNavOnDesktop);
    }
  }

  if (themeToggle) {
    themeToggle.addEventListener('click', function () {
      setTheme(activeTheme() === 'dark' ? 'light' : 'dark');
    });
    updateThemeToggle();
  }

  document.querySelectorAll('form[data-loading-label]').forEach(function (form) {
    form.addEventListener('submit', function () {
      var button = form.querySelector('button[type="submit"]');
      if (button) {
        button.textContent = form.getAttribute('data-loading-label');
        button.disabled = true;
      }
    });
  });

  document.querySelectorAll('[data-copy]').forEach(function (button) {
    button.addEventListener('click', function () {
      var text = button.getAttribute('data-copy') || '';
      if (navigator.clipboard && text) {
        navigator.clipboard.writeText(text);
        button.textContent = 'Copied';
        setTimeout(function () { button.textContent = button.getAttribute('data-label') || 'Copy'; }, 1200);
      }
    });
  });

  document.querySelectorAll('[data-copy-code]').forEach(function (button) {
    button.addEventListener('click', function () {
      var block = document.querySelector('[data-code-block]');
      var text = block ? block.textContent : '';
      if (navigator.clipboard && text) {
        navigator.clipboard.writeText(text);
        button.textContent = 'Copied';
        setTimeout(function () { button.textContent = button.getAttribute('data-label') || 'Copy contents'; }, 1200);
      }
    });
  });

  var ownerInput = document.querySelector('#new-github-owner');
  var repoInput = document.querySelector('#new-github-repo');
  var pathInput = document.querySelector('#new-path');
  var githubPreview = document.querySelector('[data-repo-preview-github]');
  var pathPreview = document.querySelector('[data-repo-preview-path]');
  function updateRepoPreview() {
    if (!ownerInput || !repoInput) { return; }
    var owner = ownerInput.value.trim() || 'owner';
    var repo = repoInput.value.trim() || 'repository';
    var path = pathInput && pathInput.value.trim() ? pathInput.value.trim() : 'apps/' + repo;
    if (githubPreview) { githubPreview.textContent = owner + '/' + repo; }
    if (pathPreview) { pathPreview.textContent = '{{.Root}}/' + path; }
  }
  [ownerInput, repoInput, pathInput].forEach(function (input) {
    if (input) { input.addEventListener('input', updateRepoPreview); }
  });
  updateRepoPreview();

  document.querySelectorAll('[data-open-drawer]').forEach(function (button) {
    button.addEventListener('click', function () {
      document.body.classList.add('drawer-open');
      var first = document.querySelector('.drawer input');
      if (first) { setTimeout(function () { first.focus(); }, 80); }
    });
  });

  document.querySelectorAll('[data-close-drawer]').forEach(function (button) {
    button.addEventListener('click', function () {
      document.body.classList.remove('drawer-open');
    });
  });

  document.querySelectorAll('[data-password-toggle]').forEach(function (button) {
    button.addEventListener('click', function () {
      var input = document.querySelector(button.getAttribute('data-password-toggle'));
      if (!input) { return; }
      input.type = input.type === 'password' ? 'text' : 'password';
      button.textContent = input.type === 'password' ? 'Show' : 'Hide';
    });
  });

  var repoSearch = document.querySelector('[data-repo-search]');
  var repoFilter = document.querySelector('[data-repo-filter]');
  var repoSort = document.querySelector('[data-repo-sort]');
  var repoCount = document.querySelector('[data-repo-count]');
  function updateRepos() {
    var rows = Array.prototype.slice.call(document.querySelectorAll('[data-repo-row]'));
    var query = repoSearch ? repoSearch.value.trim().toLowerCase() : '';
    var filter = repoFilter ? repoFilter.value : 'all';
    var visible = 0;
    rows.forEach(function (row) {
      var matchesQuery = !query || row.textContent.toLowerCase().indexOf(query) !== -1;
      var matchesFilter = filter === 'all' || row.getAttribute('data-status') === filter;
      var show = matchesQuery && matchesFilter;
      row.hidden = !show;
      if (show) { visible++; }
    });
    if (repoSort) {
      var list = document.querySelector('[data-repo-list]');
      rows.sort(function (a, b) {
        var key = repoSort.value === 'sync' ? 'sync' : 'name';
        return (a.getAttribute('data-' + key) || '').localeCompare(b.getAttribute('data-' + key) || '');
      }).forEach(function (row) { if (list) { list.appendChild(row); } });
    }
    if (repoCount) {
      repoCount.textContent = visible + (visible === 1 ? ' repository' : ' repositories');
    }
  }
  [repoSearch, repoFilter, repoSort].forEach(function (control) {
    if (control) { control.addEventListener('input', updateRepos); control.addEventListener('change', updateRepos); }
  });
  updateRepos();

  document.querySelectorAll('form[data-confirm]').forEach(function (form) {
    form.addEventListener('submit', function (event) {
      if (!window.confirm(form.getAttribute('data-confirm'))) {
        event.preventDefault();
      }
    });
  });
})();
</script>
</body>
</html>`
}

const workspacesBody = `
<header class="page-header">
  <div>
    <h1>Workspaces</h1>
    <p class="page-description">Create and select isolated workspace directories. Each workspace owns its repositories and generated runtime files.</p>
  </div>
</header>

<section class="flat-section" aria-labelledby="server-root-heading">
  <div class="project-root-bar">
    <div>
      <span class="eyebrow" id="server-root-heading">Server root</span>
      <code class="project-root">{{.Root}}</code>
    </div>
    <button class="button button--small" type="button" data-copy="{{.Root}}" data-label="Copy">Copy</button>
  </div>
</section>

<section class="panel technical-details" aria-labelledby="new-workspace-heading">
  <div class="section-heading">
    <div>
      <h2 id="new-workspace-heading">New workspace</h2>
      <p>Workspace names become directories in Uppr's configured workspace storage location.</p>
    </div>
  </div>
  <form method="post" action="/workspaces" data-loading-label="Creating...">
    <div class="field">
      <label for="workspace-name">Workspace name</label>
      <input id="workspace-name" name="name" value="{{.NewName}}" placeholder="production" autocomplete="off" required pattern="[A-Za-z0-9_. -]+">
    </div>
    <div class="form-actions" style="margin-top:14px; justify-content:flex-end;">
      <button class="button button--primary" type="submit">Create workspace</button>
    </div>
  </form>
</section>

<section class="flat-section" aria-labelledby="workspaces-heading">
  <div class="section-heading">
    <div>
      <h2 id="workspaces-heading">Workspace registry</h2>
      <p>{{len .Workspaces}} configured.</p>
    </div>
    <form method="post" action="/generate" data-loading-label="Generating...">
      <button class="button" type="submit">Generate master files</button>
    </form>
  </div>
  {{if .Workspaces}}
  <table class="repo-table">
    <thead>
      <tr>
        <th>Workspace</th>
        <th>Actions</th>
      </tr>
    </thead>
    <tbody>
    {{range .Workspaces}}
      <tr>
        <td><a class="repo-row__main" href="/workspaces/{{.Name}}/repos"><span class="repo-name">{{.Name}}</span><span class="repo-meta mono">{{.Path}}</span></a></td>
        <td>
          <div class="inline-actions">
            <a class="button button--small" href="/workspaces/{{.Name}}/repos">Open</a>
            <form method="post" action="/workspaces/{{.Name}}/delete" data-confirm="Remove this workspace from the registry? Files on disk are left alone.">
              <button class="button button--danger button--small" type="submit">Delete</button>
            </form>
          </div>
        </td>
      </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}
  <div class="empty-state">
    <h3>No workspaces configured</h3>
    <p>Create a workspace before registering repositories.</p>
  </div>
  {{end}}
</section>
`

const indexBody = `
<header class="page-header">
  <div>
    <h1>Repositories</h1>
    <p class="page-description">Manage source repositories connected to this workspace.</p>
  </div>
  <div class="inline-actions">
    <a class="button" href="{{.BasePath}}/env/download">Download env</a>
    <button class="button button--primary" type="button" data-open-drawer>Add repository</button>
  </div>
</header>

<section class="flat-section" aria-labelledby="project-root-heading">
  <div class="project-root-bar">
    <div>
      <span class="eyebrow" id="project-root-heading">Project root</span>
      <code class="project-root">{{.Root}}</code>
    </div>
    <button class="button button--small" type="button" data-copy="{{.Root}}" data-label="Copy" title="Copy the workspace root used for config and generated files.">Copy</button>
  </div>
</section>

<section class="flat-section" aria-labelledby="repositories-heading">
  <div class="toolbar">
    <div>
      <h2 id="repositories-heading">Repository dashboard</h2>
      <p class="muted"><span data-repo-count>{{len .Repos}} repositories</span> configured.</p>
    </div>
    <div class="toolbar-controls">
      <input data-repo-search placeholder="Search repositories" autocomplete="off" aria-label="Search repositories">
      <select data-repo-filter aria-label="Filter by status">
        <option value="all">All statuses</option>
        <option value="synced">Synced</option>
        <option value="changes">Changes</option>
        <option value="unknown">Unknown</option>
        <option value="error">Error</option>
      </select>
      <select data-repo-sort aria-label="Sort repositories">
        <option value="name">Sort by name</option>
        <option value="sync">Sort by last sync</option>
      </select>
    </div>
  </div>
  {{if .Repos}}
  <div class="repo-card-grid" data-repo-list>
    {{range $index, $repo := .Repos}}
    {{$status := repoStatus $.Root $repo}}
    <article class="repo-card" data-repo-row data-name="{{if .Name}}{{.Name}}{{else}}{{.URL}}{{end}}" data-status="{{$status.Key}}" data-sync="{{repoLastSync $.Root .}}">
      <div class="repo-card__header">
        <a class="repo-row__main" href="{{$.BasePath}}/repos/{{$index}}">
          <span class="repo-name">{{if .Name}}{{.Name}}{{else}}{{.URL}}{{end}}</span>
          <span class="repo-meta mono">{{.URL}}</span>
        </a>
        <span class="status status--{{$status.Key}}"><span class="status-dot"></span>{{$status.Label}}</span>
      </div>
      <div class="repo-card__details">
        <div class="repo-card__detail">
          <span class="eyebrow">Local path</span>
          <code class="mono repo-meta">{{if .Path}}{{.Path}}{{else}}{{.URL}}{{end}}</code>
        </div>
        <div class="repo-card__detail">
          <span class="eyebrow">Branch</span>
          <span class="branch-badge">{{branchLabel .Branch}}</span>
        </div>
        <div class="repo-card__detail">
          <span class="eyebrow">Last sync</span>
          <span class="muted">{{repoLastSync $.Root .}}</span>
        </div>
      </div>
      <div class="repo-card__actions">
        <span class="muted">Repository actions</span>
        <div class="inline-actions">
          <form method="post" action="{{$.BasePath}}/repos/{{$index}}/pull" data-loading-label="Pulling...">
            <button class="button button--small" type="submit">Pull</button>
          </form>
          <a class="button button--small" href="{{$.BasePath}}/repos/{{$index}}">Open</a>
          <details class="repo-card__menu">
            <summary class="button button--small" aria-label="More repository actions">More</summary>
            <div class="panel repo-card__menu-panel">
              <a class="button button--small" href="{{$.BasePath}}/repos/{{$index}}">Edit configuration</a>
              <button class="button button--small" type="button" data-copy="{{absPath $.Root .Path}}" data-label="Copy path">Copy path</button>
              <a class="button button--small" href="{{.URL}}" target="_blank" rel="noreferrer">Open on GitHub</a>
              <form method="post" action="{{$.BasePath}}/repos/{{$index}}/delete" data-confirm="Delete this repository from the configuration?">
                <button class="button button--danger button--small" type="submit">Delete repository</button>
              </form>
            </div>
          </details>
        </div>
      </div>
    </article>
    {{end}}
  </div>
  {{else}}
  <div class="empty-state">
    <h3>No repositories configured</h3>
    <p>Connect your first GitHub repository to begin generating and synchronizing infrastructure files.</p>
    <div style="margin-top:16px;"><button class="button button--primary" type="button" data-open-drawer>Add repository</button></div>
  </div>
  {{end}}
</section>

<div class="drawer-backdrop" data-close-drawer></div>
<aside class="drawer" aria-labelledby="add-repository-heading">
  <div class="section-heading">
    <div>
      <h2 id="add-repository-heading">Add repository</h2>
      <p>Connect a GitHub repository and choose where it will live locally.</p>
    </div>
    <button class="button button--small" type="button" data-close-drawer>Cancel</button>
  </div>
  <form method="post" action="{{.BasePath}}/repos" data-loading-label="Adding...">
    <div class="field">
      <label for="new-github-owner">GitHub owner</label>
      <input class="mono" id="new-github-owner" name="github_owner" value="{{githubOwner .NewRepo.URL}}" placeholder="phillip-england" autocomplete="username" required pattern="[A-Za-z0-9_.-]+">
    </div>
    <div class="field">
      <label for="new-github-repo">Repository name</label>
      <input class="mono" id="new-github-repo" name="github_repo" value="{{if .NewRepo.Name}}{{.NewRepo.Name}}{{else}}{{githubRepo .NewRepo.URL}}{{end}}" placeholder="uppr" autocomplete="off" required pattern="[A-Za-z0-9_.-]+">
    </div>
    <div class="field">
      <label for="new-branch">Branch</label>
      <input class="mono" id="new-branch" name="branch" value="{{.NewRepo.Branch}}" placeholder="main" autocomplete="off">
    </div>
    <div class="field">
      <label for="new-path">Local directory</label>
      <input class="mono" id="new-path" name="path" value="{{.NewRepo.Path}}" placeholder="apps/uppr" autocomplete="off">
      <p class="help">Leave blank to use apps/&lt;repository-name&gt;.</p>
    </div>
    <div class="preview-box" style="margin-top:16px;">
      <div><span class="eyebrow">GitHub</span><code class="mono" data-repo-preview-github>{{githubOwner .NewRepo.URL}}/{{if .NewRepo.Name}}{{.NewRepo.Name}}{{else}}{{githubRepo .NewRepo.URL}}{{end}}</code></div>
      <div><span class="eyebrow">Local destination</span><code class="mono" data-repo-preview-path>{{.Root}}/{{if .NewRepo.Path}}{{.NewRepo.Path}}{{else}}apps/repository{{end}}</code></div>
    </div>
    <div class="form-actions" style="margin-top:18px; justify-content:flex-end;">
      <button class="button" type="button" data-close-drawer>Cancel</button>
      <button class="button button--primary" type="submit" data-default-label="Add repository">Add repository</button>
    </div>
  </form>
</aside>
`

const filesBody = `
<header class="page-header">
  <div>
    <h1>Generated files</h1>
    <p class="page-description">Review and regenerate the files that control this workspace.</p>
  </div>
  <form method="post" action="{{.BasePath}}/generate" data-loading-label="Generating...">
    <button class="button button--primary" type="submit" data-default-label="Generate files">Generate files</button>
  </form>
</header>

<section class="panel" aria-labelledby="project-files-heading">
  <div class="section-heading">
    <div>
      <h2 id="project-files-heading">Workspace file explorer</h2>
      <p>Core files are manually maintained. Generated files can be rewritten from repository configuration.</p>
    </div>
  </div>
  <div class="file-browser">
    <div class="file-sidebar">
      <div class="file-group">
        <div class="file-group-title">Core</div>
        {{range .ProjectFiles}}{{if not .Generated}}
        <a class="file-row" href="{{$.BasePath}}/files/{{.ID}}">
          <span class="nav-icon">{}</span>
          <span>
            <span class="file-name mono">{{.Name}}</span>
            <span class="file-description">{{fileModified $.Root .}}</span>
          </span>
        </a>
        {{end}}{{end}}
      </div>
      <div class="file-group">
        <div class="file-group-title">Generated</div>
        {{range .ProjectFiles}}{{if .Generated}}
        <a class="file-row" href="{{$.BasePath}}/files/{{.ID}}">
          <span class="nav-icon">[]</span>
          <span>
            <span class="file-name mono">{{.Name}}</span>
            <span class="file-description">{{fileModified $.Root .}}</span>
          </span>
        </a>
        {{end}}{{end}}
      </div>
    </div>
    <div>
      <div class="notice">Select a file from the explorer to inspect its contents, copy it, or regenerate runtime files.</div>
      <div class="preview-box">
        {{range .ProjectFiles}}
        <div style="display:flex; align-items:center; justify-content:space-between; gap:12px;">
          <div>
            <strong class="mono">{{.Name}}</strong>
            <p class="muted">{{.Description}}</p>
          </div>
          {{if .Generated}}<span class="badge badge--generated">generated</span>{{else}}<span class="badge">manual</span>{{end}}
        </div>
        {{end}}
      </div>
    </div>
  </div>
</section>
`

const credentialsBody = `
<header class="page-header">
  <div>
    <h1>GitHub credentials</h1>
    <p class="page-description">Configure the account Uppr uses for HTTPS Git operations.</p>
  </div>
</header>

<section class="panel technical-details" aria-labelledby="credentials-status-heading">
  <div class="section-heading">
    <div>
      <h2 id="credentials-status-heading">GitHub connection</h2>
      {{if .Username}}
      <p>Connected as <span class="mono">{{.Username}}</span></p>
      {{else}}
      <p>No GitHub username configured.</p>
      {{end}}
    </div>
    {{if .Password}}<span class="status status--synced"><span class="status-dot"></span>Token configured</span>{{else}}<span class="status"><span class="status-dot"></span>Not verified</span>{{end}}
  </div>
  {{if .Password}}<p class="muted">Token saved <span class="mono">{{maskSecret .Password}}</span></p>{{else}}<p class="muted">Add a personal access token before pulling or pushing private repositories.</p>{{end}}
</section>

<section class="panel technical-details" aria-labelledby="credentials-heading">
  <div class="section-heading">
    <div>
      <h2 id="credentials-heading">Credentials</h2>
      <p>GitHub account passwords are not supported. Use a personal access token.</p>
    </div>
  </div>
  <form method="post" action="{{.BasePath}}/credentials" data-loading-label="Saving...">
    <div class="field">
      <label for="github-username">GitHub username</label>
      <input id="github-username" name="username" value="{{.Username}}" placeholder="octocat" autocomplete="username">
      <p class="help">Written to GITHUB_USERNAME in config/.env.</p>
    </div>
    <div class="field">
      <label for="github-password">Personal access token</label>
      <div style="display:flex; gap:8px;">
        <input id="github-password" name="password" value="{{.Password}}" placeholder="ghp_..." autocomplete="current-password" type="password">
        <button class="button" type="button" data-password-toggle="#github-password">Show</button>
      </div>
      <p class="help">Stored locally in this workspace. Do not commit config/.env.</p>
    </div>
    <div class="form-actions" style="margin-top:18px; justify-content:flex-end;">
      <button class="button" type="button" disabled>Test connection</button>
      <button class="button button--primary" type="submit" data-default-label="Save credentials">Save credentials</button>
    </div>
  </form>
</section>

<details class="technical-details">
  <summary class="button">Technical details</summary>
  <div class="panel" style="margin-top:10px;">
    <p class="muted">Stored in <code class="mono">{{.Root}}/config/.env</code></p>
    <p class="muted">Variables: <code class="mono">GITHUB_USERNAME</code>, <code class="mono">GITHUB_PASSWORD</code></p>
    <p class="muted">The .env file must not be committed.</p>
  </div>
</details>
`

const fileBody = `
<header class="page-header">
  <div>
    <h1 class="mono">{{.File.Name}}</h1>
    <p class="page-description">{{.File.Description}}</p>
  </div>
  <div class="inline-actions">
    <a class="button" href="{{.BasePath}}/files">Back to Files</a>
    <button class="button" type="button" data-copy-code data-label="Copy contents">Copy contents</button>
    {{if .File.Generated}}
    <form method="post" action="{{.BasePath}}/generate" data-loading-label="Regenerating...">
      <button class="button button--primary" type="submit">Regenerate</button>
    </form>
    {{end}}
  </div>
</header>

<section class="panel" aria-labelledby="file-heading">
  <div class="section-heading">
    <div>
      <h2 id="file-heading">File preview</h2>
      <p><span class="badge {{if .File.Generated}}badge--generated{{end}}">{{if .File.Generated}}generated{{else}}manual{{end}}</span> <span class="muted">Last modified: {{fileModified .Root .File}}</span></p>
    </div>
  </div>
  {{if .File.Generated}}
  <div class="notice">This file is generated by Uppr. Manual changes may be overwritten.</div>
  {{end}}
  {{if .Missing}}
  <div class="empty-state">
    <h3>File has not been generated</h3>
    <p>Use Generate files to write it from repos.conf.</p>
  </div>
  {{else}}
  <pre class="file-viewer" data-code-block><code>{{.Content}}</code></pre>
  {{end}}
</section>
`

const repoBody = `
<header class="page-header">
  <div>
    <h1>{{if .Repo.Name}}{{.Repo.Name}}{{else}}Repository {{.Index}}{{end}}</h1>
    <p class="page-description mono">{{.Repo.URL}}</p>
  </div>
  <div class="inline-actions">
    <a class="button" href="{{.BasePath}}/repos">Back to Repositories</a>
    <form method="post" action="{{.BasePath}}/repos/{{.Index}}/pull" data-loading-label="Pulling...">
      <button class="button" type="submit" data-default-label="Pull">Pull</button>
    </form>
  </div>
</header>

<section class="panel" aria-labelledby="repo-shell-heading">
  <div class="section-heading">
    <div>
      <span class="eyebrow">Browser shell</span>
      <h2 id="repo-shell-heading">{{if .Repo.Name}}{{.Repo.Name}}{{else}}Repository{{end}} Shell</h2>
      <p class="mono">{{if .ShellPath}}{{.ShellPath}}{{else}}{{.Repo.Path}}{{end}}</p>
    </div>
  </div>
  {{if .ShellError}}
  <div class="empty-state">
    <h3>Shell unavailable</h3>
    <p>{{.ShellError}}</p>
  </div>
  {{else}}
  <div class="terminal terminal--compact" data-shell data-shell-url="{{.BasePath}}/repos/{{.Index}}/shell" data-shell-root="{{.ShellPath}}">
    <div class="terminal-toolbar">
      <code class="mono">{{.ShellPath}}</code>
      <span class="terminal-status" data-terminal-status aria-live="polite">Connecting...</span>
    </div>
    <div class="terminal-viewport" data-terminal-viewport></div>
  </div>
  {{end}}
</section>

<section class="panel" aria-labelledby="repo-heading">
  <div class="section-heading">
    <div>
      <h2 id="repo-heading">Configuration</h2>
      <p>Repository source, local path, and generated runtime settings.</p>
    </div>
  </div>
  <form method="post" action="{{.BasePath}}/repos/{{.Index}}/save" data-loading-label="Saving...">
    {{$rateLimit := rateLimit .Repo.RateLimit}}
    <div class="field-grid grid-2">
      <div class="field">
        <label for="repo-name">Name</label>
        <input id="repo-name" name="name" value="{{.Repo.Name}}" placeholder="api" autocomplete="off">
        <p class="help">Unique service name used by Docker Compose.</p>
      </div>
      <div class="field">
        <label for="repo-branch">Branch</label>
        <input id="repo-branch" name="branch" value="{{.Repo.Branch}}" placeholder="main" autocomplete="off">
        <p class="help">Optional Git branch to clone or pull.</p>
      </div>
    </div>

    <div class="field">
      <label for="repo-url">Repository URL</label>
      <input class="mono" id="repo-url" name="url" value="{{.Repo.URL}}" placeholder="https://github.com/acme/api" autocomplete="url" required>
      <p class="help">Unique Git source for this service.</p>
    </div>

    <div class="field">
      <label for="repo-path">Local Path</label>
      <input class="mono" id="repo-path" name="path" value="{{.Repo.Path}}" placeholder="apps/api" autocomplete="off">
      <p class="help">Unique path relative to the Uppr project root.</p>
    </div>

    <div class="field-grid grid-2">
      <div class="field">
        <label for="repo-port">Host Port</label>
        <input class="mono" id="repo-port" name="port" value="{{if .Repo.Port}}{{.Repo.Port}}{{end}}" placeholder="8080" inputmode="numeric" type="number" min="1" max="65535">
        <p class="help">Port exposed on the deployment host.</p>
      </div>
      <div class="field">
        <label for="repo-container-port">Container Port</label>
        <input class="mono" id="repo-container-port" name="container_port" value="{{if .Repo.ContainerPort}}{{.Repo.ContainerPort}}{{end}}" placeholder="3000" inputmode="numeric" type="number" min="1" max="65535">
        <p class="help">Port listened to inside the container.</p>
      </div>
    </div>

    <div class="field-grid grid-2">
      <div class="field">
        <label for="repo-domains">Domains</label>
        <textarea class="mono" id="repo-domains" name="domains" placeholder="api.example.com&#10;www.api.example.com">{{domainsText .Repo}}</textarea>
        <p class="help">One hostname per line. All hostnames proxy to this app.</p>
      </div>
      <div class="field">
        <label for="repo-rate-limit-enabled">Rate Limiting</label>
        <div class="preview-box">
          <label style="display:flex; align-items:center; gap:8px; margin:0;">
            <input id="repo-rate-limit-enabled" name="rate_limit_enabled" type="checkbox" value="true" style="width:auto; min-height:auto;" {{if $rateLimit.Enabled}}checked{{end}}>
            Enable request rate limiting
          </label>
          <div class="field-grid grid-3" style="margin-top:10px;">
            <div class="field">
              <label for="repo-rate-limit-events">Events</label>
              <input class="mono" id="repo-rate-limit-events" name="rate_limit_events" value="{{$rateLimit.Events}}" inputmode="numeric" type="number" min="1">
            </div>
            <div class="field">
              <label for="repo-rate-limit-window">Window</label>
              <input class="mono" id="repo-rate-limit-window" name="rate_limit_window" value="{{$rateLimit.Window}}" placeholder="1m" autocomplete="off">
            </div>
            <div class="field">
              <label for="repo-rate-limit-zone">Zone</label>
              <input class="mono" id="repo-rate-limit-zone" name="rate_limit_zone" value="{{$rateLimit.Zone}}" placeholder="dynamic" autocomplete="off">
            </div>
          </div>
        </div>
        <p class="help">Defaults match caddyup: 100 events per 1m, keyed by remote host.</p>
      </div>
    </div>

    <input type="hidden" name="env" value="{{joinLines .Repo.Env}}">

    <div class="field">
      <label>App Environment</label>
      {{if .AppEnv}}
      <div class="env-fields">
        {{range .AppEnv}}
        <div class="env-row">
          <label class="env-key mono" for="app-env-{{.Key}}">{{.Key}}</label>
          <div>
            <input type="hidden" name="app_env_key" value="{{.Key}}">
            <input class="mono" id="app-env-{{.Key}}" name="app_env_value" value="{{.Value}}" {{if .Example}}placeholder="{{.Example}}"{{end}} autocomplete="off">
            {{if .Description}}<p class="help">{{.Description}}</p>{{end}}
            {{if .Example}}<p class="help">Example: <code class="mono">{{.Example}}</code></p>{{end}}
          </div>
        </div>
        {{end}}
      </div>
      <p class="help">Saved to <code class="mono">{{.AppEnvPath}}</code>.</p>
      {{else}}
      <div class="empty-state">
        <h3>No environment schema found</h3>
        <p>Pull the repository or add schema.json at the app root.</p>
      </div>
      {{end}}
    </div>

    <div class="field">
      <label for="repo-volumes">Volumes</label>
      <textarea class="mono" id="repo-volumes" name="volumes" placeholder="./data/api:/app/data">{{joinLines .Repo.Volumes}}</textarea>
      <p class="help">One Docker volume mapping per line.</p>
    </div>

    <div class="form-actions" style="margin-top:18px; justify-content:flex-end;">
      <a class="button" href="{{.BasePath}}/repos">Cancel</a>
      <button class="button button--primary" type="submit" data-default-label="Save Configuration">Save Configuration</button>
    </div>
  </form>
</section>

<section class="panel" aria-labelledby="repo-git-heading">
  <div class="section-heading">
    <div>
      <h2 id="repo-git-heading">Git Controls</h2>
      <p>Pull this repository, or commit and push its local changes.</p>
    </div>
  </div>
  <form method="post" action="{{.BasePath}}/repos/{{.Index}}/push" data-loading-label="Pushing...">
    <div class="field">
      <label for="repo-commit-message">Commit Message</label>
      <input id="repo-commit-message" name="message" placeholder="Update {{if .Repo.Name}}{{.Repo.Name}}{{else}}repository{{end}}" autocomplete="off" required>
    </div>
    <div class="form-actions" style="margin-top:14px; justify-content:flex-end;">
      <button class="button button--primary" type="submit" data-default-label="Push">Push</button>
    </div>
  </form>
</section>

<section class="panel danger-panel" aria-labelledby="delete-heading">
  <div class="section-heading">
    <div>
      <h2 id="delete-heading">Delete Repository</h2>
      <p>Remove this entry from repos.conf. Existing files on disk are left alone.</p>
    </div>
    <form method="post" action="{{.BasePath}}/repos/{{.Index}}/delete" data-confirm="Delete this repository from the configuration?">
      <button class="button button--danger" type="submit">Delete Repository</button>
    </form>
  </div>
</section>
`

const terminalAssets = `
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/xterm/css/xterm.css">
<script src="https://cdn.jsdelivr.net/npm/xterm/lib/xterm.js"></script>
<script src="https://cdn.jsdelivr.net/npm/xterm-addon-fit/lib/xterm-addon-fit.js"></script>
<script>
(function () {
  var shell = document.querySelector('[data-shell]');
  if (!shell || !window.Terminal || !window.FitAddon || !window.WebSocket) {
    return;
  }
  var viewport = shell.querySelector('[data-terminal-viewport]');
  var status = shell.querySelector('[data-terminal-status]');
  var term = new Terminal({
    cursorBlink: true,
    convertEol: true,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
    fontSize: 13,
    scrollback: 5000,
    theme: {
      background: '#000000',
      foreground: '#f3f4f6',
      cursor: '#f3f4f6',
      selectionBackground: '#334155'
    }
  });
  var fitAddon = new FitAddon.FitAddon();
  var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  var socket = new WebSocket(proto + '//' + location.host + shell.getAttribute('data-shell-url'));
  var resizeFrame = 0;

  term.loadAddon(fitAddon);
  term.open(viewport);

  function setStatus(text) {
    status.textContent = text;
  }

  function send(message) {
    if (socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify(message));
    }
  }

  function fitAndResize() {
    cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(function () {
      fitAddon.fit();
      send({type: 'resize', cols: term.cols, rows: term.rows});
    });
  }

  socket.binaryType = 'arraybuffer';
  socket.addEventListener('open', function () {
    setStatus('Connected');
    fitAndResize();
    term.focus();
	  document.querySelectorAll('[data-terminal-command]').forEach(function (button) {
		button.addEventListener('click', function () {
		  send({type: 'input', data: button.getAttribute('data-terminal-command') + '\n'});
		  term.focus();
		});
	  });
  });
  socket.addEventListener('message', function (event) {
    if (typeof event.data === 'string') {
      try {
        var message = JSON.parse(event.data);
        if (message.type === 'ended') {
          setStatus('Session ended');
          term.write('\r\n[process ended]\r\n');
          return;
        }
        if (message.type === 'error') {
          setStatus('Error');
          term.write('\r\n' + message.data + '\r\n');
          return;
        }
      } catch (err) {
        term.write(event.data);
        return;
      }
    } else {
      term.write(new Uint8Array(event.data));
    }
  });
  socket.addEventListener('close', function () {
    setStatus('Disconnected');
  });
  socket.addEventListener('error', function () {
    setStatus('Connection error');
  });
  term.onData(function (data) {
    send({type: 'input', data: data});
  });
  window.addEventListener('resize', fitAndResize);
  viewport.addEventListener('click', function () {
    term.focus();
  });
  window.addEventListener('beforeunload', function () {
    send({type: 'close'});
  });
})();
</script>
`

const syncBody = `
<header class="page-header">
  <div>
    <h1>Synchronize repositories</h1>
    <p class="page-description">Pull updates or publish local changes across the workspace.</p>
  </div>
</header>

<section class="panel" aria-labelledby="prepare-heading">
  <div class="section-heading"><div><h2 id="prepare-heading">Prepare every application</h2><p>Create missing <code>config/.env</code> and <code>data/main.sqlite</code> files. Environment keys are populated from each app's <code>schema.json</code> or <code>env.schema</code>.</p></div></div>
  <form method="post" action="{{.BasePath}}/sync/prepare" data-loading-label="Preparing..."><button class="button button--primary" type="submit">Prepare application files</button></form>
</section>

<section class="split-actions" aria-labelledby="sync-heading">
  <div class="panel">
    <div class="section-heading">
      <div>
        <h2 id="sync-heading">Pull all repositories</h2>
        <p>Fetch and merge remote changes for every configured repository.</p>
      </div>
    </div>
    <form method="post" action="{{.BasePath}}/sync/pull" data-loading-label="Pulling...">
      <button class="button button--primary" type="submit" data-default-label="Pull all">Pull all</button>
    </form>
  </div>
  <div class="panel danger-panel">
    <div class="section-heading">
      <div>
        <h2>Push all repositories</h2>
        <p>Commit local changes with one message and push every configured repository.</p>
      </div>
    </div>
    <form method="post" action="{{.BasePath}}/sync/push" data-loading-label="Pushing...">
      <div class="field">
                <label for="sync-commit-message">General Commit Message</label>
        <input id="sync-commit-message" name="message" placeholder="Update all repositories" autocomplete="off" required>
        <p class="help">Used for every repository that has changes to commit.</p>
      </div>
      <div class="form-actions" style="margin-top:14px; justify-content:flex-end;">
        <button class="button button--danger" type="submit" data-default-label="Push all">Push all</button>
      </div>
    </form>
  </div>
</section>

<section class="panel" aria-labelledby="sync-repos-heading">
  <div class="section-heading">
    <div>
      <h2 id="sync-repos-heading">Included repositories</h2>
      <p>These repositories are controlled by Pull all and Push all.</p>
    </div>
  </div>
  {{if .Items}}
  <table class="repo-table">
    <thead>
      <tr>
        <th>Repository</th>
        <th>Local path</th>
        <th>Branch</th>
        <th>Status</th>
        <th>Last sync</th>
        <th>Actions</th>
      </tr>
    </thead>
    <tbody>
    {{range .Items}}
    {{$status := repoStatus .Root .Repo}}
    <tr>
      <td><a class="repo-row__main" href="{{.BasePath}}/repos/{{.Index}}"><span class="repo-name">{{if .Workspace}}{{.Workspace}} / {{end}}{{if .Repo.Name}}{{.Repo.Name}}{{else}}{{.Repo.URL}}{{end}}</span><span class="repo-meta mono">{{.Repo.URL}}</span></a></td>
      <td><code class="mono repo-meta">{{if .Repo.Path}}{{.Repo.Path}}{{else}}{{.Repo.URL}}{{end}}</code></td>
      <td><span class="branch-badge">branch {{branchLabel .Repo.Branch}}</span></td>
      <td><span class="status status--{{$status.Key}}"><span class="status-dot"></span>{{$status.Label}}</span></td>
      <td class="muted">{{repoLastSync .Root .Repo}}</td>
      <td><a class="button button--small" href="{{.BasePath}}/repos/{{.Index}}">Open</a></td>
    </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}
  <div class="empty-state">
    <h3>No repositories configured</h3>
    <p>Add repositories on the Repos page before running sync controls.</p>
  </div>
  {{end}}
</section>
`

const terminalBody = `
<header class="page-header">
  <div>
    <h1>Terminal</h1>
    <p class="page-description">Run commands from the root of this workspace.</p>
  </div>
</header>

<section class="panel" aria-labelledby="workspace-terminal-heading">
  <div class="section-heading">
    <div>
      <span class="eyebrow">Browser shell</span>
      <h2 id="workspace-terminal-heading">Workspace terminal</h2>
      <p class="mono">{{.Root}}</p>
    </div>
  </div>
  <div class="terminal" data-shell data-shell-url="{{.BasePath}}/terminal/shell" data-shell-root="{{.Root}}">
    <div class="terminal-toolbar">
      <code class="mono">{{.Root}}</code>
      <span class="terminal-status" data-terminal-status aria-live="polite">Connecting...</span>
    </div>
    <div class="terminal-viewport" data-terminal-viewport></div>
  </div>
</section>
`

const launchBody = `
<header class="page-header"><div><h1>Launch application</h1><p class="page-description">Start the Docker Compose stack and watch its output live.</p></div></header>
<section class="panel">
  <div class="section-heading"><div><h2>Launch terminal</h2><p>Generate files first when repository configuration has changed.</p></div><div class="inline-actions"><form method="post" action="{{.BasePath}}/generate" data-loading-label="Generating..."><button class="button" type="submit">Generate files</button></form><button class="button button--primary" type="button" data-terminal-command="{{.Command}}">Launch stack</button></div></div>
  <div class="notice">{{.Notice}}</div>
  <div class="terminal" data-shell data-shell-url="{{.BasePath}}/launch/shell" data-shell-root="{{.Root}}"><div class="terminal-toolbar"><code class="mono">{{.Root}}</code><span class="terminal-status" data-terminal-status aria-live="polite">Connecting...</span></div><div class="terminal-viewport" data-terminal-viewport></div></div>
</section>
`
