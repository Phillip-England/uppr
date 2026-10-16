import { test, expect } from "bun:test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { lookupPublicIp } from "./commands.ts";

function cli(cwd: string, ...args: string[]) {
  return spawnSync("bun", [path.resolve(import.meta.dir, "cli.ts"), ...args], {
    cwd,
    encoding: "utf8",
  });
}
test("no arguments and command help work outside a launchpad", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-help-test-"));
  try {
    const root = cli(base);
    expect(root.status).toBe(0);
    expect(root.stdout).toContain("Usage: uppr <command>");
    expect(root.stdout).toContain("uppr help <command>");
    expect(root.stdout).toContain("domains <name> <domain,...>");
    expect(root.stdout).toContain("schema edit <name>");
    const topic = cli(base, "schema", "--help");
    expect(topic.status).toBe(0);
    expect(topic.stdout).toContain("uppr schema domains");
    expect(topic.stdout).toContain("uppr schema edit <app>");
    expect(topic.stdout).toContain("$VISUAL, $EDITOR, or vi");
    expect(cli(base, "domains", "--help").stdout).toContain("uppr domains <app>");
    expect(cli(base, "ip", "--help").stdout).toContain("uppr ip");
    expect(cli(base, "deps", "--help").stdout).toContain("uppr deps caddy-switch");
    expect(cli(base, "pull", "--help").stdout).toContain("[--ff]");
  } finally { fs.rmSync(base, { recursive: true, force: true }); }
});
test("environment commands show metadata and set values without printing them", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-env-test-"));
  const root = path.join(base, "pad");
  try {
    expect(cli(base, "init", root).status).toBe(0);
    expect(cli(root, "add", "https://example.com/api.git", "--name", "api", "--domain", "api.example.com").status).toBe(0);
    const repo = path.join(root, "repos", "api");
    fs.mkdirSync(repo);
    fs.writeFileSync(path.join(repo, "Dockerfile"), "FROM scratch\n");
    fs.writeFileSync(path.join(repo, "schema.json"), JSON.stringify({ variables: [{ name: "TOKEN", description: "API token", required: false }] }));
    const set = spawnSync("bun", [path.resolve(import.meta.dir, "cli.ts"), "env", "set", "api", "TOKEN"], { cwd: root, input: "secret-value\n", encoding: "utf8" });
    expect(set.status).toBe(0);
    expect(fs.readFileSync(path.join(repo, "config", ".env"), "utf8")).toContain("TOKEN=secret-value");
    const list = cli(root, "env", "list", "api");
    expect(list.status).toBe(0);
    expect(list.stdout).toContain("TOKEN · optional · set · API token");
    expect(list.stdout).not.toContain("secret-value");
    expect(cli(root, "env", "unset", "api", "TOKEN").status).toBe(0);
    expect(cli(root, "env", "list", "api").stdout).toContain("TOKEN · optional · empty");
  } finally { fs.rmSync(base, { recursive: true, force: true }); }
});
test("registration and runtime require explicit domain and environment setup", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-setup-gate-"));
  const root = path.join(base, "pad");
  try {
    expect(cli(base, "init", root).status).toBe(0);
    const missingDomain = cli(root, "add", "https://example.com/api.git", "--name", "api");
    expect(missingDomain.status).toBe(1);
    expect(missingDomain.stderr).toContain("--domain");
    expect(JSON.parse(fs.readFileSync(path.join(root, "launchpad.json"), "utf8")).repos).toEqual([]);
    expect(cli(root, "add", "https://example.com/api.git", "--name", "api", "--domain", "api.example.com").status).toBe(0);
    const repo = path.join(root, "repos", "api");
    fs.mkdirSync(repo);
    fs.writeFileSync(path.join(repo, "Dockerfile"), "FROM scratch\n");
    const schema = path.join(repo, "schema.json");
    fs.writeFileSync(schema, JSON.stringify({ variables: [{ name: "TOKEN" }] }));
    expect(cli(root, "run-app", root, "api").stderr).toContain("domains are not configured");
    fs.writeFileSync(schema, JSON.stringify({ domains: ["api.example.com"], variables: [{ name: "TOKEN" }] }));
    expect(cli(root, "run-app", root, "api").stderr).toContain("config/.env is missing");
    expect(fs.existsSync(path.join(repo, "config", ".env"))).toBe(false);
    fs.mkdirSync(path.join(repo, "config"));
    fs.writeFileSync(path.join(repo, "config", ".env"), "TOKEN=\n");
    expect(cli(root, "run-app", root, "api").stderr).toContain("required values missing");
  } finally { fs.rmSync(base, { recursive: true, force: true }); }
});
test("delete removes a checkout and registration only after confirmation", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-delete-test-"));
  const root = path.join(base, "pad");
  try {
    expect(cli(base, "init", root).status).toBe(0);
    expect(cli(root, "add", "https://example.com/api.git", "--name", "api", "--domain", "api.example.com").status).toBe(0);
    const repo = path.join(root, "repos", "api");
    fs.mkdirSync(path.join(repo, "data"), { recursive: true });
    fs.writeFileSync(path.join(repo, "data", "main.sqlite"), "private data");
    expect(cli(root, "delete", "api").status).toBe(1);
    expect(fs.existsSync(repo)).toBe(true);
    expect(cli(root, "delete", "api", "--bad").status).toBe(1);
    expect(cli(root, "delete", "api", "--yes").status).toBe(0);
    expect(fs.existsSync(repo)).toBe(false);
    expect(JSON.parse(fs.readFileSync(path.join(root, "launchpad.json"), "utf8")).repos).toEqual([]);
    expect(fs.readFileSync(path.join(root, "Caddyfile"), "utf8")).not.toContain("api.localhost");
    expect(cli(root, "list").stdout).toContain("No apps registered");
  } finally { fs.rmSync(base, { recursive: true, force: true }); }
});
test("dependency check reports the host services needed to launch apps", () => {
  const result = cli(process.cwd(), "deps", "check");
  expect(result.stdout).toContain("Linux and systemd");
  expect(result.stdout).toContain("Docker access");
  expect(result.stdout).toContain("Docker Buildx");
  expect(result.stdout).toContain("Caddy rate limit");
});
test("CLI creates a schema, lists its route, and archives/restores a pad", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-cli-test-")),
    root = path.join(base, "pad");
  try {
    expect(cli(base, "init", root).status).toBe(0);
    expect(
      cli(
        root,
        "add",
        "https://example.com/api.git",
        "--name",
        "api",
        "--domain",
        "api.example.com",
      ).status,
    ).toBe(0);
    const repo = path.join(root, "repos", "api");
    fs.mkdirSync(repo);
    fs.writeFileSync(path.join(repo, "Dockerfile"), "FROM scratch\n");
    fs.writeFileSync(path.join(repo, ".env.example"), "API_KEY=placeholder\n");
    expect(cli(root, "schema", "generate", "api").status).toBe(0);
    const schema = JSON.parse(
      fs.readFileSync(path.join(repo, "schema.json"), "utf8"),
    );
    expect(schema.variables.map((v: { name: string }) => v.name)).toEqual([
      "API_KEY",
    ]);
    expect(
      fs.readFileSync(path.join(repo, "schema.json"), "utf8"),
    ).not.toContain("placeholder");
    const list = cli(root, "list");
    expect(list.status).toBe(0);
    expect(list.stdout).toContain("api.example.com");
    expect(list.stdout).toContain("127.0.0.1:20000");
    const archive = path.join(base, "backup.tar.gz"),
      dest = path.join(base, "restored");
    expect(cli(root, "backup", archive).status).toBe(0);
    const restore = cli(base, "restore", archive, dest);
    expect(restore.status).toBe(0);
    expect(
      fs.readFileSync(path.join(dest, "repos", "api", "schema.json"), "utf8"),
    ).toContain("API_KEY");
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
});

