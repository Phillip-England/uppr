import { test, expect } from "bun:test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

function cli(cwd: string, ...args: string[]) {
  return spawnSync("bun", [path.resolve(import.meta.dir, "cli.tsx"), ...args], {
    cwd,
    encoding: "utf8",
  });
}
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
      cli(root, "add", "https://example.com/api.git", "--name", "api").status,
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
      [path.resolve(import.meta.dir, "cli.tsx"), "schema", "edit", "api"],
      { cwd: root, env: { ...process.env, VISUAL: editor, EDITOR: editor }, encoding: "utf8" },
    );
    expect(result.status).toBe(1);
    expect(fs.readFileSync(schema, "utf8")).toBe('{"variables": []}\n');
  } finally {
    fs.rmSync(base, { recursive: true, force: true });
  }
});
