import { spawn } from "node:child_process";
import path from "node:path";

export type RestartJob = {
  state: "restarting" | "succeeded" | "failed";
  action: string;
  lines: string[];
  message?: string;
};

const jobs = new Map<string, RestartJob>();
const listeners = new Set<() => void>();
const key = (root: string, name: string) => `${root}\0${name}`;
const notify = () => listeners.forEach((listener) => listener());

export function subscribeRestartJobs(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function restartJob(root: string, name: string): RestartJob | undefined {
  return jobs.get(key(root, name));
}

export function startOperation(
  root: string,
  name: string,
  action: string,
  args: string[],
  unit?: string,
  entry = path.resolve(process.argv[1]),
) {
  const id = key(root, name);
  if (jobs.get(id)?.state === "restarting") return false;
  const job: RestartJob = { state: "restarting", action, lines: [] };
  jobs.set(id, job);
  notify();
  // Use the current entry point so development and installed bundles behave alike.
  const child = spawn(process.execPath, [entry, ...args], {
    cwd: root,
    env: { ...process.env, FORCE_COLOR: "0", UPPR_NONINTERACTIVE: "1" },
    stdio: ["ignore", "pipe", "pipe"],
  });
  const capture = (stream: NodeJS.ReadableStream) => {
    let partial = "";
    stream.on("data", (data: Buffer) => {
      if (job.state !== "restarting") return;
      partial += data.toString("utf8");
      const parts = partial.split(/\r?\n|\r/);
      partial = parts.pop() || "";
      for (const line of parts) {
        job.lines.push(line.slice(0, 1000));
      }
      while (partial.length > 1000) {
        job.lines.push(partial.slice(0, 1000));
        partial = partial.slice(1000);
      }
      if (job.lines.length > 200) job.lines.splice(0, job.lines.length - 200);
      notify();
    });
    stream.on("end", () => {
      if (job.state !== "restarting") return;
      if (partial) job.lines.push(partial.slice(0, 1000));
      if (job.lines.length > 200) job.lines.splice(0, job.lines.length - 200);
      notify();
    });
  };
  capture(child.stdout);
  capture(child.stderr);
  // The app process writes to its systemd journal. Follow only new entries
  // while this restart is active; keep the same small, transient buffer.
  const journal =
    unit && process.platform === "linux"
      ? spawn(
          "journalctl",
          ["--follow", "--lines=0", "--output=cat", "--unit", unit],
          {
            cwd: root,
            stdio: ["ignore", "pipe", "pipe"],
          },
        )
      : undefined;
  if (journal) {
    capture(journal.stdout);
    journal.on("error", () => {});
  }
  let error: Error | undefined;
  child.on("error", (cause) => {
    error = cause;
  });
  child.on("close", (code) => {
    journal?.kill();
    job.state = code === 0 ? "succeeded" : "failed";
    job.message =
      error?.message ||
      (code === 0
        ? `${action} complete`
        : `${action} exited ${code ?? "unexpectedly"}`);
    notify();
  });
  return true;
}

export function startRestart(
  root: string,
  name: string,
  unit?: string,
  entry = path.resolve(process.argv[1]),
) {
  return startOperation(root, name, "Restart", ["restart", name], unit, entry);
}
