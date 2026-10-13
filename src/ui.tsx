import React, { useEffect, useMemo, useState } from "react";
import fs from "node:fs";
import path from "node:path";
import { Box, Text, useApp, useInput, render, renderToString } from "ink";
import { restartJob, subscribeRestartJobs } from "./restart-jobs.ts";
import { gitToken, removeGitToken, saveGitToken } from "./auth.ts";
import { dependencies } from "./deps.ts";
import {
  appStatus,
  appSetup,
  appRouting,
  containerPort,
  defaultRateLimit,
  findPad,
  probe,
  repoDir,
  readEnv,
  readSchema,
  saveRepoEnv,
  safeName,
  saveSchema,
  schemaPath,
  sorted,
  starterSchema,
  validDomain,
  validPort,
  which,
  type Manifest,
  type Repo,
  type Variable,
  type SetupStep,
  type Schema,
} from "./core.ts";

type Entry = {
  repo: Repo;
  status: ReturnType<typeof appStatus>;
  setup: SetupStep;
};
function displayedContainerPort(root: string, r: Repo) {
  try {
    return String(containerPort(root, r));
  } catch {
    return "?";
  }
}
const C = {
  purple: "#B598F2",
  cyan: "#6EE7DD",
  green: "#8CE9A2",
  red: "#F28F9B",
  muted: "#8992A7",
  white: "#F5F0E8",
  yellow: "#F4D18A",
};
const actionLabels: Record<string, string> = {
  pull: "pull repository",
  "schema-generate": "generate schema",
  "schema-edit": "edit schema",
  "env-setup": "configure environment",
  install: "install services",
  serve: "launch app",
  logs: "view logs",
};
function pendingChanges(root: string, r: Repo) {
  const dir = repoDir(root, r);
  return fs.existsSync(path.join(dir, ".git")) &&
    !!probe("git", ["status", "--porcelain"], dir).out;
}
function displayDomains(root: string, r: Repo) {
  try { return appRouting(root, r).domains; }
  catch { return [r.domain]; }
}
function Badge({ state }: { state: string }) {
  const color =
    state === "running"
      ? C.green
      : state === "stopped" || state === "not installed"
        ? C.muted
        : state === "unhealthy" || state === "failed"
          ? C.red
          : C.yellow;
  return (
    <Text color={color} bold>
      {state === "running" ? "●" : state === "stopped" ? "○" : "◆"} {state}
    </Text>
  );
}
function Board({
  root,
  m,
  entries,
  selected = -1,
  interactive = false,
  notice,
}: {
  root: string;
  m: Manifest;
  entries: Entry[];
  selected?: number;
  interactive?: boolean;
  notice?: string;
}) {
  const running = entries.filter((e) => e.status.state === "running").length;
  const active = entries[selected];
  return (
    <Box flexDirection="column" paddingX={1}>
      <Box
        borderStyle="round"
        borderColor={C.purple}
        paddingX={1}
        marginBottom={1}
      >
        <Box width={20}>
          <Text color={C.purple} bold>
            ◈ UPPR
          </Text>
        </Box>
        <Text color={C.muted}>{m.id} </Text>
        <Text color={C.cyan} bold>
          {running}/{entries.length} running
        </Text>
      </Box>
      <Text color={C.muted}>Launchpad · {path.basename(root)}</Text>
      <Box
        flexDirection="column"
        borderStyle="round"
        borderColor={C.muted}
        paddingX={1}
        marginTop={1}
      >
        <Box marginBottom={1}>
          <Box width={22}>
            <Text color={C.muted}>APP</Text>
          </Box>
          <Box width={17}>
            <Text color={C.muted}>STATE</Text>
          </Box>
          <Text color={C.muted}>BROWSER URL</Text>
        </Box>
        {entries.length === 0 ? (
          <Text color={C.muted}>
            {interactive
              ? "No apps yet. Press a to add a repository."
              : "No apps yet. Run uppr to add a repository."}
          </Text>
        ) : (
          entries.map(({ repo: r, status: s, setup }, i) => (
            <Box key={r.name} flexDirection="column">
              <Box>
                <Box width={22}>
                  <Text color={selected === i ? C.cyan : C.muted}>
                    {selected === i ? "❯ " : "  "}
                  </Text>
                  <Text color={C.white} bold={selected === i}>
                    {r.name}
                  </Text>
                </Box>
                <Box width={17}>
                  <Badge
                    state={
                      interactive &&
                      restartJob(root, r.name)?.state === "restarting"
                        ? `${restartJob(root, r.name)?.action}ing`
                        : s.state
                    }
                  />
                </Box>
                <Text
                  color={s.state === "running" ? C.green : C.muted}
                  bold={s.state === "running"}
                >
                  https://{displayDomains(root, r)[0]}
                </Text>
              </Box>
              {s.state !== "running" && (
                <Text color={setup.action ? C.yellow : C.muted}>
                  {" "}
                  Setup: {setup.label}
                </Text>
              )}
              {!interactive && (
                <Text color={C.muted}>
                  {" "}
                  Internal: 127.0.0.1:{r.host_port} → container :
                  {displayedContainerPort(root, r)} · Files: {repoDir(root, r)}
                </Text>
              )}
            </Box>
          ))
        )}
      </Box>
      {active && (
        <Box
          flexDirection="column"
          borderStyle="round"
          borderColor={C.cyan}
          paddingX={1}
        >
          <Text color={C.muted}>
            {active.repo.name} ·{" "}
            {active.status.state === "running"
              ? "OPEN IN BROWSER"
              : "CONFIGURED BROWSER URL"}
          </Text>
          <Text
            color={active.status.state === "running" ? C.green : C.white}
            bold
          >
            https://{displayDomains(root, active.repo)[0]}
          </Text>
          {active.status.state !== "running" && (
            <Text color={active.setup.action ? C.yellow : C.muted}>
              {active.setup.label} · {active.setup.detail}
            </Text>
          )}
          <Text color={C.muted}>
            Internal: 127.0.0.1:{active.repo.host_port} → container :
            {displayedContainerPort(root, active.repo)}
          </Text>
          {active.status.health !== "—" && (
            <Text color={C.muted}>
              Health {active.status.health} · service {active.status.service}
            </Text>
          )}
          {interactive && active.setup.action && (
            <Text color={C.cyan}>
              Enter → {actionLabels[active.setup.action]}
            </Text>
          )}
          {interactive && displayDomains(root, active.repo).length > 1 && (
            <Text color={C.muted}>
              +{displayDomains(root, active.repo).length - 1} more domains
            </Text>
          )}
          {interactive && pendingChanges(root, active.repo) && (
            <Text color={C.yellow}>Local Git changes · w push repository</Text>
          )}
          {interactive && restartJob(root, active.repo.name) && (
            <Text color={C.cyan}>
              o → observe {restartJob(root, active.repo.name)?.action} output
            </Text>
          )}
          {interactive &&
            restartJob(root, active.repo.name)?.state === "failed" && (
              <Text color={C.red}>
                {restartJob(root, active.repo.name)?.message}. Press o for
                output.
              </Text>
            )}
        </Box>
      )}
      {interactive && notice && <Text color={C.yellow}>{notice}</Text>}
      {interactive && restartJob(root, "--all") && (
        <Text color={C.cyan}>
          {restartJob(root, "--all")?.action} all:{" "}
          {restartJob(root, "--all")?.state === "restarting"
            ? "running"
            : restartJob(root, "--all")?.state}{" "}
          · o view output
        </Text>
      )}
      {interactive && (
        <Box flexDirection="column" marginTop={1}>
          <Text color={C.muted}>
            ↑↓ choose · Enter next · a add · p pull · P pull all · A launch all
            · D host setup · ? help
          </Text>
          <Text color={C.muted}>
            s launch · r restart · o observe · u curl · v Caddyfile · x stop · l
            logs · c env · d routes · w push · t settings · i install · q quit
          </Text>
        </Box>
      )}
    </Box>
  );
}
function Dashboard({
  root,
  m,
  onAction,
}: {
  root: string;
  m: Manifest;
  onAction: (action: string, name?: string) => void;
}) {
  const { exit } = useApp();
  const [selected, setSelected] = useState(0),
    [tick, setTick] = useState(0),
    [, setJobsRevision] = useState(0),
    [notice, setNotice] = useState("");
  useEffect(() => {
    const id = setInterval(() => setTick((x) => x + 1), 3000);
    const unsubscribe = subscribeRestartJobs(() =>
      setJobsRevision((x) => x + 1),
    );
    return () => {
      clearInterval(id);
      unsubscribe();
    };
  }, []);
  const entries = useMemo(
    () =>
      sorted(m).map((repo) => {
        const status = appStatus(m, repo);
        return { repo, status, setup: appSetup(root, repo, status) };
      }),
    [root, m, tick],
  );
  useInput((input, key) => {
    if (input === "q" || key.escape) {
      exit();
      return;
    }
    if (key.upArrow || input === "k") setSelected((x) => Math.max(0, x - 1));
    if (key.downArrow || input === "j")
      setSelected((x) => Math.min(entries.length - 1, x + 1));
    if (["a", "i", "?", "P", "A", "v", "t", "D"].includes(input)) {
      onAction(
        (
          {
            a: "add",
            i: "install",
            "?": "help",
            P: "pull-all",
            A: "launch-all",
            v: "caddyfile",
            t: "settings",
            D: "dependencies",
          } as Record<string, string>
        )[input],
      );
      exit();
      return;
    }
    if (!entries.length) return;
    const entry = entries[Math.min(selected, entries.length - 1)];
    const name = entry.repo.name;
    if (input === "o") {
      const target =
        restartJob(root, name)?.state === "restarting"
          ? name
          : restartJob(root, "--all")?.state === "restarting"
            ? "--all"
            : restartJob(root, name)
              ? name
              : restartJob(root, "--all")
                ? "--all"
                : undefined;
      if (!target) {
        setNotice(`${name} has no operation output to observe.`);
        return;
      }
      onAction("observe", target);
      exit();
      return;
    }
    setNotice("");
    const actions: Record<string, string> = {
      s: "serve",
      p: "pull",
      r: "restart",
      x: "stop",
      l: "logs",
      u: "curl",
      c: "env-setup",
      e: "schema-edit",
      g: "schema-generate",
      d: "routing",
      w: "push",
    };
    const action = key.return ? entry.setup.action : actions[input];
    if (action) {
      onAction(action, name);
      exit();
    }
  });
  return (
    <Board
      root={root}
      m={m}
      entries={entries}
      selected={selected}
      interactive
      notice={notice}
    />
  );
}
function Observe({ root, name }: { root: string; name: string }) {
  const { exit } = useApp();
  const [, setTick] = useState(0);
  useEffect(() => subscribeRestartJobs(() => setTick((x) => x + 1)), []);
  useInput((input, key) => {
    if (input === "q" || key.escape) exit();
  });
  const job = restartJob(root, name);
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>
        ◈ UPPR / observe {name}
      </Text>
      <Text color={C.muted}>
        {job?.action || "Operation"}{" "}
        {job?.state === "restarting" ? "running" : job?.state || "complete"} ·
        q/Esc dashboard
      </Text>
      {job?.lines.length ? (
        job.lines
          .slice(-Math.max(1, (process.stdout.rows || 30) - 5))
          .map((line, i) => (
            <Text key={i} color={C.white}>
              {line || " "}
            </Text>
          ))
      ) : (
        <Text color={job?.state === "failed" ? C.red : C.green}>
          {job?.message || "Waiting for output…"}
        </Text>
      )}
    </Box>
  );
}
export async function showObserve(name: string) {
  const [root] = findPad();
  const app = render(<Observe root={root} name={name} />);
  await app.waitUntilExit();
}
function EnvSetup({
  root,
  repo,
  variables,
  initial,
  onSaved,
}: {
  root: string;
  repo: Repo;
  variables: Variable[];
  initial: Record<string, string>;
  onSaved: () => void;
}) {
  const { exit } = useApp();
  const [selected, setSelected] = useState(
    Math.max(
      0,
      variables.findIndex(
        (v) => v.required !== false && !initial[v.name]?.trim(),
      ),
    ),
  );
  const [updates, setUpdates] = useState<Record<string, string>>({});
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [message, setMessage] = useState("");
  const current = variables[selected];
  const value = (v: Variable) => updates[v.name] ?? initial[v.name] ?? "";
  const first = Math.max(0, Math.min(selected - 3, variables.length - 8));
  const visible = variables.slice(first, first + 8);
  useInput((input, key) => {
    if (editing && current) {
      if (key.escape) {
        setEditing(false);
        setDraft("");
      } else if (key.return) {
        if (/[\r\n\0]/.test(draft)) {
          setMessage("Values must fit on one line.");
          return;
        }
        setUpdates((old) => ({ ...old, [current.name]: draft }));
        setEditing(false);
        setDraft("");
        setMessage("");
      } else if (key.backspace || key.delete) {
        setDraft((old) => old.slice(0, -1));
      } else if (key.ctrl && input === "u") {
        setDraft("");
      } else if (!key.ctrl && !key.meta && input) {
        setDraft((old) => old + input);
      }
      return;
    }
    if (input === "q" || key.escape) return exit();
    if (key.upArrow || input === "k") setSelected((x) => Math.max(0, x - 1));
    if (key.downArrow || input === "j")
      setSelected((x) => Math.min(variables.length - 1, x + 1));
    if ((key.return || input === "e") && current) {
      setDraft(value(current));
      setEditing(true);
      setMessage("");
    }
    if (input === "s") {
      try {
        saveRepoEnv(root, repo, updates);
        onSaved();
        exit();
      } catch (error) {
        setMessage(error instanceof Error ? error.message : String(error));
      }
    }
  });
  const missing = variables.filter(
    (v) => v.required !== false && !value(v).trim(),
  );
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>
        ◈ UPPR / {repo.name} / environment
      </Text>
      <Text color={C.muted}>
        {path.join(repoDir(root, repo), "config", ".env")}
      </Text>
      <Box
        flexDirection="column"
        borderStyle="round"
        borderColor={C.purple}
        paddingX={1}
        marginTop={1}
      >
        {variables.length === 0 ? (
          <Text color={C.muted}>
            No variables in schema.json. Press s to create the env file.
          </Text>
        ) : (
          visible.map((v, i) => (
            <Box key={v.name}>
              <Text
                color={i + first === selected ? C.cyan : C.white}
                bold={i + first === selected}
              >
                {i + first === selected ? "❯ " : "  "}
                {v.name} {v.required === false ? "(optional)" : "(required)"} ·{" "}
                {value(v) ? "set" : "empty"}
                {updates[v.name] !== undefined ? " · changed" : ""}
              </Text>
            </Box>
          ))
        )}
      </Box>
      {variables.length > 8 && (
        <Text color={C.muted}>
          Showing {first + 1}–{first + visible.length} of {variables.length}{" "}
          variables
        </Text>
      )}
      {current && (
        <Box flexDirection="column" marginTop={1}>
          <Text color={C.white} bold>
            {current.name}
          </Text>
          <Text color={C.muted}>
            {current.description || "No description in schema.json."}
          </Text>
          {current.example && (
            <Text color={C.muted}>Example: {current.example}</Text>
          )}
        </Box>
      )}
      {editing && current ? (
        <Box flexDirection="column" marginTop={1}>
          <Text color={C.cyan}>
            Enter {current.name} (Enter set · Ctrl+U clear · Esc cancel):
          </Text>
          <Text color={C.white}>
            {/KEY|SECRET|TOKEN|PASS|CREDENTIAL|PRIVATE|AUTH/i.test(current.name)
              ? "•".repeat(draft.length)
              : draft}
            ▌
          </Text>
        </Box>
      ) : (
        <Text color={C.muted}>
          ↑↓ select · Enter edit · s save · q/Esc cancel · {missing.length}{" "}
          required missing
        </Text>
      )}
      {message && <Text color={C.red}>{message}</Text>}
    </Box>
  );
}
export async function showEnvSetup(name: string) {
  const [root, m] = findPad();
  const repo = m.repos.find((r) => r.name === name);
  if (!repo) throw new Error(`unknown repo ${name}`);
  const variables = readSchema(schemaPath(root, repo)).variables;
  const env = path.join(repoDir(root, repo), "config", ".env");
  const initial = fs.existsSync(env)
    ? readEnv(fs.readFileSync(env, "utf8"))
    : {};
  let saved = false;
  const app = render(
    <EnvSetup
      root={root}
      repo={repo}
      variables={variables}
      initial={initial}
      onSaved={() => {
        saved = true;
      }}
    />,
  );
  await app.waitUntilExit();
  return saved;
}
const addFields = [
  { key: "url", label: "Git URL", hint: "Repository clone URL" },
  { key: "name", label: "App name", hint: "Defaults to the repository name" },
  { key: "domain", label: "Domain", hint: "Defaults to <name>.localhost" },
  { key: "container", label: "Container port", hint: "Defaults to 3000" },
  { key: "host", label: "Host port", hint: "Automatically assigned if blank" },
] as const;
type AddField = (typeof addFields)[number]["key"];
function AddApp({
  m,
  onDone,
}: {
  m: Manifest;
  onDone: (args: string[]) => void;
}) {
  const { exit } = useApp();
  const [values, setValues] = useState<Record<AddField, string>>({
    url: "",
    name: "",
    domain: "",
    container: "",
    host: "",
  });
  const [selected, setSelected] = useState(0);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [message, setMessage] = useState("");
  const field = addFields[selected];
  useInput((input, key) => {
    if (editing) {
      if (key.escape) {
        setEditing(false);
        setDraft("");
      } else if (key.return) {
        setValues((old) => ({ ...old, [field.key]: draft.trim() }));
        setEditing(false);
        setDraft("");
        setMessage("");
        setSelected((x) => Math.min(addFields.length - 1, x + 1));
      } else if (key.backspace || key.delete)
        setDraft((old) => old.slice(0, -1));
      else if (!key.ctrl && !key.meta && input && !/[\r\n\0]/.test(input))
        setDraft((old) => old + input);
      return;
    }
    if (input === "q" || key.escape) return exit();
    if (key.upArrow || input === "k") setSelected((x) => Math.max(0, x - 1));
    if (key.downArrow || input === "j")
      setSelected((x) => Math.min(addFields.length - 1, x + 1));
    if (key.return || input === "e") {
      setDraft(values[field.key]);
      setEditing(true);
    }
    if (input === "s") {
      if (!values.url) {
        setMessage("Enter a Git URL before adding the app.");
        return;
      }
      const inferred = path
        .basename(values.url)
        .replace(/\.git$/, "")
        .toLowerCase();
      const name = values.name || inferred;
      const domain = values.domain || `${name}.localhost`;
      if (!safeName.test(name)) {
        setMessage(
          "App name must start with a letter and use lowercase letters, numbers, or hyphens.",
        );
        return;
      }
      if (!validDomain(domain)) {
        setMessage("Enter a valid domain, such as api.example.com.");
        return;
      }
      if (
        (values.container && !validPort(Number(values.container))) ||
        (values.host && !validPort(Number(values.host)))
      ) {
        setMessage("Ports must be whole numbers from 1 to 65535.");
        return;
      }
      if (
        m.repos.some(
          (r) =>
            r.name === name ||
            r.url === values.url ||
            r.domain === domain ||
            (values.host && r.host_port === Number(values.host)),
        )
      ) {
        setMessage(
          "That app name, URL, domain, or host port is already assigned.",
        );
        return;
      }
      const args = ["add", values.url];
      if (values.name) args.push("--name", values.name);
      if (values.domain) args.push("--domain", values.domain);
      if (values.container) args.push("--container-port", values.container);
      if (values.host) args.push("--port", values.host);
      onDone(args);
      exit();
    }
  });
  const suggested = values.url
    ? path
        .basename(values.url)
        .replace(/\.git$/, "")
        .toLowerCase()
    : "app";
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>
        ◈ UPPR / add application
      </Text>
      <Text color={C.muted}>
        Register a Git repository. Pull it next to set up its schema and
        environment.
      </Text>
      <Box
        flexDirection="column"
        borderStyle="round"
        borderColor={C.purple}
        paddingX={1}
        marginTop={1}
      >
        {addFields.map((item, i) => (
          <Box key={item.key} flexDirection="column" marginBottom={1}>
            <Text
              color={i === selected ? C.cyan : C.white}
              bold={i === selected}
            >
              {i === selected ? "❯ " : "  "}
              {item.label}:{" "}
              {values[item.key] ||
                (item.key === "name"
                  ? suggested
                  : item.key === "domain"
                    ? `${values.name || suggested}.localhost`
                    : item.key === "container"
                      ? "3000"
                      : item.key === "host"
                        ? "auto"
                        : "—")}
            </Text>
            {i === selected && <Text color={C.muted}> {item.hint}</Text>}
          </Box>
        ))}
      </Box>
      {editing ? (
        <Text color={C.cyan}>
          Editing {field.label}: <Text color={C.white}>{draft}▌</Text>
        </Text>
      ) : (
        <Text color={C.muted}>
          ↑↓ choose · Enter edit · s add app · q/Esc cancel
        </Text>
      )}
      {message && <Text color={C.red}>{message}</Text>}
    </Box>
  );
}
export async function showAddApp() {
  let args: string[] | null = null;
  const [, m] = findPad();
  const app = render(
    <AddApp
      m={m}
      onDone={(value) => {
        args = value;
      }}
    />,
  );
  await app.waitUntilExit();
  return args;
}
function InitPad({ onDone }: { onDone: (directory: string) => void }) {
  const { exit } = useApp();
  const [directory, setDirectory] = useState("my-pad");
  const [message, setMessage] = useState("");
  useInput((input, key) => {
    if (key.escape) return exit();
    if (key.return) {
      const target = path.resolve(directory.trim());
      if (!directory.trim()) {
        setMessage("Enter a folder name.");
        return;
      }
      if (
        fs.existsSync(target) &&
        (!fs.statSync(target).isDirectory() || fs.readdirSync(target).length)
      ) {
        setMessage("Choose a new or empty folder for the launchpad.");
        return;
      }
      onDone(target);
      exit();
    } else if (key.ctrl && input === "u") setDirectory("");
    else if (key.backspace || key.delete)
      setDirectory((old) => old.slice(0, -1));
    else if (!key.ctrl && !key.meta && input && !/[\r\n\0]/.test(input)) {
      setDirectory((old) => old + input);
      setMessage("");
    }
  });
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>
        ◈ UPPR / new launchpad
      </Text>
      <Text color={C.muted}>
        A launchpad holds your app checkouts, routes, and service configuration.
      </Text>
      <Box borderStyle="round" borderColor={C.cyan} paddingX={1} marginTop={1}>
        <Text color={C.white}>Folder: {directory}▌</Text>
      </Box>
      <Text color={C.muted}>{path.resolve(directory || ".")}</Text>
      <Text color={C.muted}>Enter create · Ctrl+U clear · Esc cancel</Text>
      {message && <Text color={C.red}>{message}</Text>}
    </Box>
  );
}
export async function showInitPad() {
  let directory: string | null = null;
  const app = render(
    <InitPad
      onDone={(value) => {
        directory = value;
      }}
    />,
  );
  await app.waitUntilExit();
  return directory;
}
function CurlPath({
  name,
  onDone,
}: {
  name: string;
  onDone: (route: string) => void;
}) {
  const { exit } = useApp();
  const [route, setRoute] = useState("");
  const [message, setMessage] = useState("");
  useInput((input, key) => {
    if (key.escape) return exit();
    if (key.return) {
      const value = route.trim() || "/";
      if (!value.startsWith("/")) {
        setMessage("Request path must start with /.");
        return;
      }
      onDone(value);
      exit();
    } else if (key.ctrl && input === "u") setRoute("");
    else if (key.backspace || key.delete)
      setRoute((old) => old.slice(0, -1));
    else if (!key.ctrl && !key.meta && input && !/[\r\n\0]/.test(input)) {
      setRoute((old) => old + input);
      setMessage("");
    }
  });
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>
        ◈ UPPR / curl {name}
      </Text>
      <Text color={C.muted}>Request path</Text>
      <Box borderStyle="round" borderColor={C.cyan} paddingX={1}>
        <Text color={C.white}>{route}▌</Text>
      </Box>
      <Text color={C.muted}>Enter send · Ctrl+U clear · Esc cancel</Text>
      {message && <Text color={C.red}>{message}</Text>}
    </Box>
  );
}
export async function showCurlPath(name: string): Promise<string | null> {
  let route: string | null = null;
  const app = render(
    <CurlPath
      name={name}
      onDone={(value) => {
        route = value;
      }}
    />,
  );
  await app.waitUntilExit();
  return route;
}
function RoutingScreen({ root, repo, onSaved }: {
  root: string;
  repo: Repo;
  onSaved: () => void;
}) {
  const { exit } = useApp();
  const file = schemaPath(root, repo);
  const initial: Schema = fs.existsSync(file) ? readSchema(file) : starterSchema(root, repo);
  const current = appRouting(root, repo);
  const [values, setValues] = useState([
    current.domains.join(", "),
    String(current.rateLimit.events),
    current.rateLimit.window,
    current.rateLimit.zone,
  ]);
  const [selected, setSelected] = useState(0);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [message, setMessage] = useState("");
  const labels = ["Domains", "Events", "Window", "Zone"];
  useInput((input, key) => {
    if (editing) {
      if (key.escape) setEditing(false);
      else if (key.return) {
        setValues((old) => old.map((v, i) => i === selected ? draft.trim() : v));
        setEditing(false);
        setMessage("");
      } else if (key.ctrl && input === "u") setDraft("");
      else if (key.backspace || key.delete) setDraft((old) => old.slice(0, -1));
      else if (!key.ctrl && !key.meta && input && !/[\r\n\0]/.test(input))
        setDraft((old) => old + input);
      return;
    }
    if (key.escape || input === "q") return exit();
    if (key.upArrow || input === "k") setSelected((i) => Math.max(0, i - 1));
    if (key.downArrow || input === "j") setSelected((i) => Math.min(3, i + 1));
    if (key.return || input === "e") {
      setDraft(values[selected]);
      setEditing(true);
    }
    if (input === "s") {
      try {
        const schema: Schema = {
          ...initial,
          domains: values[0].split(",").map((x) => x.trim()),
          rate_limit: {
            events: Number(values[1]),
            window: values[2],
            zone: values[3],
          },
        };
        saveSchema(file, schema);
        onSaved();
        exit();
      } catch (error) {
        setMessage(error instanceof Error ? error.message : String(error));
      }
    }
  });
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>◈ UPPR / routes · {repo.name}</Text>
      <Text color={C.muted}>Domains live in the repository schema. Rate limits apply per client IP.</Text>
      {labels.map((label, i) => (
        <Text key={label} color={selected === i ? C.cyan : C.white}>
          {selected === i ? "❯ " : "  "}{label}: {editing && selected === i ? draft + "▌" : values[i]}
        </Text>
      ))}
      <Text color={C.muted}>↑↓ select · Enter edit · s save · Esc cancel</Text>
      <Text color={C.yellow}>Save changes locally, then press w to push and i to install.</Text>
      {message && <Text color={C.red}>{message}</Text>}
    </Box>
  );
}
export async function showRouting(name: string) {
  const [root, m] = findPad();
  const repo = m.repos.find((r) => r.name === name)!;
  let saved = false;
  const app = render(<RoutingScreen root={root} repo={repo} onSaved={() => { saved = true; }} />);
  await app.waitUntilExit();
  return saved;
}
function PushScreen({ root, repo, onDone }: {
  root: string;
  repo: Repo;
  onDone: (message: string) => void;
}) {
  const { exit } = useApp();
  const dir = repoDir(root, repo);
  const status = probe("git", ["status", "--short"], dir);
  const [message, setMessage] = useState("Update app configuration");
  useInput((input, key) => {
    if (key.escape) return exit();
    if (key.return) {
      if (!message.trim()) return;
      onDone(message.trim());
      exit();
    } else if (key.ctrl && input === "u") setMessage("");
    else if (key.backspace || key.delete) setMessage((old) => old.slice(0, -1));
    else if (!key.ctrl && !key.meta && input && !/[\r\n\0]/.test(input))
      setMessage((old) => old + input);
  });
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>◈ UPPR / push {repo.name}</Text>
      <Text color={C.muted}>Changes to commit and push:</Text>
      <Text color={C.white}>{status.out || "(no local changes; existing commits may still need pushing)"}</Text>
      <Text color={C.cyan}>Commit message: {message}▌</Text>
      <Text color={C.muted}>Enter commit and push · Ctrl+U clear · Esc cancel</Text>
    </Box>
  );
}
export async function showPush(name: string): Promise<string | null> {
  const [root, m] = findPad();
  const repo = m.repos.find((r) => r.name === name)!;
  if (!fs.existsSync(path.join(repoDir(root, repo), ".git")))
    throw new Error(`${name} is not cloned; pull it before pushing`);
  let message: string | null = null;
  const app = render(<PushScreen root={root} repo={repo} onDone={(value) => { message = value; }} />);
  await app.waitUntilExit();
  return message;
}
function SettingsScreen() {
  const { exit } = useApp();
  const [saved, setSaved] = useState(!!gitToken());
  const [editing, setEditing] = useState(false);
  const [token, setToken] = useState("");
  const [message, setMessage] = useState("");
  useInput((input, key) => {
    if (editing) {
      if (key.escape) { setToken(""); setEditing(false); }
      else if (key.return) {
        try {
          saveGitToken(token);
          setSaved(true);
          setToken("");
          setEditing(false);
          setMessage("GitHub token saved.");
        } catch (error) { setMessage(error instanceof Error ? error.message : String(error)); }
      } else if (key.ctrl && input === "u") setToken("");
      else if (key.backspace || key.delete) setToken((old) => old.slice(0, -1));
      else if (!key.ctrl && !key.meta && input && !/[\r\n\0]/.test(input)) setToken((old) => old + input);
      return;
    }
    if (key.escape || input === "q") return exit();
    if (input === "l") { setToken(""); setEditing(true); setMessage(""); }
    if (input === "x") { removeGitToken(); setSaved(false); setMessage("GitHub token removed."); }
  });
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>◈ UPPR / settings</Text>
      <Text color={C.white}>GitHub token: {saved ? "saved" : "not saved"}</Text>
      {editing && <Text color={C.cyan}>Token: {"•".repeat(token.length)}▌</Text>}
      <Text color={C.muted}>{editing ? "Enter save · Esc cancel" : "l save token · x remove token · Esc return"}</Text>
      <Text color={C.muted}>Used for HTTPS Git pull and push. SSH remotes use your SSH key.</Text>
      {message && <Text color={C.yellow}>{message}</Text>}
    </Box>
  );
}
export async function showSettings() {
  const app = render(<SettingsScreen />);
  await app.waitUntilExit();
}
function ContinueScreen({
  title,
  detail,
  error = false,
}: {
  title: string;
  detail?: string;
  error?: boolean;
}) {
  const { exit } = useApp();
  useInput(() => exit());
  return (
    <Box flexDirection="column" paddingX={1} marginTop={1}>
      <Text color={error ? C.red : C.green} bold>
        {title}
      </Text>
      {detail && <Text color={C.muted}>{detail}</Text>}
      <Text color={C.muted}>Press any key to return to the dashboard.</Text>
    </Box>
  );
}
export async function showContinue(
  title: string,
  detail?: string,
  error = false,
) {
  const app = render(
    <ContinueScreen title={title} detail={detail} error={error} />,
  );
  await app.waitUntilExit();
}
function DependenciesScreen({ onAction }: { onAction: (action: "install" | "docker-access") => void }) {
  const { exit } = useApp();
  const rows = dependencies();
  const canInstall = process.platform === "linux" && !!(which("apt-get") || which("dnf"));
  const missing = rows.filter((row) => row.required && !row.ok);
  const dockerInstalled = !!rows.find((row) => row.name === "Docker")?.ok;
  const accessRow = rows.find((row) => row.name === "Docker access");
  const dockerAccess = !!accessRow?.ok;
  const needsDockerGroup = !dockerAccess && /permission denied/i.test(accessRow?.detail || "");
  useInput((input, key) => {
    if (input === "q" || key.escape) exit();
    if (key.return && canInstall) {
      onAction("install");
      exit();
    }
    if (input === "g" && process.platform === "linux" && dockerInstalled && needsDockerGroup) {
      onAction("docker-access");
      exit();
    }
  });
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>◈ UPPR / host setup</Text>
      <Text color={C.muted}>Shared tools for every repo in this launchpad</Text>
      <Box flexDirection="column" marginY={1}>
        {rows.map((row) => (
          <Box key={row.name}>
            <Box width={22}><Text color={row.ok ? C.green : row.required ? C.yellow : C.muted}>{row.ok ? "✓" : row.required ? "!" : "○"} {row.name}</Text></Box>
            <Text color={C.muted}>{row.detail}</Text>
          </Box>
        ))}
      </Box>
      {missing.length === 0 && <Text color={C.green}>Host dependencies are ready.</Text>}
      {needsDockerGroup && dockerInstalled && process.platform === "linux" && (
        <Text color={C.yellow}>g grant this account Docker access · sign out and back in afterward</Text>
      )}
      <Text color={canInstall ? C.cyan : C.yellow}>
        {canInstall ? "Enter install or repair with apt/dnf · sudo may ask for your password" : "Automatic installation needs a Linux host with apt or dnf"}
      </Text>
      <Text color={C.muted}>Esc/q return to dashboard</Text>
    </Box>
  );
}
export async function showDependencies() {
  let action: "install" | "docker-access" | null = null;
  const app = render(<DependenciesScreen onAction={(value) => { action = value; }} />);
  await app.waitUntilExit();
  return action;
}
export async function showHelp() {
  const app = render(<HelpScreen />);
  await app.waitUntilExit();
}
function HelpScreen() {
  const { exit } = useApp();
  useInput(() => exit());
  return (
    <Box flexDirection="column" paddingX={1}>
      <Text color={C.purple} bold>
        ◈ UPPR / keys
      </Text>
      {[
        ["Enter", "Run the selected app's next setup step"],
        ["a · p · P · A", "Add app · pull one · pull all · launch all"],
        ["D", "Check and install host dependencies"],
        [
          "s · r · o · x · l",
          "Launch · restart · observe output · stop · recent logs",
        ],
        ["u · v · c · i", "Curl app · Caddyfile · configure env · install"],
        ["d · w · t", "Edit routes and limits · push repo · Git settings"],
        ["e · g", "Edit schema · generate from env example"],
        ["↑↓ · j/k · q", "Select app · quit"],
      ].map(([keys, detail]) => (
        <Box key={keys}>
          <Box width={19}>
            <Text color={C.cyan}>{keys}</Text>
          </Box>
          <Text color={C.white}>{detail}</Text>
        </Box>
      ))}
      <Text color={C.muted}>
        Schema and env changes need an app restart to take effect.
      </Text>
      <Text color={C.muted}>Press any key to return.</Text>
    </Box>
  );
}
export function showList() {
  const [root, m] = findPad();
  const entries = sorted(m).map((repo) => {
    const status = appStatus(m, repo);
    return { repo, status, setup: appSetup(root, repo, status) };
  });
  console.log(
    renderToString(<Board root={root} m={m} entries={entries} />, {
      columns: Math.max(100, process.stdout.columns || 100),
    }),
  );
}
export async function showDashboard() {
  const [root, m] = findPad();
  if (!process.stdin.isTTY) {
    showList();
    return null;
  }
  let action: { kind: string; name?: string } | null = null;
  const app = render(
    <Dashboard
      root={root}
      m={m}
      onAction={(kind, name) => {
        action = { kind, name };
      }}
    />,
  );
  await app.waitUntilExit();
  return action;
}
