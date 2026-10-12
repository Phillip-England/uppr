import React, { useEffect, useMemo, useState } from "react";
import fs from "node:fs";
import path from "node:path";
import { Box, Text, useApp, useInput, render, renderToString } from "ink";
import { restartJob, subscribeRestartJobs } from "./restart-jobs.ts";
import {
  appStatus,
  appSetup,
  findPad,
  repoDir,
  readEnv,
  readSchema,
  saveRepoEnv,
  safeName,
  schemaPath,
  sorted,
  validDomain,
  validPort,
  type Manifest,
  type Repo,
  type Variable,
  type SetupStep,
} from "./core.ts";

type Entry = {
  repo: Repo;
  status: ReturnType<typeof appStatus>;
  setup: SetupStep;
};
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
  serve: "serve app",
  logs: "view logs",
};
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
                        ? "restarting"
                        : s.state
                    }
                  />
                </Box>
                <Text
                  color={s.state === "running" ? C.green : C.muted}
                  bold={s.state === "running"}
                >
                  https://{r.domain}
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
                  {r.container_port} · Files: {repoDir(root, r)}
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
            https://{active.repo.domain}
          </Text>
          {active.status.state !== "running" && (
            <Text color={active.setup.action ? C.yellow : C.muted}>
              {active.setup.label} · {active.setup.detail}
            </Text>
          )}
          <Text color={C.muted}>
            Internal: 127.0.0.1:{active.repo.host_port} → container :
            {active.repo.container_port}
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
          {interactive &&
            restartJob(root, active.repo.name)?.state === "restarting" && (
              <Text color={C.cyan}>o → observe restart output</Text>
            )}
          {interactive &&
            restartJob(root, active.repo.name)?.state === "failed" && (
              <Text color={C.red}>
                {restartJob(root, active.repo.name)?.message}. Run uppr restart{" "}
                {active.repo.name} for details.
              </Text>
            )}
        </Box>
      )}
      {interactive && notice && <Text color={C.yellow}>{notice}</Text>}
      {interactive && (
        <Box flexDirection="column" marginTop={1}>
          <Text color={C.muted}>
            ↑↓ choose · Enter next · a add · p pull · c env · i install · ? help
          </Text>
          <Text color={C.muted}>
            s serve · r restart · o observe · x stop · l logs · e schema · g
            generate · q quit
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
    if (input === "a" || input === "i" || input === "?") {
      onAction(input === "a" ? "add" : input === "i" ? "install" : "help");
      exit();
      return;
    }
    if (!entries.length) return;
    const entry = entries[Math.min(selected, entries.length - 1)];
    const name = entry.repo.name;
    if (input === "o") {
      if (restartJob(root, name)?.state !== "restarting") {
        setNotice(`${name} has no active operation to observe.`);
        return;
      }
      onAction("observe", name);
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
      c: "env-setup",
      e: "schema-edit",
      g: "schema-generate",
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
        Restart {job?.state || "complete"} · q/Esc dashboard
      </Text>
      {job?.state === "restarting" ? (
        job.lines.length ? (
          job.lines
            .slice(-Math.max(1, (process.stdout.rows || 30) - 5))
            .map((line, i) => (
              <Text key={i} color={C.white}>
                {line || " "}
              </Text>
            ))
        ) : (
          <Text color={C.muted}>Waiting for output…</Text>
        )
      ) : (
        <Text color={job?.state === "failed" ? C.red : C.green}>
          {job?.message || "Observation ended"}
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
        ["a · p · c · i", "Add app · pull · configure env · install"],
        [
          "s · r · o · x · l",
          "Serve · restart · observe active output · stop · recent logs",
        ],
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
