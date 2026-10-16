import { test, expect } from "bun:test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  appSetup,
  containerPort,
  findPad,
  freePort,
  globalCaddy,
  initPad,
  loadManifest,
  readSchema,
  requireSinglePad,
  renderCaddy,
  renderUnit,
  saveRepoEnv,
  saveManifest,
  saveSchema,
  select,
  starterSchema,
  unitName,
  validDomain,
} from "./core.ts";

function fixture() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-test-"));
  const root = initPad(path.join(dir, "pad"));
  return {
    dir,
    root,
    m: loadManifest(root),
    cleanup: () => fs.rmSync(dir, { recursive: true, force: true }),
  };
}
test("launchpad persists, selects apps, and maps routes", () => {
  const f = fixture();
  try {
    const r = {
      name: "api",
      url: "https://example.com/api.git",
      domain: "api.example.com",
      host_port: 20000,
      container_port: 3000,
    };
    f.m.repos.push(r);
    saveManifest(f.root, f.m);
    const [root, m] = findPad(path.join(f.root, "repos"));
    expect(root).toBe(f.root);
    expect(select(m, "api")[0]).toEqual(r);
    expect(freePort(m)).toBe(20001);
    expect(renderCaddy(root, m)).toContain("reverse_proxy 127.0.0.1:20000");
    expect(unitName(m, r)).toContain("api.service");
    expect(
      renderUnit(root, "/usr/bin/bun", "/opt/uppr/uppr.js", "deploy", m, r),
    ).toContain("ExecStart=/usr/bin/bun /opt/uppr/uppr.js run-app");
  } finally {
    f.cleanup();
  }
});
test("only the registered launchpad can be installed again", () => {
  const a = fixture(), b = fixture();
  try {
    const pads = { [a.m.id]: { id: a.m.id, root: a.root } };
    expect(() => requireSinglePad(a.root, a.m, pads)).not.toThrow();
    expect(() => requireSinglePad(b.root, b.m, pads)).toThrow(/uppr uninstall/);
    expect(() => requireSinglePad(b.root, a.m, pads)).toThrow(/uppr uninstall/);
  } finally {
    a.cleanup();
    b.cleanup();
  }
});
test("schema PORT controls the published container port", () => {
  const f = fixture();
  try {
    const r = {
      name: "api",
      url: "https://example.com/api.git",
      domain: "api.example.com",
      host_port: 20000,
      container_port: 3000,
    };
    f.m.repos.push(r);
    saveManifest(f.root, f.m);
    const repo = path.join(f.root, "repos", r.name);
    fs.mkdirSync(path.join(repo, "config"), { recursive: true });
    saveSchema(path.join(repo, "schema.json"), {
      variables: [{ name: "PORT", required: false, example: "8839" }],
    });
    expect(containerPort(f.root, r)).toBe(8839);
    fs.writeFileSync(path.join(repo, "config", ".env"), "PORT=8840\n");
    expect(containerPort(f.root, r)).toBe(8840);
    expect(renderCaddy(f.root, f.m)).toContain("reverse_proxy 127.0.0.1:20000");
    fs.writeFileSync(path.join(repo, "config", ".env"), "PORT=wrong\n");
    expect(() => containerPort(f.root, r)).toThrow(/PORT must be an integer/);
  } finally {
    f.cleanup();
  }
});
test("explicit schema port binds a custom listen variable and catches missing mapping", () => {
  const f = fixture();
  try {
    const r = {
      name: "egate", url: "https://example.com/egate.git",
      domain: "egate.example.com", host_port: 20000, container_port: 3000,
    };
    f.m.repos.push(r);
    const repo = path.join(f.root, "repos", r.name);
    fs.mkdirSync(path.join(repo, "config"), { recursive: true });
    fs.writeFileSync(path.join(repo, "Dockerfile"), "FROM scratch\n");
    const file = path.join(repo, "schema.json");
    saveSchema(file, { variables: [{ name: "EGATE_PORT", example: "8119", required: false }] });
    expect(() => containerPort(f.root, r)).toThrow(/schema.json must declare "port"/);
    saveSchema(file, {
      port: { variable: "EGATE_PORT" },
      variables: [{ name: "EGATE_PORT", example: "8119", required: false }],
    });
    expect(containerPort(f.root, r)).toBe(8119);
    fs.writeFileSync(path.join(repo, "config", ".env"), "EGATE_PORT=8120\n");
    expect(containerPort(f.root, r)).toBe(8120);
    expect(() => saveRepoEnv(f.root, r, { EGATE_PORT: "invalid" })).toThrow(/EGATE_PORT must be an integer/);
    expect(containerPort(f.root, r)).toBe(8120);
    expect(() => saveSchema(file, { port: { variable: "MISSING" }, variables: [] })).toThrow(/schema port/);
    expect(() => saveSchema(file, {
      port: { variable: "EGATE_PORT" }, variables: [{ name: "EGATE_PORT", example: "bad" }],
    })).toThrow(/schema port example/);
  } finally { f.cleanup(); }
});
test("schema generation discovers a single custom port variable", () => {
  const f = fixture();
  try {
    const r = {
      name: "egate", url: "https://example.com/egate.git",
      domain: "egate.example.com", host_port: 20000, container_port: 3000,
    };
    const repo = path.join(f.root, "repos", r.name);
    fs.mkdirSync(repo, { recursive: true });
    fs.writeFileSync(path.join(repo, ".env.example"), "EGATE_PORT=8119\nEGATE_KEY=replace-me\n");
    const schema = starterSchema(f.root, r);
    expect(schema.port).toEqual({ variable: "EGATE_PORT" });
    expect(schema.variables.find((v) => v.name === "EGATE_PORT")).toMatchObject({
      example: "8119", required: false,
    });
    saveSchema(path.join(repo, "schema.json"), schema);
    expect(containerPort(f.root, r)).toBe(8119);
  } finally { f.cleanup(); }
});
test("schema domains and rate limit drive Caddy routes with safe defaults", () => {
  const f = fixture();
  try {
    const r = {
      name: "api", url: "https://example.com/api.git", domain: "api.localhost",
      host_port: 20000, container_port: 3000,
    };
    f.m.repos.push(r);
    saveManifest(f.root, f.m);
    const repo = path.join(f.root, "repos", r.name);
    fs.mkdirSync(repo);
    const file = path.join(repo, "schema.json");
    saveSchema(file, { variables: [], domains: ["api.example.com", "alt.example.com"] });
    let caddy = renderCaddy(f.root, f.m);
    expect(caddy).toContain("api.example.com {");
    expect(caddy).toContain("alt.example.com {");
    expect(caddy).not.toContain("api.localhost {");
    expect(caddy).toContain(`zone uppr-${f.m.id}-api-dynamic {`);
    expect(caddy).toContain("key {remote_host}");
    expect(caddy).toContain("events 100");
    expect(caddy).toContain("window 1m");
    saveSchema(file, { variables: [], domains: ["api.example.com"], rate_limit: { events: 25, window: "10s", zone: "api" } });
    caddy = globalCaddy({ [f.m.id]: { id: f.m.id, root: f.root } });
    expect(caddy).toContain(`zone uppr-${f.m.id}-api-api {`);
    expect(caddy).toContain("events 25");
    expect(caddy).toContain("window 10s");
    expect(() => saveSchema(file, { variables: [], domains: ["bad domain"] })).toThrow();
    expect(() => saveSchema(file, { variables: [], rate_limit: { events: 0 } })).toThrow();
  } finally { f.cleanup(); }
});
test("schema env setup preserves existing entries and protects secrets", () => {
  const f = fixture();
  try {
    const r = {
      name: "api",
      url: "https://example.com/api.git",
      domain: "api.example.com",
      host_port: 20000,
      container_port: 3000,
    };
    const repo = path.join(f.root, "repos", r.name);
    fs.mkdirSync(repo);
    fs.writeFileSync(path.join(repo, "Dockerfile"), "FROM scratch\n");
    saveSchema(path.join(repo, "schema.json"), {
      variables: [
        { name: "API_KEY", required: true },
        { name: "LABEL", required: false },
      ],
    });
    const env = path.join(repo, "config", ".env");
    expect(() => saveRepoEnv(f.root, r, { LABEL: "staging" })).toThrow(
      /API_KEY/,
    );
    expect(fs.existsSync(env)).toBe(false);
    saveRepoEnv(f.root, r, { API_KEY: "secret", LABEL: "staging" });
    expect(fs.readFileSync(env, "utf8")).toContain(
      "API_KEY=secret\nLABEL=staging\n",
    );
    expect(fs.statSync(env).mode & 0o777).toBe(0o600);
    fs.appendFileSync(env, "CUSTOM=keep\n");
    saveRepoEnv(f.root, r, { LABEL: "production" });
    expect(fs.readFileSync(env, "utf8")).toContain("CUSTOM=keep\n");
    expect(fs.readFileSync(env, "utf8")).toContain("LABEL=production\n");
    expect(fs.readFileSync(env, "utf8")).toContain("API_KEY=secret\n");
    expect(() => saveRepoEnv(f.root, r, { LABEL: "bad\nvalue" })).toThrow(
      /control character/,
    );
  } finally {
    f.cleanup();
  }
});
test("setup guidance advances from checkout through environment", () => {
  const f = fixture();
  try {
    const r = {
      name: "api",
      url: "https://example.com/api.git",
      domain: "api.example.com",
      host_port: 20000,
      container_port: 3000,
    };
    const status = {
      state: "not installed",
      service: "not installed",
      container: "—",
      health: "—",
    };
    expect(appSetup(f.root, r, status).action).toBe("pull");
    const repo = path.join(f.root, "repos", r.name);
    fs.mkdirSync(repo);
    expect(appSetup(f.root, r, status).label).toBe("Needs Dockerfile");
    fs.writeFileSync(path.join(repo, "Dockerfile"), "FROM scratch\n");
    expect(appSetup(f.root, r, status).action).toBe("schema-generate");
    saveSchema(path.join(repo, "schema.json"), {
      variables: [{ name: "API_KEY" }],
    });
    expect(appSetup(f.root, r, status).action).toBe("domains");
    saveSchema(path.join(repo, "schema.json"), {
      domains: ["api.example.com"],
      variables: [{ name: "API_KEY" }],
    });
    expect(appSetup(f.root, r, status).action).toBe("env-setup");
    saveRepoEnv(f.root, r, { API_KEY: "secret" });
    expect(appSetup(f.root, r, status).action).toBe(
      process.platform === "linux" ? "install" : undefined,
    );
  } finally {
    f.cleanup();
  }
});
test("schema validation rejects invalid and duplicate names", () => {
  const f = fixture();
  try {
    const p = path.join(f.root, "schema.json");
    saveSchema(p, { variables: [{ name: "API_KEY", required: true }] });
    expect(readSchema(p).variables[0].name).toBe("API_KEY");
    expect(() =>
      saveSchema(p, { variables: [{ name: "API_KEY" }, { name: "API_KEY" }] }),
    ).toThrow();
    expect(() =>
      saveSchema(p, { variables: [{ name: "not-valid" }] }),
    ).toThrow();
  } finally {
    f.cleanup();
  }
});
test("global routes reject cross-pad conflicts", () => {
  const a = fixture(),
    b = fixture();
  try {
    a.m.repos.push({
      name: "a",
      url: "a",
      domain: "same.example.com",
      host_port: 20000,
      container_port: 3000,
    });
    b.m.repos.push({
      name: "b",
      url: "b",
      domain: "same.example.com",
      host_port: 20001,
      container_port: 3000,
    });
    saveManifest(a.root, a.m);
    saveManifest(b.root, b.m);
    expect(() =>
      globalCaddy({
        [a.m.id]: { id: a.m.id, root: a.root },
        [b.m.id]: { id: b.m.id, root: b.root },
      }),
    ).toThrow(/domain/);
  } finally {
    a.cleanup();
    b.cleanup();
  }
});
