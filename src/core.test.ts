import { test, expect } from "bun:test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  findPad,
  freePort,
  globalCaddy,
  initPad,
  loadManifest,
  readSchema,
  renderCaddy,
  renderUnit,
  saveManifest,
  saveSchema,
  select,
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
    expect(renderCaddy(m)).toContain("reverse_proxy 127.0.0.1:20000");
    expect(unitName(m, r)).toContain("api.service");
    expect(
      renderUnit(root, "/usr/bin/bun", "/opt/uppr/uppr.js", "deploy", m, r),
    ).toContain("ExecStart=/usr/bin/bun /opt/uppr/uppr.js run-app");
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
