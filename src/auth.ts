import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export function credentialPath() {
  return path.join(os.homedir(), ".config", "uppr", "credentials.json");
}

export function gitToken() {
  const file = credentialPath();
  return fs.existsSync(file)
    ? (JSON.parse(fs.readFileSync(file, "utf8")).token as string)
    : "";
}

export function saveGitToken(token: string) {
  if (!token.trim()) throw new Error("token cannot be empty");
  const file = credentialPath();
  fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  fs.writeFileSync(file, JSON.stringify({ token: token.trim() }), { mode: 0o600 });
  fs.chmodSync(file, 0o600);
}

export function removeGitToken() {
  fs.rmSync(credentialPath(), { force: true });
}