test("schema edit rejects an invalid live schema without changing it", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-schema-test-"));
  const root = path.join(base, "pad");
  try {
    expect(cli(base, "init", root).status).toBe(0);
    expect(
      cli(root, "add", "https://example.com/api.git", "--name", "api", "--domain", "api.example.com").status,
    ).toBe(0);
    const repo = path.join(root, "repos", "api");
    fs.mkdirSync(repo);
    const schema = path.join(repo, "schema.json");
    fs.writeFileSync(schema, '{"variables": []}\n');
    const editor = path.join(base, "invalid-editor");
    fs.writeFileSync(
      editor,
      '#!/bin/sh\nprintf \'{"variables": [}\\n\' > "$1"\n',
      { mode: 0o700 },
    );
    const result = spawnSync(
      "bun",
      [path.resolve(import.meta.dir, "cli.ts"), "schema", "edit", "api"],
      {
        cwd: root,
        env: { ...process.env, VISUAL: editor, EDITOR: editor },
        encoding: "utf8",
      },
    );
    expect(result.status).toBe(1);
    expect(fs.readFileSync(schema, "utf8")).toBe('{"variables": []}\n');
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
});

test("curl shows its request and response, and caddyfile is readable without generating files", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-ops-test-"));
  const root = path.join(base, "pad");
  try {
    expect(cli(base, "init", root).status).toBe(0);
    expect(
      cli(
        root,
        "add",
        "https://example.com/api.git",
        "--name",
        "api",
        "--port",
        "24242",
        "--domain",
        "api.example.com",
      ).status,
    ).toBe(0);
    expect(cli(root, "pull").status).toBe(1);
    const localCaddy = fs.readFileSync(path.join(root, "Caddyfile"), "utf8");
    const caddy = cli(root, "caddyfile");
    expect(caddy.status).toBe(0);
    expect(caddy.stdout).toContain("api.example.com {");
    expect(caddy.stdout).toContain("reverse_proxy 127.0.0.1:24242");
    expect(fs.readFileSync(path.join(root, "Caddyfile"), "utf8")).toBe(
      localCaddy,
    );

    const bin = path.join(base, "bin");
    fs.mkdirSync(bin);
    fs.writeFileSync(
      path.join(bin, "curl"),
      '#!/bin/sh\nprintf "response body\\n"\n',
      { mode: 0o700 },
    );
    const response = spawnSync(
      "bun",
      [
        path.resolve(import.meta.dir, "cli.ts"),
        "curl",
        "api",
        "/health",
        "-X",
        "POST",
        "-d",
        "hello",
      ],
      {
        cwd: root,
        env: { ...process.env, PATH: `${bin}:${process.env.PATH}` },
        encoding: "utf8",
      },
    );
    expect(response.status).toBe(0);
    expect(response.stdout).toContain("$ curl");
    expect(response.stdout).toContain("'-d' 'hello'");
    expect(response.stdout).toContain("'http://127.0.0.1:24242/health'");
    expect(response.stdout).toContain("response body");
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
});
test("domains command creates a schema, validates domains, and updates routes", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-routing-test-"));
  const root = path.join(base, "pad");
  try {
    expect(cli(base, "init", root).status).toBe(0);
    expect(cli(root, "add", "https://example.com/api.git", "--name", "api", "--domain", "api.example.com").status).toBe(0);
    fs.mkdirSync(path.join(root, "repos", "api"));
    expect(cli(root, "domains", "api", "api.example.com,alt.example.com").status).toBe(0);
    const schemaPath = path.join(root, "repos", "api", "schema.json");
    const saved = fs.readFileSync(schemaPath, "utf8");
    expect(JSON.parse(saved).domains).toEqual(["api.example.com", "alt.example.com"]);
    expect(cli(root, "domains", "api", "bad domain").status).toBe(1);
    expect(fs.readFileSync(schemaPath, "utf8")).toBe(saved);
    expect(cli(root, "schema", "domains", "api", "api.example.com,alt.example.com").status).toBe(0);
    expect(cli(root, "schema", "rate", "api", "--events", "25", "--window", "10s").status).toBe(0);
    const caddy = cli(root, "caddyfile");
    expect(caddy.status).toBe(0);
    expect(caddy.stdout).toContain("alt.example.com {");
    expect(caddy.stdout).toContain("events 25");
    expect(caddy.stdout).toContain("window 10s");
    expect(cli(root, "teach").status).toBe(0);
    const guide = fs.readFileSync(path.join(root, "UPPR.md"), "utf8");
    expect(guide).toContain("rate_limit");
    expect(guide).toContain("domains");
    expect(guide).toContain('"port": { "variable": "APP_PORT" }');
    fs.writeFileSync(path.join(root, "UPPR.md"), "outdated guide\n");
    expect(cli(root, "teach").status).toBe(0);
    expect(fs.readFileSync(path.join(root, "UPPR.md"), "utf8")).toBe(guide);
  } finally { fs.rmSync(base, { recursive: true, force: true }); }
});

