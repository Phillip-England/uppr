import fs from "node:fs";
import * as tar from "tar";
import path from "node:path";
import os from "node:os";
import net from "node:net";
import crypto from "node:crypto";
import { spawnSync } from "node:child_process";
import { gitToken, removeGitToken, saveGitToken } from "./auth.ts";
import { caddyBinary, dependencies } from "./deps.ts";
import {
  appStatus,
  appSetup,
  appRouting,
  active,
  containerName,
  containerPort,
  defaultRateLimit,
  envName,
  exists,
  fail,
  findPad,
  freePort,
  generate,
  globalCaddy,
  imageName,
  initPad,
  installedPads,
  linux,
  loadManifest,
  prepareRepo,
  privileged,
  probe,
  readSchema,
  readEnv,
  registryDir,
  renderCaddyUnit,
  repoDir,
  renderCaddy,
  run,
  safeName,
  saveManifest,
  saveSchema,
  saveRepoEnv,
  schemaPath,
  select,
  serviceState,
  sorted,
  starterSchema,
  unitDir,
  unitName,
  validDomain,
  validPort,
  which,
  type Manifest,
  type Repo,
  type Schema,
  type Variable,
} from "./core.ts";

function option(args: string[], name: string) {
  const i = args.indexOf(name);
  return i < 0 ? undefined : args[i + 1];
}
function only(args: string[], n: number, usage: string) {
  if (args.length !== n) fail(`usage: ${usage}`);
}
function ttyLine(prompt: string, hidden = false) {
  process.stderr.write(prompt);
  if (hidden) spawnSync("stty", ["-echo"], { stdio: "inherit" });
  let line = "";
  const b = Buffer.alloc(1);
  try {
    while (fs.readSync(0, b, 0, 1, null)) {
      if (b[0] === 10) break;
      line += b.toString();
    }
  } finally {
    if (hidden) {
      spawnSync("stty", ["echo"], { stdio: "inherit" });
      process.stderr.write("\n");
    }
  }
  return line.trim();
}
function confirm(s: string) {
  if (!process.stdin.isTTY) fail("interactive confirmation required");
  return /^y(es)?$/i.test(ttyLine(`${s} [y/N] `));
}
function requireInstalled(m: Manifest, repos: Repo[]) {
  for (const r of repos) {
    const state = serviceState(unitName(m, r));
    if (
      state === "not installed" ||
      state === "unavailable" ||
      state === "bad-setting"
    )
      fail(`service ${unitName(m, r)} is ${state}; run uppr install`);
  }
}
function dockerAccess() {
  if (!which("docker")) fail("docker is missing; run uppr deps install");
  const p = probe("docker", ["info", "--format", "{{.ServerVersion}}"]);
  if (!p.ok) {
    const message = p.err || p.out;
    if (
      message.toLowerCase().includes("permission denied") &&
      message.includes("docker.sock")
    )
      fail(
        `Docker socket access denied for ${os.userInfo().username}; run sudo usermod -aG docker ${os.userInfo().username}, then sign out and back in`,
      );
    fail(`Docker daemon unavailable: ${message}`);
  }
}
function buildx() {
  if (process.env.DOCKER_BUILDKIT === "0") return;
  if (probe("docker", ["buildx", "version"]).ok) return;
  let pkg = "";
  if (which("dpkg-query")) {
    if (
      probe("dpkg-query", ["-W", "-f=${Status}", "docker-ce-cli"]).out ===
      "install ok installed"
    )
      pkg = "docker-buildx-plugin";
    else if (
      probe("dpkg-query", ["-W", "-f=${Status}", "docker.io"]).out ===
      "install ok installed"
    )
      pkg = "docker-buildx";
  }
  if (
    pkg &&
    process.stdin.isTTY &&
    confirm(`Buildx is unavailable. Install ${pkg} now?`)
  ) {
    privileged("apt-get", ["install", "-y", pkg]);
    if (!probe("docker", ["buildx", "version"]).ok)
      fail("Buildx remains unavailable");
  } else
    console.error(
      `Buildx is unavailable; ${pkg ? `run sudo apt-get install ${pkg}` : "install a matching Buildx plugin"}. Continuing with the legacy builder.`,
    );
}
function imageId(m: Manifest, r: Repo) {
  return probe("docker", [
    "image",
    "inspect",
    "--format",
    "{{.Id}}",
    imageName(m, r),
  ]).out;
}
function buildApp(root: string, m: Manifest, r: Repo) {
  prepareRepo(root, r);
  run("docker", ["build", "-t", imageName(m, r), repoDir(root, r)]);
}
async function ready(m: Manifest, r: Repo) {
  let observed = false;
  console.log(`Waiting for ${r.name} to become ready...`);
  for (let n = 0; n < 60; n++) {
    const p = probe("docker", [
      "inspect",
      "--format",
      "{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}",
      containerName(m, r),
    ]);
    if (p.ok) {
      const [state, health] = p.out.split(/\s+/);
      if (
        state === "running" &&
        (health === "healthy" || (!health && observed))
      ) {
        console.log("ready", r.name);
        return;
      }
      if (state === "running" && !health) observed = true;
      if (state === "exited" || state === "dead" || health === "unhealthy")
        fail(`${r.name} failed to become ready; run uppr logs ${r.name}`);
    }
    if (n > 0 && n % 5 === 0)
      console.log(`Still waiting for ${r.name} (${n * 2}s)...`);
    await Bun.sleep(2000);
  }
  fail(
    `${r.name} did not become ready within 120 seconds; run uppr logs ${r.name}`,
  );
}
function readRegistry() {
  return installedPads();
}
function rateLimitCaddy() {
  const binary = caddyBinary();
  if (!binary) fail("Caddy is missing; run uppr deps install");
  if (!probe(binary, ["list-modules"]).out.split("\n").includes("http.handlers.rate_limit"))
    fail("Caddy lacks the rate_limit module; run uppr deps caddy");
  return binary;
}
function buildRateLimitCaddy() {
  linux();
  if (!which("go") && !which("xcaddy"))
    fail("Go or xcaddy is required; install Go, then run uppr deps caddy");
  const stage = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-caddy-build-"));
  const binary = path.join(stage, "uppr-caddy");
  try {
    console.log("Building Caddy with the rate_limit module...");
    const args = ["build", "--output", binary, "--with", "github.com/mholt/caddy-ratelimit"];
    if (which("xcaddy")) run("xcaddy", args, { cwd: stage });
    else run("go", ["run", "github.com/caddyserver/xcaddy/cmd/xcaddy@latest", ...args], { cwd: stage });
    if (!probe(binary, ["list-modules"]).out.split("\n").includes("http.handlers.rate_limit"))
      fail("custom Caddy build lacks the rate_limit module");
    privileged("mkdir", ["-p", "/usr/local/bin"]);
    privileged("install", ["-m", "0755", binary, "/usr/local/bin/uppr-caddy.new"]);
    privileged("mv", ["-f", "/usr/local/bin/uppr-caddy.new", "/usr/local/bin/uppr-caddy"]);
    console.log("installed /usr/local/bin/uppr-caddy");
  } finally {
    fs.rmSync(stage, { recursive: true, force: true });
  }
}
function validateCaddy(body: string) {
  const caddy = rateLimitCaddy();
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-caddy-"));
  const p = path.join(dir, "Caddyfile");
  try {
    fs.writeFileSync(p, body);
    run(caddy, ["validate", "--config", p, "--adapter", "caddyfile"], {
      quiet: true,
    });
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}
function verifyUnit(p: string) {
  run("systemd-analyze", ["verify", p], { quiet: true });
}
async function installHost(root: string, m: Manifest) {
  linux();
  console.log("Checking host and preparing app configuration...");
  if (active("caddy.service") && !active("uppr-caddy.service"))
    fail(
      "caddy.service is active; stop it before installing uppr-caddy.service",
    );
  for (const r of m.repos) prepareRepo(root, r);
  generate(root, m);
  const caddy = rateLimitCaddy();
  if (!which("docker")) fail("docker is required; run uppr deps install");
  const pads = readRegistry();
  pads[m.id] = { id: m.id, root };
  const body = globalCaddy(pads);
  console.log("Validating Caddy routes and app services...");
  validateCaddy(body);
  for (const r of m.repos) {
    console.log(`Checking ${r.name}...`);
    verifyUnit(path.join(root, "systemd", unitName(m, r)));
    if (!active(unitName(m, r))) await checkFreePort(r);
  }
  const stage = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-install-"));
  try {
    const caddyFile = path.join(stage, "Caddyfile"),
      reg = path.join(stage, `${m.id}.json`),
      caddyUnit = path.join(stage, "uppr-caddy.service");
    fs.writeFileSync(caddyFile, body);
    fs.writeFileSync(reg, JSON.stringify(pads[m.id], null, 2) + "\n");
    const caddyHash = crypto.createHash("sha256").update(fs.readFileSync(caddy)).digest("hex");
    fs.writeFileSync(caddyUnit, renderCaddyUnit(caddy) + `# binary sha256 ${caddyHash}\n`);
    verifyUnit(caddyUnit);
    const installedCaddyUnit = path.join(unitDir, "uppr-caddy.service");
    const caddyUnitChanged = !exists(installedCaddyUnit) ||
      fs.readFileSync(installedCaddyUnit, "utf8") !== fs.readFileSync(caddyUnit, "utf8");
    console.log("Installing systemd units and Caddy routes...");
    privileged("mkdir", ["-p", registryDir, "/var/lib/uppr/caddy"]);
    const old = exists(unitDir)
      ? fs
          .readdirSync(unitDir)
          .filter(
            (x) => x.startsWith(`uppr-${m.id}-`) && x.endsWith(".service"),
          )
      : [];
    const keep = new Set(m.repos.map((r) => unitName(m, r)));
    for (const name of old) {
      if (!keep.has(name)) {
        privileged("systemctl", ["disable", "--now", name]);
        privileged("rm", ["-f", path.join(unitDir, name)]);
      }
    }
    const changed: string[] = [];
    for (const r of m.repos) {
      const name = unitName(m, r),
        src = path.join(root, "systemd", name),
        dst = path.join(unitDir, name);
      if (
        active(name) &&
        ((exists(dst) &&
          fs.readFileSync(dst, "utf8") !== fs.readFileSync(src, "utf8")) ||
          !probe("docker", [
            "port",
            containerName(m, r),
            `${containerPort(root, r)}/tcp`,
          ]).out.includes(`127.0.0.1:${r.host_port}`))
      )
        changed.push(name);
      privileged("install", ["-m", "0644", src, dst]);
    }
    privileged("install", [
      "-m",
      "0644",
      reg,
      path.join(registryDir, `${m.id}.json`),
    ]);
    privileged("install", ["-m", "0644", caddyFile, "/etc/uppr/Caddyfile"]);
    privileged("install", [
      "-m",
      "0644",
      caddyUnit,
      installedCaddyUnit,
    ]);
    console.log("Reloading systemd and enabling services...");
    privileged("systemctl", ["daemon-reload"]);
    requireInstalled(m, m.repos);
    for (const r of m.repos)
      privileged("systemctl", ["enable", unitName(m, r)]);
    for (const name of changed) privileged("systemctl", ["restart", name]);
    privileged("systemctl", ["enable", "uppr-caddy.service"]);
    console.log("Starting or reloading Caddy...");
    privileged("systemctl", [caddyUnitChanged ? "restart" : "reload-or-restart", "uppr-caddy.service"]);
    console.log(
      `installed ${m.repos.length} app units and refreshed shared Caddy routes`,
    );
  } finally {
    fs.rmSync(stage, { recursive: true, force: true });
  }
}
function checkFreePort(r: Repo) {
  return new Promise<void>((resolve, reject) => {
    const server = net.createServer();
    server.once("error", (error) =>
      reject(
        new Error(
          `host port ${r.host_port} for ${r.name} is in use: ${error.message}`,
        ),
      ),
    );
    server.listen(r.host_port, "127.0.0.1", () =>
      server.close(() => resolve()),
    );
  });
}
function uninstallHost(m: Manifest) {
  linux();
  const pads = readRegistry();
  if (!pads[m.id]) fail(`launchpad ${m.id} is not installed`);
  delete pads[m.id];
  const body = globalCaddy(pads);
  if (Object.keys(pads).length) validateCaddy(body);
  const stage = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-uninstall-"));
  try {
    const file = path.join(stage, "Caddyfile");
    fs.writeFileSync(file, body);
    for (const e of fs
      .readdirSync(unitDir)
      .filter((x) => x.startsWith(`uppr-${m.id}-`) && x.endsWith(".service"))) {
      privileged("systemctl", ["disable", "--now", e]);
      privileged("rm", ["-f", path.join(unitDir, e)]);
    }
    privileged("rm", ["-f", path.join(registryDir, `${m.id}.json`)]);
    privileged("install", ["-m", "0644", file, "/etc/uppr/Caddyfile"]);
    privileged("systemctl", ["daemon-reload"]);
    if (Object.keys(pads).length)
      privileged("systemctl", ["reload-or-restart", "uppr-caddy.service"]);
    else privileged("systemctl", ["disable", "--now", "uppr-caddy.service"]);
    console.log("uninstalled launchpad", m.id);
  } finally {
    fs.rmSync(stage, { recursive: true, force: true });
  }
}
function shellQuote(value: string) {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}
function git(root: string, args: string[]) {
  const token = gitToken();
  let env = process.env,
    dir = "";
  try {
    if (token) {
      dir = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-askpass-"));
      const helper = path.join(dir, "askpass.sh");
      fs.writeFileSync(
        helper,
        `#!/bin/sh\nexec ${shellQuote(process.execPath)} ${shellQuote(path.resolve(process.argv[1]))} askpass "$@"\n`,
        { mode: 0o700 },
      );
      env = {
        ...process.env,
        GIT_ASKPASS: helper,
        GIT_TERMINAL_PROMPT: "0",
        UPPR_GIT_TOKEN: token,
      };
    }
    run("git", args, { cwd: root, env });
  } finally {
    if (dir) fs.rmSync(dir, { recursive: true, force: true });
  }
}
function pull(root: string, r: Repo) {
  const p = repoDir(root, r);
  if (!exists(path.join(p, ".git"))) {
    if (exists(p)) fail(`${p} exists without .git`);
    git(root, [
      "clone",
      ...(r.branch ? ["--branch", r.branch] : []),
      "--",
      r.url,
      p,
    ]);
  } else git(p, ["pull", "--ff-only"]);
  protectRepo(p);
  fs.mkdirSync(path.join(p, "data"), { recursive: true, mode: 0o700 });
}
function protectRepo(p: string) {
  const f = path.join(p, ".gitignore"),
    block = "# uppr: local configuration and data\n/config/\n/data/\n";
  let old = exists(f) ? fs.readFileSync(f, "utf8") : "";
  if (!old.includes(block)) {
    if (old && !old.endsWith("\n")) old += "\n";
    fs.writeFileSync(f, old + block);
  }
}
function push(root: string, r: Repo, message: string, confirmed = false) {
  const p = repoDir(root, r);
  if (!exists(path.join(p, ".git"))) fail(`${r.name} is not cloned`);
  protectRepo(p);
  const tracked = probe("git", ["ls-files", "--", "config", "data"], p);
  if (!tracked.ok) fail(tracked.err);
  if (tracked.out) fail(`protected files tracked by Git: ${tracked.out}`);
  const status = probe("git", ["status", "--short"], p);
  console.log(
    `[${r.name}] changes to commit and push:\n${status.out || "(no local changes)"}`,
  );
  if (!confirmed && !confirm("Continue?")) fail("push canceled");
  git(p, ["add", "-A"]);
  if (probe("git", ["diff", "--cached", "--name-only"], p).out)
    git(p, ["commit", "-m", message]);
  git(p, ["push"]);
}
export function schemaCommand(args: string[]) {
  const [root, m] = findPad();
  const sub = args[0],
    name = args[1];
  if (!sub || !name)
    fail("usage: uppr schema generate|edit|show|add|remove <name> [options]");
  const r = select(m, name)[0],
    p = schemaPath(root, r);
  if (sub === "generate") {
    if (exists(p) && !args.includes("--force"))
      fail(`${p} already exists; use schema edit or --force`);
    const schema = starterSchema(root, r, option(args, "--from-env"));
    saveSchema(p, schema);
    console.log(`generated ${p} with ${schema.variables.length} variables`);
    return;
  }
  if (sub === "show") {
    console.log(fs.readFileSync(p, "utf8"));
    return;
  }
  if (sub === "edit") {
    const tmp = path.join(repoDir(root, r), `.schema-${process.pid}.json`);
    if (exists(p)) fs.copyFileSync(p, tmp);
    else fs.writeFileSync(tmp, JSON.stringify(starterSchema(root, r), null, 2) + "\n");
    try {
      const editor = process.env.VISUAL || process.env.EDITOR || "vi";
      run(editor.split(/\s+/)[0], [...editor.split(/\s+/).slice(1), tmp]);
      saveSchema(p, readSchema(tmp));
      console.log(
        `updated ${p}; run uppr prepare ${r.name} and uppr restart ${r.name} to apply new required values`,
      );
    } finally {
      fs.rmSync(tmp, { force: true });
    }
    return;
  }
  if (sub === "add") {
    const variable = args[2];
    if (!variable || !envName.test(variable))
      fail(
        "usage: uppr schema add <app> <VARIABLE> [--description TEXT] [--example TEXT] [--optional]",
      );
    const s = exists(p) ? readSchema(p) : starterSchema(root, r);
    if (s.variables.some((x) => x.name === variable))
      fail(`${variable} already exists`);
    s.variables.push({
      name: variable,
      description: option(args, "--description") || "",
      example: option(args, "--example") || "",
      required: !args.includes("--optional"),
    });
    saveSchema(p, s);
    console.log(`added ${variable} to ${p}`);
    return;
  }
  if (sub === "remove") {
    const variable = args[2];
    if (!variable) fail("usage: uppr schema remove <app> <VARIABLE>");
    const s = readSchema(p),
      count = s.variables.length;
    s.variables = s.variables.filter((x) => x.name !== variable);
    if (s.variables.length === count) fail(`${variable} not found`);
    saveSchema(p, s);
    console.log(`removed ${variable} from ${p}`);
    return;
  }
  if (sub === "domains") {
    if (!args[2]) fail("usage: uppr schema domains <app> <domain[,domain...]>");
    const s: Schema = exists(p) ? readSchema(p) : starterSchema(root, r);
    s.domains = args.slice(2).join(",").split(",").map((x) => x.trim());
    saveSchema(p, s);
    console.log(`updated domains in ${p}; run uppr install to apply`);
    return;
  }
  if (sub === "rate") {
    const s: Schema = exists(p) ? readSchema(p) : starterSchema(root, r);
    const events = option(args, "--events");
    const window = option(args, "--window");
    const zone = option(args, "--zone");
    if (!events && !window && !zone)
      fail("usage: uppr schema rate <app> --events N --window 1m --zone dynamic");
    s.rate_limit = {
      ...defaultRateLimit,
      ...s.rate_limit,
      ...(events !== undefined ? { events: Number(events) } : {}),
      ...(window !== undefined ? { window } : {}),
      ...(zone !== undefined ? { zone } : {}),
    };
    saveSchema(p, s);
    console.log(`updated rate limit in ${p}; run uppr install to apply`);
    return;
  }
  fail("usage: uppr schema generate|edit|show|add|remove|domains|rate <name>");
}
export async function command(argv: string[]) {
  const [cmd, ...args] = argv;
  if (!cmd || cmd === "help" || cmd === "--help" || cmd === "-h") {
    console.log(commandHelp(args[0]));
    return;
  }
  if (args.includes("--help") || args.includes("-h")) {
    console.log(commandHelp(cmd));
    return;
  }
  if (cmd === "init") {
    only(args, 1, "uppr init <directory>");
    console.log("initialized", initPad(args[0]));
    return;
  }
  if (cmd === "restore") {
    only(args, 2, "uppr restore <archive.tar.gz> <directory>");
    await restore(args[0], args[1]);
    return;
  }
  if (cmd === "dump") {
    if (args.length > 1) fail("usage: uppr dump [directory]");
    const p = path.resolve(args[0] || ".", "UPPR.md");
    fs.writeFileSync(p, guide);
    console.log("wrote", p);
    return;
  }
  if (cmd === "askpass") {
    if (args[0]?.toLowerCase().includes("username"))
      console.log("x-access-token");
    else if (args[0]?.toLowerCase().includes("password"))
      console.log(process.env.UPPR_GIT_TOKEN || "");
    return;
  }
  if (cmd === "deps") {
    if (args[0] === "docker-access") {
      only(args, 1, "uppr deps docker-access");
      linux();
      if (!which("docker")) fail("install Docker first with uppr deps install");
      const username = os.userInfo().username;
      privileged("groupadd", ["-f", "docker"]);
      privileged("usermod", ["-aG", "docker", username]);
      console.log(`Added ${username} to the docker group. Sign out and back in before launching apps.`);
      return;
    }
    if (args[0] === "caddy") {
      only(args, 1, "uppr deps caddy");
      buildRateLimitCaddy();
      return;
    }
    if (args[0] === "check") {
      only(args, 1, "uppr deps check");
      const rows = dependencies();
      for (const row of rows)
        console.log(row.ok ? "OK" : row.required ? "MISSING" : "OPTIONAL", row.name, "—", row.detail);
      if (rows.some((row) => row.required && !row.ok)) fail("dependencies missing");
      return;
    }
    if (args[0] === "install") {
      only(args, 1, "uppr deps install");
      linux();
      console.log("Installing host packages. Sudo may ask for your password...");
      if (which("apt-get")) {
        const buildxPackage = ["docker-buildx", "docker-buildx-plugin"].find((pkg) => probe("apt-cache", ["show", pkg]).ok);
        privileged("apt-get", [
          "install",
          "-y",
          "git",
          "sudo",
          "systemd",
          "curl",
          "docker.io",
          "golang-go",
          ...(buildxPackage ? [buildxPackage] : []),
        ]);
        privileged("systemctl", ["enable", "--now", "docker.service"]);
      } else if (which("dnf")) {
        const buildxPackage = ["docker-buildx-plugin", "docker-buildx"].find((pkg) => probe("dnf", ["info", pkg]).ok);
        privileged("dnf", ["install", "-y", "git", "sudo", "systemd", "curl", "moby-engine", "golang", ...(buildxPackage ? [buildxPackage] : [])]);
        privileged("systemctl", ["enable", "--now", "docker.service"]);
      } else fail("automatic dependency installation supports apt and dnf");
      if (!dependencies().find((row) => row.name === "Caddy rate limit")?.ok)
        buildRateLimitCaddy();
      console.log("Host package installation complete.");
      for (const row of dependencies())
        console.log(row.ok ? "OK" : row.required ? "NEEDS ATTENTION" : "OPTIONAL", row.name, "—", row.detail);
      return;
    }
    fail("usage: uppr deps check|install|caddy|docker-access");
  }
  if (cmd === "auth") {
    only(args, 1, "uppr auth login|status|logout");
    if (args[0] === "status")
      console.log(gitToken() ? "GitHub token saved" : "no GitHub token saved");
    else if (args[0] === "logout") {
      removeGitToken();
      console.log("GitHub token removed");
    } else if (args[0] === "login") {
      if (!process.stdin.isTTY) fail("auth login needs a terminal");
      const token = ttyLine("GitHub personal access token: ", true);
      saveGitToken(token);
      console.log("GitHub token saved");
    } else fail("usage: uppr auth login|status|logout");
    return;
  }
  if (cmd === "run-app") {
    only(args, 2, "uppr run-app <launchpad> <repo>");
    const pad = path.resolve(args[0]),
      man = loadManifest(pad),
      r = select(man, args[1])[0];
    prepareRepo(pad, r);
    const p = repoDir(pad, r);
    const appPort = containerPort(pad, r);
    const portEnv = readSchema(schemaPath(pad, r)).variables.some(
      (v) => v.name === "PORT",
    )
      ? ["--env", `PORT=${appPort}`]
      : [];
    await import("./core.ts").then((x) =>
      x.spawnWait("docker", [
        "run",
        "--rm",
        "--name",
        containerName(man, r),
        "--publish",
        `127.0.0.1:${r.host_port}:${appPort}`,
        "--env-file",
        path.join(p, "config", ".env"),
        ...portEnv,
        "--mount",
        `type=bind,src=${path.join(p, "config")},dst=/app/config`,
        "--mount",
        `type=bind,src=${path.join(p, "data")},dst=/app/data`,
        imageName(man, r),
      ]),
    );
    return;
  }
  const [root, m] =
    cmd === "serve" &&
    args[0] &&
    exists(args[0]) &&
    fs.statSync(args[0]).isDirectory()
      ? findPad(args[0])
      : findPad();
  if (cmd === "list") {
    only(args, 0, "uppr list");
    console.log(`Launchpad ${m.id} · ${root}`);
    if (!m.repos.length) {
      console.log("No apps registered. Run uppr add <git-url> to add one.");
      return;
    }
    for (const r of sorted(m)) {
      const status = appStatus(m, r);
      const setup = appSetup(root, r, status);
      let domains: string[];
      let port: string;
      try { domains = appRouting(root, r).domains; } catch { domains = [r.domain]; }
      try { port = String(containerPort(root, r)); } catch { port = "?"; }
      console.log(`\n${r.name} · ${status.state}`);
      console.log(`  ${domains.map((d) => `https://${d}`).join(", ")} → 127.0.0.1:${r.host_port} → ${port}`);
      console.log(`  Service: ${status.service} · Container: ${status.container} · Health: ${status.health}`);
      console.log(`  Checkout: ${repoDir(root, r)}`);
      console.log(`  Next: ${setup.label} — ${setup.detail}`);
      const next: Record<string, string> = {
        pull: `uppr pull ${r.name}`,
        "schema-generate": `uppr schema generate ${r.name}`,
        "schema-edit": `uppr schema edit ${r.name}`,
        "env-setup": `uppr prepare ${r.name}`,
        install: "uppr install",
        serve: `uppr launch ${r.name}`,
        logs: `uppr logs ${r.name}`,
      };
      if (setup.action && next[setup.action]) console.log(`  Run: ${next[setup.action]}`);
    }
    return;
  }
  if (cmd === "env") {
    const [sub, name, variable] = args;
    if (!sub || !name || args.length > 3) fail("usage: uppr env list|set|unset <app> [VARIABLE]");
    const r = select(m, name)[0];
    const variables = readSchema(schemaPath(root, r)).variables;
    const env = path.join(repoDir(root, r), "config", ".env");
    const values = exists(env) ? readEnv(fs.readFileSync(env, "utf8")) : {};
    if (sub === "list" && !variable) {
      console.log(env);
      if (!variables.length) console.log("No variables declared in schema.json.");
      for (const v of variables) {
        console.log(`${v.name} · ${v.required === false ? "optional" : "required"} · ${values[v.name]?.trim() ? "set" : "empty"}${v.description ? ` · ${v.description}` : ""}${v.example ? ` · example: ${v.example}` : ""}`);
      }
      return;
    }
    if ((sub === "set" || sub === "unset") && variable && envName.test(variable)) {
      if (!variables.some((v) => v.name === variable)) fail(`${variable} is not in ${r.name}/schema.json`);
      let value = "";
      if (sub === "set") {
        if (process.stdin.isTTY) value = ttyLine(`${variable} for ${r.name}: `, true);
        else value = fs.readFileSync(0, "utf8").replace(/\r?\n$/, "");
        if (!value) fail(`${variable} needs a value; use uppr env unset to clear it`);
      }
      saveRepoEnv(root, r, { [variable]: value });
      console.log(`${sub === "set" ? "set" : "cleared"} ${variable} for ${r.name}; restart the app to apply`);
      return;
    }
    fail("usage: uppr env list <app> | env set <app> <VARIABLE> | env unset <app> <VARIABLE>");
  }
  if (cmd === "schema") {
    schemaCommand(args);
    return;
  }
  if (cmd === "add") {
    if (!args[0]) fail("usage: uppr add <url> [options]");
    const url = args[0],
      name =
        option(args, "--name") ||
        path
          .basename(url)
          .replace(/\.git$/, "")
          .toLowerCase(),
      domain = option(args, "--domain") || `${name}.localhost`,
      port = Number(option(args, "--port") || freePort(m)),
      container = Number(option(args, "--container-port") || 3000),
      branch = option(args, "--branch") || "";
    if (
      !safeName.test(name) ||
      !validDomain(domain) ||
      !validPort(port) ||
      !validPort(container)
    )
      fail("invalid name, domain, or port");
    if (
      m.repos.some(
        (r) =>
          r.name === name ||
          r.url === url ||
          r.host_port === port ||
          r.domain === domain,
      )
    )
      fail("repo name, URL, host port, or domain already assigned");
    m.repos.push({
      name,
      url,
      branch,
      domain,
      host_port: port,
      container_port: container,
    });
    saveManifest(root, m);
    console.log(`added ${name}; run uppr pull ${name}`);
    return;
  }
  if (cmd === "remove" || cmd === "rm") {
    only(args, 1, "uppr remove <name>");
    select(m, args[0]);
    m.repos = m.repos.filter((r) => r.name !== args[0]);
    saveManifest(root, m);
    console.log(
      `removed ${args[0]} from manifest; files remain. Run uppr install to remove its service.`,
    );
    return;
  }
  if (cmd === "pull") {
    if (args.length !== 1) fail("usage: uppr pull <name|--all>");
    for (const r of select(m, args[0] === "--all" ? undefined : args[0])) {
      console.log("pulling", r.name);
      pull(root, r);
      console.log("pulled", r.name);
    }
    return;
  }
  if (cmd === "caddyfile") {
    only(args, 0, "uppr caddyfile");
    process.stdout.write(renderCaddy(root, m));
    return;
  }
  if (cmd === "curl") {
    if (!args[0] || args[0] === "--all")
      fail("usage: uppr curl <name> [path] [curl options]");
    const r = select(m, args[0])[0];
    const rest = args.slice(1);
    const route = rest[0]?.startsWith("/") ? rest.shift()! : "/";
    const url = `http://127.0.0.1:${r.host_port}${route}`;
    const curlArgs = [
      "--no-progress-meter",
      "--include",
      "--fail-with-body",
      ...rest,
      url,
    ];
    console.log("$ curl", curlArgs.map(shellQuote).join(" "));
    console.error(`Waiting for response from ${r.name}...`);
    run("curl", curlArgs);
    return;
  }
  if (cmd === "push") {
    if (args.length < 3 || args[1] !== "-m")
      fail("usage: uppr push <name> -m <message> [--yes]");
    const confirmed = args.at(-1) === "--yes";
    push(root, select(m, args[0])[0], args.slice(2, confirmed ? -1 : undefined).join(" "), confirmed);
    return;
  }
  if (cmd === "prepare") {
    if (args.length > 1) fail("usage: uppr prepare [name]");
    for (const r of select(m, args[0])) {
      prepareRepo(
        root,
        r,
        process.stdin.isTTY
          ? (v) =>
              ttyLine(
                `${v.name} for ${r.name}${v.description ? ` — ${v.description}` : ""}: `,
                true,
              )
          : undefined,
      );
      console.log("prepared", r.name);
    }
    return;
  }
  if (cmd === "generate") {
    generate(root, m);
    console.log(`generated ${m.repos.length} app units and Caddyfile`);
    return;
  }
  if (cmd === "install") {
    await installHost(root, m);
    return;
  }
  if (cmd === "uninstall") {
    uninstallHost(m);
    return;
  }
  if (cmd === "serve" || cmd === "restart" || cmd === "launch") {
    linux();
    let targetRoot = root,
      targetM = m,
      name: string | undefined;
    if (cmd === "restart") {
      only(args, 1, "uppr restart <name>");
      name = args[0];
    } else if (cmd === "launch") {
      if (args.length !== 1) fail("usage: uppr launch <name|--all>");
      name = args[0] === "--all" ? undefined : args[0];
    } else if (args.length === 1) {
      if (exists(args[0]) && fs.statSync(args[0]).isDirectory())
        [targetRoot, targetM] = findPad(args[0]);
      else name = args[0];
    } else if (args.length === 2) {
      [targetRoot, targetM] = findPad(args[0]);
      name = args[1];
    } else if (args.length > 2) fail("usage: uppr serve [path] [name]");
    const repos = select(targetM, name);
    console.log("Checking services and Docker...");
    requireInstalled(targetM, repos);
    dockerAccess();
    buildx();
    const changed = new Map<string, boolean>();
    for (const r of repos) {
      console.log(`Building ${r.name}...`);
      const before = imageId(targetM, r);
      buildApp(targetRoot, targetM, r);
      const after = imageId(targetM, r);
      changed.set(r.name, before !== after || !after);
    }
    for (const r of repos) {
      const action =
        cmd === "restart"
          ? "restart"
          : active(unitName(targetM, r))
            ? changed.get(r.name)
              ? "restart"
              : "unchanged"
            : "start";
      if (action === "unchanged") {
        console.log("unchanged", r.name);
        continue;
      }
      console.log(`${action}ing ${r.name} service...`);
      privileged("systemctl", [action, unitName(targetM, r)]);
    }
    for (const r of repos) await ready(targetM, r);
    return;
  }
  if (cmd === "stop") {
    linux();
    if (args.length > 1) fail("usage: uppr stop [name]");
    for (const r of select(m, args[0]))
      privileged("systemctl", ["stop", unitName(m, r)]);
    return;
  }
  if (cmd === "status") {
    if (args.length > 1) fail("usage: uppr status [name]");
    for (const r of select(m, args[0])) {
      const s = appStatus(m, r);
      const domains = appRouting(root, r).domains;
      console.log(
        `${r.name}: ${s.state} | service ${s.service} | container ${s.container} | health ${s.health} | ${domains.map((d) => `https://${d}`).join(", ")} → 127.0.0.1:${r.host_port} → ${containerPort(root, r)}`,
      );
    }
    return;
  }
  if (cmd === "logs") {
    linux();
    if (
      args.length < 1 ||
      args.length > 2 ||
      (args.length === 2 && args[1] !== "--no-follow")
    )
      fail("usage: uppr logs <name> [--no-follow]");
    run("journalctl", [
      "-u",
      unitName(m, select(m, args[0])[0]),
      "-n",
      "200",
      ...(args[1] === "--no-follow" ? ["--no-pager"] : ["-f"]),
    ]);
    return;
  }
  if (cmd === "doctor") {
    let bad = false;
    for (const tool of ["git", "docker", "caddy", "bun"]) {
      const ok = tool === "bun" ? exists(process.execPath) : tool === "caddy" ? !!caddyBinary() : !!which(tool);
      console.log(ok ? "OK" : "MISSING", tool);
      bad ||= !ok;
    }
    const caddyRateLimit = !!caddyBinary() && probe(caddyBinary(), ["list-modules"]).out.split("\n").includes("http.handlers.rate_limit");
    console.log(caddyRateLimit ? "OK" : "MISSING", "Caddy rate_limit module");
    bad ||= !caddyRateLimit;
    if (which("docker")) {
      const p = probe("docker", ["info", "--format", "{{.ServerVersion}}"]);
      if (!p.ok) {
        console.log("DOCKER unavailable:", p.err);
        bad = true;
      }
      if (!probe("docker", ["buildx", "version"]).ok) {
        console.log("BUILDX unavailable");
        bad = true;
      }
    }
    for (const r of sorted(m)) {
      try {
        prepareRepo(root, r);
        console.log("READY", r.name);
      } catch (e) {
        console.log("NOT READY", r.name, (e as Error).message);
        bad = true;
      }
    }
    if (bad) fail("doctor found issues");
    return;
  }
  if (cmd === "backup") {
    only(args, 1, "uppr backup <archive.tar.gz>");
    await backup(root, m, args[0]);
    return;
  }
  fail(`unknown command ${cmd}; run uppr help`);
}
async function backup(root: string, m: Manifest, dest: string) {
  dest = path.resolve(dest);
  if (dest === root || dest.startsWith(root + path.sep))
    fail("backup archive must be outside the launchpad");
  console.log("Preparing backup...");
  const stopped: string[] = [];
  try {
    if (process.platform === "linux") {
      if (!which("systemctl"))
        fail("systemctl is required for a consistent Linux backup");
      for (const r of m.repos) {
        const name = unitName(m, r);
        if (active(name)) {
          console.log(`Stopping ${r.name} for backup...`);
          privileged("systemctl", ["stop", name]);
          stopped.push(name);
        }
      }
    } else
      console.error(
        "Ensure apps are stopped before backing up SQLite on this host",
      );
    fs.mkdirSync(path.dirname(dest), { recursive: true });
    const tmp = dest + ".tmp-" + process.pid;
    try {
      console.log("Checking files and creating archive...");
      checkArchiveLinks(root, root);
      await tar.c({ gzip: true, file: tmp, cwd: root }, ["."]);
      fs.renameSync(tmp, dest);
    } finally {
      fs.rmSync(tmp, { force: true });
    }
    console.log("wrote", dest);
  } finally {
    for (const name of stopped) {
      console.log(`Restarting ${name}...`);
      privileged("systemctl", ["start", name]);
    }
  }
}
function checkArchiveLinks(root: string, dir: string) {
  for (const entry of fs.readdirSync(dir)) {
    const p = path.join(dir, entry),
      info = fs.lstatSync(p);
    if (info.isSymbolicLink()) {
      const target = path.resolve(path.dirname(p), fs.readlinkSync(p));
      if (target !== root && !target.startsWith(root + path.sep))
        fail(`symlink ${p} points outside launchpad`);
    } else if (info.isDirectory()) checkArchiveLinks(root, p);
    else if (!info.isFile()) fail(`unsupported file type ${p}`);
  }
}
async function restore(archive: string, dest: string) {
  dest = path.resolve(dest);
  if (exists(dest)) fail(`destination ${dest} already exists`);
  const stage = fs.mkdtempSync(path.join(path.dirname(dest), ".uppr-restore-"));
  try {
    console.log("Extracting and checking backup archive...");
    await tar.x({
      file: path.resolve(archive),
      cwd: stage,
      strict: true,
      filter: (name, entry) => {
        const normalized = name.replace(/^\.\//, "");
        if (path.isAbsolute(normalized) || normalized.split("/").includes(".."))
          fail(`unsafe archive path ${name}`);
        if ("type" in entry && entry.type === "SymbolicLink") {
          const target = path.resolve(
            stage,
            path.dirname(normalized),
            ("linkpath" in entry ? entry.linkpath : "") || "",
          );
          if (target !== stage && !target.startsWith(stage + path.sep))
            fail(`unsafe symlink ${name}`);
        }
        if ("type" in entry && entry.type === "Link")
          fail(`hard links are not supported in restore: ${name}`);
        return true;
      },
    });
    loadManifest(stage);
    fs.renameSync(stage, dest);
    console.log(
      `restored ${dest}; run uppr doctor, uppr generate, and uppr install there`,
    );
  } finally {
    if (exists(stage)) fs.rmSync(stage, { recursive: true, force: true });
  }
}

const guide = `# Integrating an application with Uppr

Commit a production Dockerfile and schema.json at the repository root. The container must listen on 0.0.0.0 at its configured port. Store persistent data under /app/data and read environment variables from process.env. Uppr bind mounts config/ and data/ and passes config/.env through Docker --env-file. Keep config/ and data/ out of Git and Docker build context.

Example schema.json:

\`\`\`json
{
  "domains": ["api.example.com"],
  "rate_limit": { "events": 100, "window": "1m", "zone": "dynamic" },
  "variables": [{ "name": "PORT", "example": "3000", "required": false }]
}
\`\`\`

The domains array is the source for Caddy routes. If it is absent, Uppr uses the launchpad manifest domain for older apps. Rate limiting applies per client IP through the Caddy rate_limit module. Missing rate_limit values default to 100 events per 1m in zone dynamic. Uppr prefixes the generated zone with the launchpad and app IDs to isolate apps. Set the values per application as needed. Each domain routes to the app's loopback host port. DNS must point to the host. After a schema route change, run uppr install.

Create or update a schema with uppr schema generate <app>, uppr schema domains <app> api.example.com,alt.example.com, and uppr schema rate <app> --events 100 --window 1m --zone dynamic. Schema changes are local Git changes; inspect and push them with uppr push <app> -m "Update routes". Store a GitHub personal access token for HTTPS remotes with uppr auth login; SSH remotes use your SSH key.

The Caddy binary must include github.com/mholt/caddy-ratelimit. Run uppr deps install or uppr deps caddy to build one with xcaddy and install it as /usr/local/bin/uppr-caddy. Uppr validates generated Caddyfiles before installing them. Use uppr caddyfile to preview the routes and uppr doctor to check the host.
`;
export const help = `Uppr — deploy Docker apps with systemd and Caddy

Usage: uppr <command> [arguments]
       uppr help [command]

Start here
  init <directory>                 Create a launchpad
  deps check|install               Check or install Linux host dependencies
  add <git-url> [options]          Register an app
  pull <name|--all>                Clone or fast-forward checkouts
  schema generate <name>           Create an app schema
  prepare [name]                   Fill missing required environment values
  install                          Install systemd units and Caddy routes
  launch <name|--all>              Build and start current checkouts
  list                             Show apps, routes, state, and next steps

Manage apps
  env list|set|unset <name> [VAR]  Inspect or change app environment
  schema <subcommand>              Manage variables, domains, and rate limits
  status [name]                    Show service and container status
  logs <name> [--no-follow]        Show journal logs
  curl <name> [path] [curl args]    Call an app's loopback port
  restart <name> | stop [name]     Control app services
  push <name> -m <message>         Commit and push a checkout
  remove <name>                   Unregister an app; preserve its files

Host and data
  caddyfile | generate             Preview or write generated configuration
  uninstall                        Remove this launchpad's installed services
  deps caddy|docker-access         Build Caddy or grant Docker group access
  doctor                           Check host and app readiness
  auth login|status|logout         Manage HTTPS Git token
  backup <archive.tar.gz>          Archive launchpad, including data
  restore <archive> <directory>    Restore into a new directory
  dump [directory]                 Write the app integration guide

Run uppr help <command> for options and examples. Commands except init, restore,
deps, auth, and help run from a launchpad or any directory inside it.`;

const topics: Record<string, string> = {
  init: `uppr init <directory>\nCreate a launchpad with launchpad.json, repos/, and systemd/.`,
  add: `uppr add <git-url> [--name NAME] [--domain DOMAIN] [--port HOST_PORT] [--container-port PORT] [--branch BRANCH]\nRegister an app. The host port is assigned automatically if omitted; the container port defaults to 3000. Then run uppr pull <name>.`,
  pull: `uppr pull <name|--all>\nClone or fast-forward Git checkouts. This does not build or start apps.`,
  schema: `uppr schema generate <app> [--from-env FILE] [--force]\nuppr schema show|edit <app>\nuppr schema add <app> <VARIABLE> [--description TEXT] [--example TEXT] [--optional]\nuppr schema remove <app> <VARIABLE>\nuppr schema domains <app> <domain[,domain...]>\nuppr schema rate <app> [--events N] [--window 1m] [--zone dynamic]\nThe schema lives at repos/<app>/schema.json. Run uppr install after changing routes or rate limits.`,
  env: `uppr env list <app>\nuppr env set <app> <VARIABLE>\nuppr env unset <app> <VARIABLE>\nList shows schema descriptions, examples, and whether values are set without printing secrets. Set reads one value from a hidden terminal prompt or stdin (for example: printf '%s' "$TOKEN" | uppr env set api API_KEY). Unset clears an optional value. Restart the app after changes.`,
  prepare: `uppr prepare [app]\nPrompt for missing required schema variables and create config/.env and data/main.sqlite. Existing values are preserved. Use uppr env list <app> to inspect variable status and uppr env set <app> <VARIABLE> to set optional values.`,
  install: `uppr install\nValidate and install app systemd units and shared Caddy routes; enable services and start or reload Caddy. Run this after route, port, or manifest changes. Requires Linux, Docker, systemd, and Caddy with rate_limit.`,
  launch: `uppr launch <name|--all>\nBuild Docker images from current checkouts, start or restart changed services, and wait for readiness. Does not pull from Git.`,
  serve: `uppr serve [launchpad-path] [name]\nCompatibility alias for launch. With no name, serves all apps in the launchpad.`,
  list: `uppr list\nShow each app's state, route, ports, checkout, and next setup step.`,
  status: `uppr status [name]\nShow service, container, and health state. Omit name for all apps.`,
  logs: `uppr logs <name> [--no-follow]\nShow the last 200 journal lines and follow new output by default.`,
  curl: `uppr curl <name> [path] [curl options]\nCall the app on its local host port. Example: uppr curl api /health. Options are passed to curl.`,
  restart: `uppr restart <name>\nRebuild the app image, restart its service, and wait for readiness.`,
  stop: `uppr stop [name]\nStop one app or all apps in the launchpad.`,
  remove: `uppr remove <name>\nRemove the app from launchpad.json while preserving its checkout. Run uppr install to remove installed routes and service.`,
  push: `uppr push <name> -m <message> [--yes]\nReview local changes, then commit and push the app checkout. Confirmation is required unless --yes is given. Protected config/ and data/ files cannot be tracked.`,
  auth: `uppr auth login|status|logout\nStore, check, or remove a GitHub personal access token for HTTPS Git remotes. SSH remotes use your SSH key.`,
  deps: `uppr deps check|install|caddy|docker-access\nCheck dependencies, install Linux packages, build Caddy with rate_limit, or add your account to the Docker group. Sign out and back in after docker-access.`,
  doctor: `uppr doctor\nCheck host tools, Docker access, Buildx, Caddy rate_limit, and app preparation.`,
  caddyfile: `uppr caddyfile\nPrint the current launchpad's generated Caddyfile without changing files.`,
  generate: `uppr generate\nWrite local systemd units and Caddyfile for review.`,
  uninstall: `uppr uninstall\nRemove this launchpad's installed services and routes. Checkout files and data remain.`,
  backup: `uppr backup <archive.tar.gz>\nStop active app services during archive creation, then restart them. The archive includes secrets and data.`,
  restore: `uppr restore <archive.tar.gz> <directory>\nRestore into a new directory, then run uppr doctor, uppr generate, and uppr install there.`,
  dump: `uppr dump [directory]\nWrite UPPR.md with application integration instructions.`,
};
function commandHelp(topic?: string) {
  if (!topic) return help;
  if (topic === "rm") topic = "remove";
  if (topic === "run-app" || topic === "askpass") return `uppr ${topic} is an internal service command.`;
  return topics[topic] || `Unknown help topic: ${topic}\n\n${help}`;
}
