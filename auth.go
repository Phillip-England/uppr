package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultAddr       = ":9944"
	legacyDefaultAddr = ":8787"
	defaultDBPath     = "data/main.sqlite"
	sessionCookieName = "uppr_session"
	sessionTTL        = 12 * time.Hour
	failWindow        = 24 * time.Hour
	maxFailures       = 5
)

type serverConfig struct {
	AdminUsername string
	AdminPassword string
	SessionSecret string
	Addr          string
	DBPath        string
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

type loginPage struct {
	Error string
}

func defaultEnvContents() []byte {
	secret, err := randomToken(32)
	if err != nil {
		secret = "change-me-session-secret"
	}
	return []byte(strings.Join([]string{
		"GITHUB_USERNAME=",
		"GITHUB_PASSWORD=",
		"ADMIN_USERNAME=",
		"ADMIN_PASSWORD=",
		"SESSION_SECRET=" + secret,
		"ADDR=:9944",
		"UPPR_DOMAINS=uppr.localhost",
		"UPPR_DOCKER_IMAGE=uppr:local",
		"UPPR_DOCKER_NETWORK=uppr-network",
		"UPPR_RATE_LIMIT_ENABLED=true",
		"UPPR_RATE_LIMIT_ZONE=uppr",
		"UPPR_RATE_LIMIT_EVENTS=100",
		"UPPR_RATE_LIMIT_WINDOW=1m",
		"",
	}, "\n"))
}

func ensureEnvDefaults(path string) error {
	values, err := readDotEnv(path)
	if err != nil {
		return err
	}
	defaults := map[string]string{}
	ordered := []string{}
	if _, ok := values["GITHUB_USERNAME"]; !ok {
		defaults["GITHUB_USERNAME"] = ""
		ordered = append(ordered, "GITHUB_USERNAME")
	}
	if _, ok := values["GITHUB_PASSWORD"]; !ok {
		defaults["GITHUB_PASSWORD"] = ""
		ordered = append(ordered, "GITHUB_PASSWORD")
	}
	if _, ok := values["ADMIN_USERNAME"]; !ok {
		defaults["ADMIN_USERNAME"] = ""
		ordered = append(ordered, "ADMIN_USERNAME")
	}
	if _, ok := values["ADMIN_PASSWORD"]; !ok {
		defaults["ADMIN_PASSWORD"] = ""
		ordered = append(ordered, "ADMIN_PASSWORD")
	}
	if _, ok := values["SESSION_SECRET"]; !ok {
		secret, err := randomToken(32)
		if err != nil {
			return err
		}
		defaults["SESSION_SECRET"] = secret
		ordered = append(ordered, "SESSION_SECRET")
	}
	if _, ok := values["ADDR"]; !ok {
		defaults["ADDR"] = defaultAddr
		ordered = append(ordered, "ADDR")
	} else if normalizeListenAddr(values["ADDR"]) == legacyDefaultAddr {
		defaults["ADDR"] = defaultAddr
		ordered = append(ordered, "ADDR")
	}
	for _, entry := range []struct{ key, value string }{
		{"UPPR_DOMAINS", "uppr.localhost"},
		{"UPPR_DOCKER_IMAGE", "uppr:local"},
		{"UPPR_DOCKER_NETWORK", "uppr-network"},
		{"UPPR_RATE_LIMIT_ENABLED", "true"},
		{"UPPR_RATE_LIMIT_ZONE", "uppr"},
		{"UPPR_RATE_LIMIT_EVENTS", "100"},
		{"UPPR_RATE_LIMIT_WINDOW", "1m"},
	} {
		if _, ok := values[entry.key]; !ok {
			defaults[entry.key] = entry.value
			ordered = append(ordered, entry.key)
		}
	}
	if len(ordered) == 0 {
		return nil
	}
	return writeDotEnvValues(path, ordered, defaults)
}

func loadServerConfig(envPath string) (serverConfig, error) {
	values, err := readDotEnv(envPath)
	if err != nil {
		return serverConfig{}, err
	}
	cfg := serverConfig{
		AdminUsername: values["ADMIN_USERNAME"],
		AdminPassword: values["ADMIN_PASSWORD"],
		SessionSecret: values["SESSION_SECRET"],
		Addr:          values["ADDR"],
		DBPath:        filepath.Join(filepath.Dir(filepath.Dir(envPath)), defaultDBPath),
	}
	if cfg.Addr == "" {
		if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
			cfg.Addr = ":" + strings.TrimPrefix(port, ":")
		} else {
			cfg.Addr = defaultAddr
		}
	}
	cfg.Addr = normalizeListenAddr(cfg.Addr)
	for key, value := range map[string]string{
		"ADMIN_USERNAME": cfg.AdminUsername,
		"ADMIN_PASSWORD": cfg.AdminPassword,
		"SESSION_SECRET": cfg.SessionSecret,
	} {
		if strings.TrimSpace(value) == "" {
			return serverConfig{}, fmt.Errorf("%s is required in %s", key, envPath)
		}
	}
	return cfg, nil
}

func normalizeListenAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return defaultAddr
	}
	if strings.HasPrefix(addr, ":") || strings.Contains(addr, ":") {
		return addr
	}
	return ":" + addr
}

func initAuthDB(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS login_failures (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ip TEXT NOT NULL,
  attempted_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_login_failures_ip_time
ON login_failures (ip, attempted_at);
`)
	return err
}

func ensureAuthDBFile(root string) error {
	path := filepath.Join(root, defaultDBPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := initAuthDB(db); err != nil {
		return err
	}
	// SQLite creates files using the process umask. Keep login/IP data private
	// even when Uppr was started from a shell with a permissive umask.
	return os.Chmod(path, 0o600)
}

func (app *webApp) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !app.authRequired {
		http.Redirect(w, r, app.postLoginPath(), http.StatusSeeOther)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if app.isAuthed(r) {
			http.Redirect(w, r, app.postLoginPath(), http.StatusSeeOther)
			return
		}
		renderLogin(w, loginPage{})
	case http.MethodPost:
		ip := clientIP(r)
		blocked, err := app.isBlocked(ip)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if blocked {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		username := r.FormValue("username")
		password := r.FormValue("password")
		if subtleEqual(username, app.cfg.AdminUsername) && subtleEqual(password, app.cfg.AdminPassword) {
			if err := app.createSession(w); err != nil {
				http.Error(w, "could not create session", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, app.postLoginPath(), http.StatusSeeOther)
			return
		}
		blocked, err = app.recordFailure(ip)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if blocked {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		renderLogin(w, loginPage{Error: "Invalid username or password."})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (app *webApp) postLoginPath() string {
	if app.isServerMode() && app.basePath == "" {
		return "/workspaces"
	}
	return app.routePath("/repos")
}

func (app *webApp) handleLogout(w http.ResponseWriter, r *http.Request) {
	if app.authRequired {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if id, ok := app.verifyCookie(cookie.Value); ok {
				app.sessions.delete(id)
			}
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *webApp) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !app.authRequired || r.URL.Path == "/" || r.URL.Path == "/developers" || r.URL.Path == "/login" || r.URL.Path == "/logo.png" {
			next.ServeHTTP(w, r)
			return
		}
		if !app.isAuthed(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (app *webApp) isAuthed(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	id, ok := app.verifyCookie(cookie.Value)
	return ok && app.sessions.valid(id)
}

func (app *webApp) createSession(w http.ResponseWriter) error {
	id, err := randomToken(32)
	if err != nil {
		return err
	}
	app.sessions.put(id, time.Now().Add(sessionTTL))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    app.signCookie(id),
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
	return nil
}

func (app *webApp) signCookie(id string) string {
	mac := hmac.New(sha256.New, []byte(app.cfg.SessionSecret))
	mac.Write([]byte(id))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return id + "." + sig
}

func (app *webApp) verifyCookie(value string) (string, bool) {
	id, sig, ok := strings.Cut(value, ".")
	if !ok || id == "" || sig == "" {
		return "", false
	}
	return id, hmac.Equal([]byte(app.signCookie(id)), []byte(value))
}

func (app *webApp) isBlocked(ip string) (bool, error) {
	if err := app.purgeFailures(); err != nil {
		return false, err
	}
	count, err := app.failureCount(ip)
	return count >= maxFailures, err
}

func (app *webApp) recordFailure(ip string) (bool, error) {
	if err := app.purgeFailures(); err != nil {
		return false, err
	}
	if _, err := app.db.Exec(`INSERT INTO login_failures (ip, attempted_at) VALUES (?, ?)`, ip, time.Now().Unix()); err != nil {
		return false, err
	}
	count, err := app.failureCount(ip)
	return count >= maxFailures, err
}

func (app *webApp) purgeFailures() error {
	_, err := app.db.Exec(`DELETE FROM login_failures WHERE attempted_at < ?`, time.Now().Add(-failWindow).Unix())
	return err
}

func (app *webApp) failureCount(ip string) (int, error) {
	var count int
	err := app.db.QueryRow(`SELECT COUNT(*) FROM login_failures WHERE ip = ? AND attempted_at >= ?`, ip, time.Now().Add(-failWindow).Unix()).Scan(&count)
	return count, err
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: map[string]time.Time{}}
}

func (s *sessionStore) put(id string, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = expires
}

func (s *sessionStore) valid(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.sessions[id]
	if !ok || time.Now().After(expires) {
		delete(s.sessions, id)
		return false
	}
	return true
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func randomToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func subtleEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func renderLogin(w http.ResponseWriter, page loginPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := loginTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="icon" type="image/png" href="/logo.png?v=2">
<link rel="apple-touch-icon" href="/logo.png?v=2">
<title>uppr login</title>
<style>
:root { color-scheme: light dark; --bg:#f4f1ea; --surface:#fbfaf7; --text:#1f252d; --muted:#657080; --border:#d6d0c5; --accent:#2563eb; --danger:#c94a55; --danger-soft:#fbeaec; }
@media (prefers-color-scheme: dark) { :root { --bg:#0b0d10; --surface:#14181f; --text:#f3f6fa; --muted:#aab3c2; --border:#2a313d; --accent:#5b9cff; --danger:#ef6a73; --danger-soft:rgba(239,106,115,.12); } }
* { box-sizing:border-box; }
body { margin:0; min-height:100vh; display:grid; place-items:center; padding:24px; font:14px/1.45 Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color:var(--text); background:var(--bg); }
main { width:min(100%, 380px); }
.brand { display:flex; gap:11px; align-items:center; margin-bottom:18px; }
.brand-mark { width:42px; height:42px; border:1px solid var(--border); border-radius:11px; display:grid; place-items:center; background:linear-gradient(145deg,var(--surface),var(--bg)); box-shadow:0 8px 24px rgba(37,99,235,.12); }
.brand-mark img { width:34px; height:34px; display:block; object-fit:contain; }
.brand-name { font-size:18px; font-weight:760; }
.subtitle { color:var(--muted); font-size:12px; margin:0; }
form { display:grid; gap:14px; background:var(--surface); border:1px solid var(--border); border-radius:10px; padding:20px; box-shadow:0 16px 40px rgba(24,29,37,.08); }
h1 { margin:0; font-size:22px; line-height:1.2; letter-spacing:0; }
label { display:grid; gap:6px; font-size:13px; font-weight:650; }
input { width:100%; min-height:40px; border:1px solid var(--border); border-radius:8px; padding:8px 10px; background:transparent; color:var(--text); font:inherit; }
input:focus { outline:0; border-color:var(--accent); box-shadow:0 0 0 3px rgba(37,99,235,.18); }
button { min-height:40px; border:1px solid var(--accent); border-radius:8px; background:var(--accent); color:#fff; font:inherit; font-weight:700; cursor:pointer; }
.error { margin:0; border:1px solid rgba(239,106,115,.38); background:var(--danger-soft); color:var(--text); border-radius:8px; padding:10px 12px; font-weight:650; }
</style>
</head>
<body>
<main>
  <div class="brand"><div class="brand-mark"><img src="/logo.png?v=2" alt=""></div><div class="brand-name">Uppr</div></div>
  <form method="post" action="/login">
    <h1>Log in</h1>
    {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
    <label>Username <input name="username" autocomplete="username" required autofocus></label>
    <label>Password <input name="password" type="password" autocomplete="current-password" required></label>
    <button type="submit">Log in</button>
  </form>
</main>
</body>
</html>`))
