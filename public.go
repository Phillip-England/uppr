package main

import (
	"html/template"
	"net/http"
)

func (app *webApp) handleDevelopers(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/developers" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	renderPublicPage(w, publicDevelopersTemplate)
}

func renderPublicPage(w http.ResponseWriter, tmpl *template.Template) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

const publicCSS = `:root{color-scheme:dark;--bg:#090b10;--surface:#11151d;--surface2:#171c26;--text:#f4f6fb;--muted:#9da8ba;--line:#283141;--blue:#70a5ff;--green:#6be5ad}*{box-sizing:border-box}html{scroll-behavior:smooth}body{margin:0;background:radial-gradient(circle at 80% 0,rgba(49,96,180,.22),transparent 34rem),var(--bg);color:var(--text);font:16px/1.65 Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:inherit}.wrap{width:min(1120px,calc(100% - 40px));margin:auto}nav{height:76px;display:flex;align-items:center;justify-content:space-between}.brand{display:flex;align-items:center;gap:12px;text-decoration:none;font-size:20px;font-weight:800}.mark{width:34px;height:34px;border-radius:9px;display:grid;place-items:center;background:linear-gradient(135deg,var(--blue),var(--green));color:#08111c;font-weight:900}.links{display:flex;gap:28px}.links a{text-decoration:none;color:var(--muted);font-size:14px;font-weight:650}.links a.active,.links a:hover{color:var(--text)}.hero{padding:105px 0 95px;max-width:850px}.eyebrow{color:var(--green);font:700 13px/1.2 ui-monospace,SFMono-Regular,Menlo,monospace;text-transform:uppercase;letter-spacing:.13em}h1{font-size:clamp(48px,8vw,86px);line-height:.98;letter-spacing:-.06em;margin:22px 0 28px}h2{font-size:clamp(30px,5vw,48px);line-height:1.08;letter-spacing:-.04em;margin:0 0 18px}h3{font-size:18px;margin:0 0 9px}.lede{font-size:clamp(18px,2.4vw,23px);line-height:1.55;color:var(--muted);max-width:720px}.actions{display:flex;gap:12px;flex-wrap:wrap;margin-top:36px}.button{display:inline-flex;padding:12px 18px;border-radius:9px;text-decoration:none;font-weight:750;font-size:14px;border:1px solid var(--line);background:var(--surface)}.button.primary{background:var(--text);color:#0a0d12}.terminal{margin-top:48px;border:1px solid var(--line);background:rgba(17,21,29,.88);border-radius:14px;overflow:hidden;box-shadow:0 28px 80px rgba(0,0,0,.32)}.terminal-head{padding:12px 16px;border-bottom:1px solid var(--line);color:var(--muted);font:12px ui-monospace,monospace}.terminal pre{margin:0;padding:24px;overflow:auto;color:#dce5f5;font:14px/1.75 ui-monospace,monospace}.prompt{color:var(--green)}section{padding:84px 0;border-top:1px solid var(--line)}.intro{max-width:650px;color:var(--muted);font-size:18px;margin-bottom:38px}.grid{display:grid;grid-template-columns:repeat(3,1fr);gap:16px}.card{padding:25px;border:1px solid var(--line);background:linear-gradient(145deg,var(--surface),rgba(17,21,29,.55));border-radius:12px}.card p{color:var(--muted);margin:0}.num{color:var(--blue);font:700 12px ui-monospace,monospace;margin-bottom:28px}.flow{display:grid;grid-template-columns:repeat(4,1fr);gap:1px;background:var(--line);border:1px solid var(--line);border-radius:12px;overflow:hidden}.flow div{background:var(--surface);padding:25px}.flow strong{display:block}.flow span{color:var(--muted);font-size:14px}.docs{max-width:820px;padding:70px 0 100px}.docs h1{font-size:clamp(44px,7vw,72px)}.docs h2{font-size:30px;margin-top:70px}.docs p,.docs li{color:var(--muted)}.docs code{font:14px ui-monospace,monospace;color:#dbe7ff;background:var(--surface2);padding:2px 6px;border-radius:5px}.docs pre{border:1px solid var(--line);background:var(--surface);border-radius:11px;padding:20px;overflow:auto;color:#dce5f5;font:14px/1.7 ui-monospace,monospace}.docs pre code{padding:0;background:none}.callout{border-left:3px solid var(--green);padding:15px 19px;background:rgba(107,229,173,.06);color:var(--muted)}footer{border-top:1px solid var(--line);padding:30px 0 45px;color:var(--muted);font-size:13px}@media(max-width:760px){.grid,.flow{grid-template-columns:1fr}.links{gap:16px}.hero{padding-top:70px}section{padding:62px 0}.wrap{width:min(100% - 28px,1120px)}}`

