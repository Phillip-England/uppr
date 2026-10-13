import fs from "node:fs";
import { probe, which } from "./core.ts";

export type Dependency = { name: string; ok: boolean; detail: string; required: boolean };

export function caddyBinary() {
  return fs.existsSync("/usr/local/bin/uppr-caddy")
    ? "/usr/local/bin/uppr-caddy"
    : which("uppr-caddy") || which("caddy");
}

export function dependencies(): Dependency[] {
  const docker = which("docker");
  const caddy = caddyBinary();
  const module = !!caddy && probe(caddy, ["list-modules"]).out.split("\n").includes("http.handlers.rate_limit");
  const daemon = docker ? probe("docker", ["info", "--format", "{{.ServerVersion}}"]) : undefined;
  const systemd = process.platform === "linux" && !!which("systemctl") && probe("systemctl", ["show", "--property=Version", "--value"]).ok;
  return [
    { name: "Linux and systemd", ok: systemd, detail: systemd ? "ready" : process.platform !== "linux" ? "deployment requires a Linux host" : "systemd is unavailable", required: true },
    { name: "Systemd verifier", ok: !!which("systemd-analyze"), detail: "validates generated services before installation", required: true },
    { name: "Sudo", ok: process.getuid?.() === 0 || !!which("sudo"), detail: "needed for host changes unless running as root", required: true },
    { name: "Git", ok: !!which("git"), detail: which("git") ? "ready" : "needed to pull repositories", required: true },
    { name: "Docker", ok: !!docker, detail: docker ? "installed" : "needed to build and run apps", required: true },
    { name: "Docker access", ok: !!daemon?.ok, detail: daemon?.ok ? `daemon ${daemon.out}` : docker ? (daemon?.err || daemon?.out || "daemon unavailable").split("\n")[0] : "install Docker first", required: true },
    { name: "Docker Buildx", ok: !!docker && probe("docker", ["buildx", "version"]).ok, detail: "recommended for Docker builds", required: false },
    { name: "Caddy rate limit", ok: module, detail: module ? caddy : caddy ? "custom rate_limit module missing" : "Caddy is missing", required: true },
    { name: "Go or xcaddy", ok: !!(which("go") || which("xcaddy")), detail: module ? "only needed to rebuild Caddy" : "needed to build the custom Caddy binary", required: !module },
    { name: "Bun runtime", ok: fs.existsSync(process.execPath), detail: "runs Uppr itself", required: true },
    { name: "Curl", ok: !!which("curl"), detail: "used by the optional app request command", required: false },
  ];
}