test("public IP lookup prints only a validated IPv4 address", async () => {
  expect(await lookupPublicIp(async () => new Response("203.0.113.7\n"))).toBe("203.0.113.7");
  expect(lookupPublicIp(async () => new Response("not an IP"))).rejects.toThrow("invalid IPv4");
  expect(lookupPublicIp(async () => new Response("error", { status: 503 }))).rejects.toThrow("HTTP 503");
});

test("run-app publishes the custom port selected by schema and env", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-port-test-"));
  const root = path.join(base, "pad");
  try {
    expect(cli(base, "init", root).status).toBe(0);
    expect(
      cli(root, "add", "https://example.com/api.git", "--name", "api", "--domain", "api.example.com").status,
    ).toBe(0);
    const repo = path.join(root, "repos", "api");
    fs.mkdirSync(path.join(repo, "config"), { recursive: true });
    fs.writeFileSync(path.join(repo, "Dockerfile"), "FROM scratch\n");
    fs.writeFileSync(
      path.join(repo, "schema.json"),
      '{"port":{"variable":"EGATE_PORT"},"domains":["api.example.com"],"variables":[{"name":"EGATE_PORT","example":"8839","required":false}]}\n',
    );
    fs.writeFileSync(path.join(repo, "config", ".env"), "EGATE_PORT=8840\n");
    const bin = path.join(base, "bin"),
      args = path.join(base, "docker-args");
    fs.mkdirSync(bin);
    fs.writeFileSync(
      path.join(bin, "docker"),
      '#!/bin/sh\nprintf "%s\\n" "$@" > "$UPPR_TEST_DOCKER_ARGS"\n',
      { mode: 0o700 },
    );
    const result = spawnSync(
      "bun",
      [path.resolve(import.meta.dir, "cli.ts"), "run-app", root, "api"],
      {
        cwd: root,
        encoding: "utf8",
        env: {
          ...process.env,
          PATH: `${bin}:${process.env.PATH}`,
          UPPR_TEST_DOCKER_ARGS: args,
        },
      },
    );
    expect(result.status).toBe(0);
    expect(fs.readFileSync(args, "utf8")).toContain("127.0.0.1:20000:8840");
    expect(fs.readFileSync(args, "utf8")).toContain("EGATE_PORT=8840");
    fs.writeFileSync(path.join(repo, "config", ".env"), "");
    const fallback = spawnSync(
      "bun",
      [path.resolve(import.meta.dir, "cli.ts"), "run-app", root, "api"],
      {
        cwd: root,
        encoding: "utf8",
        env: {
          ...process.env,
          PATH: `${bin}:${process.env.PATH}`,
          UPPR_TEST_DOCKER_ARGS: args,
        },
      },
    );
    expect(fallback.status).toBe(0);
    expect(fs.readFileSync(args, "utf8")).toContain("127.0.0.1:20000:8839");
    expect(fs.readFileSync(args, "utf8")).toContain("EGATE_PORT=8839");
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
});