func publicChrome(title, active, body string) string {
	overviewClass, developerClass := "", ""
	if active == "overview" {
		overviewClass = ` class="active"`
	}
	if active == "developers" {
		developerClass = ` class="active"`
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="description" content="Uppr deploys and operates application repositories with one consistent contract."><title>` + title + `</title><style>` + publicCSS + `</style></head><body><div class="wrap"><nav><a class="brand" href="/"><span class="mark">U</span>Uppr</a><div class="links"><a href="/"` + overviewClass + `>Overview</a><a href="/developers"` + developerClass + `>Developers</a></div></nav></div>` + body + `<footer><div class="wrap">Uppr — a predictable path from repository to running application.</div></footer></body></html>`
}

const publicHomeBody = `<main><div class="wrap"><div class="hero"><div class="eyebrow">Repository → running application</div><h1>Give every app the same operational shape.</h1><p class="lede">Uppr organizes repositories into workspaces, prepares their runtime configuration, and generates the Docker and Caddy layer that launches them behind real domains.</p><div class="actions"><a class="button primary" href="/developers">Developer quick start</a><a class="button" href="#how">See how it works</a></div><div class="terminal"><div class="terminal-head">workspace / launch</div><pre><span class="prompt">$</span> uppr pull
[api] git pull
<span class="prompt">$</span> uppr launch
configured port 3000 from Dockerfile
applications ready · proxy reloaded</pre></div></div></div></div><section><div class="wrap"><div class="eyebrow">One control plane</div><h2>Operations without repository guesswork.</h2><p class="intro">Uppr understands a small, explicit application contract and turns it into repeatable deployment infrastructure.</p><div class="grid"><article class="card"><div class="num">01 / SYNC</div><h3>Workspace orchestration</h3><p>Clone, pull, inspect, and push groups of Git repositories from one focused interface.</p></article><article class="card"><div class="num">02 / PREPARE</div><h3>Predictable configuration</h3><p>Populate environment keys from a committed schema while keeping values and persistent data outside Git.</p></article><article class="card"><div class="num">03 / LAUNCH</div><h3>Generated runtime</h3><p>Build Compose services and Caddy routes from repository metadata, Dockerfiles, domains, and policy.</p></article></div></div></section><section id="how"><div class="wrap"><div class="eyebrow">The operating model</div><h2>From source to service in four moves.</h2><div class="flow"><div><strong>Register</strong><span>Add Git repositories to an isolated workspace.</span></div><div><strong>Inspect</strong><span>Read each schema and Dockerfile contract.</span></div><div><strong>Prepare</strong><span>Create private config and durable data paths.</span></div><div><strong>Launch</strong><span>Build apps, route domains, and reload safely.</span></div></div></div></section></main>`

const publicDevelopersBody = `<main><div class="wrap docs"><div class="eyebrow">Developer quick start</div><h1>Make an application Uppr-ready.</h1><p class="lede">Uppr works best when every repository exposes the same small set of operational signals. The CLI can write the complete AI implementation brief directly into any project.</p><h2>1. Generate the integration brief</h2><pre><code>cd path/to/your-application
uppr dump</code></pre><p>This creates <code>UPPR.md</code> in the current directory. Give that file to your coding agent or use it as the project checklist. To target another directory, run <code>uppr dump ./path</code>.</p><h2>2. Adopt the project contract</h2><pre><code>your-app/
├── Dockerfile
├── schema.json
├── config/
│   └── .env
└── data/
    └── main.sqlite</code></pre><ul><li><code>config/.env</code> contains runtime values and stays out of Git.</li><li><code>schema.json</code> declares environment variable names, descriptions, examples, and whether they are required.</li><li><code>data/main.sqlite</code> is the conventional primary database; other persistent files also belong under <code>data/</code>.</li><li>The root <code>Dockerfile</code> must contain a numeric <code>EXPOSE</code> instruction and listen on <code>0.0.0.0</code>.</li></ul><div class="callout">Inside containers, Uppr mounts these directories at <code>/app/config</code> and <code>/app/data</code>. Make application paths work in both local and container execution.</div><h2>3. Describe the environment</h2><pre><code>{
  "variables": [{
    "name": "APP_ENV",
    "description": "Runtime mode used by the application.",
    "example": "production",
    "required": true
  }]
}</code></pre><p>Uppr adds missing schema keys to <code>config/.env</code> with blank values and preserves existing values.</p><h2>4. Validate the container boundary</h2><pre><code>docker build -t your-app .
docker run --rm -p 3000:3000 \
  -v "$PWD/config:/app/config" \
  -v "$PWD/data:/app/data" your-app</code></pre><p>Replace <code>3000</code> with the first numeric port in the Dockerfile’s <code>EXPOSE</code> instruction. Confirm durable writes appear under the host <code>data/</code> directory.</p><h2>5. Hand it to Uppr</h2><p>Add the repository to a workspace, pull it, prepare application files, and launch. Uppr derives the port, protects the config and data mounts, and generates the deployment layer.</p></div></main>`

var publicHomeTemplate = template.Must(template.New("public-home").Parse(publicChrome("Uppr — application operations, made predictable", "overview", publicHomeBody)))
var publicDevelopersTemplate = template.Must(template.New("public-developers").Parse(publicChrome("Developer quick start — Uppr", "developers", publicDevelopersBody)))
