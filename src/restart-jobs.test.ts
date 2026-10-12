import { test, expect } from "bun:test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  restartJob,
  startRestart,
  subscribeRestartJobs,
} from "./restart-jobs.ts";

test("restart output is observable only while its job is active", async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "uppr-restart-test-"));
  const entry = path.join(root, "restart.ts");
  fs.writeFileSync(
    entry,
    'console.log("build started"); await Bun.sleep(150); console.log("ready");\n',
  );
  try {
    expect(startRestart(root, "api", undefined, entry)).toBe(true);
    expect(startRestart(root, "api", undefined, entry)).toBe(false);
    expect(restartJob(root, "api")?.state).toBe("restarting");
    await new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => {
        unsubscribe();
        reject(new Error("restart timed out"));
      }, 5000);
      const unsubscribe = subscribeRestartJobs(() => {
        if (restartJob(root, "api")?.lines.includes("build started")) {
          clearTimeout(timeout);
          unsubscribe();
          resolve();
        }
      });
    });
    expect(restartJob(root, "api")?.lines).toContain("build started");
    if (restartJob(root, "api")?.state === "restarting") {
      await new Promise<void>((resolve, reject) => {
        const timeout = setTimeout(() => {
          unsubscribe();
          reject(new Error("restart timed out"));
        }, 5000);
        const unsubscribe = subscribeRestartJobs(() => {
          if (restartJob(root, "api")?.state !== "restarting") {
            clearTimeout(timeout);
            unsubscribe();
            resolve();
          }
        });
      });
    }
    expect(restartJob(root, "api")?.state).toBe("succeeded");
    expect(restartJob(root, "api")?.lines).toEqual([]);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