test("pull --all updates all checkouts and pull <name> updates only one", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-pull-test-"));
  const root = path.join(base, "pad");
  const git = (cwd: string, ...args: string[]) => {
    const result = spawnSync("git", args, {
      cwd,
      encoding: "utf8",
      env: {
        ...process.env,
        GIT_AUTHOR_NAME: "Uppr Test",
        GIT_AUTHOR_EMAIL: "uppr@example.test",
        GIT_COMMITTER_NAME: "Uppr Test",
        GIT_COMMITTER_EMAIL: "uppr@example.test",
      },
    });
    expect(result.status).toBe(0);
    return result.stdout.trim();
  };
  try {
    expect(cli(base, "init", root).status).toBe(0);
    for (const name of ["one", "two"]) {
      const source = path.join(base, `${name}-source`);
      fs.mkdirSync(source);
      git(source, "init", "-b", "main");
      fs.writeFileSync(path.join(source, "version.txt"), "first\n");
      git(source, "add", "version.txt");
      git(source, "commit", "-m", "first");
      const remote = path.join(base, `${name}.git`);
      git(base, "clone", "--bare", source, remote);
      git(source, "remote", "add", "origin", remote);
      expect(cli(root, "add", remote, "--name", name, "--domain", `${name}.example.com`).status).toBe(0);
    }
    expect(cli(root, "pull", "--all").status).toBe(0);
    for (const name of ["one", "two"]) {
      const source = path.join(base, `${name}-source`);
      fs.writeFileSync(path.join(source, "version.txt"), "second\n");
      git(source, "commit", "-am", "second");
      git(source, "push", "origin", "main");
    }
    expect(cli(root, "pull", "one").status).toBe(0);
    expect(
      fs.readFileSync(path.join(root, "repos", "one", "version.txt"), "utf8"),
    ).toBe("second\n");
    expect(
      fs.readFileSync(path.join(root, "repos", "two", "version.txt"), "utf8"),
    ).toBe("first\n");
    const checkout = path.join(root, "repos", "one");
    fs.writeFileSync(path.join(checkout, "version.txt"), "local commit\n");
    git(checkout, "commit", "-am", "local commit");
    fs.writeFileSync(path.join(checkout, "version.txt"), "uncommitted edit\n");
    fs.writeFileSync(path.join(checkout, "scratch.txt"), "remove me\n");
    fs.mkdirSync(path.join(checkout, "config"));
    fs.writeFileSync(path.join(checkout, "config", ".env"), "SECRET=keep\n");
    fs.writeFileSync(path.join(checkout, "data", "main.sqlite"), "keep data\n");
    const source = path.join(base, "one-source");
    fs.writeFileSync(path.join(source, "version.txt"), "third\n");
    git(source, "commit", "-am", "third");
    git(source, "push", "origin", "main");
    expect(cli(root, "pull", "one").status).not.toBe(0);
    expect(cli(root, "pull", "one", "--ff").status).toBe(0);
    expect(git(checkout, "rev-parse", "HEAD")).toBe(git(source, "rev-parse", "HEAD"));
    expect(fs.readFileSync(path.join(checkout, "version.txt"), "utf8")).toBe("third\n");
    expect(fs.existsSync(path.join(checkout, "scratch.txt"))).toBe(false);
    expect(fs.readFileSync(path.join(checkout, "config", ".env"), "utf8")).toBe("SECRET=keep\n");
    expect(fs.readFileSync(path.join(checkout, "data", "main.sqlite"), "utf8")).toBe("keep data\n");
    expect(cli(root, "pull", "--ff", "--all").status).toBe(0);
    expect(fs.readFileSync(path.join(root, "repos", "two", "version.txt"), "utf8")).toBe("second\n");
    expect(cli(root, "pull", "one", "--unknown").status).not.toBe(0);
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
});
