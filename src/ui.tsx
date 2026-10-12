import React, { useEffect, useState } from "react";
import { Box, Text, useApp, useInput, render, renderToString } from "ink";
import {
  appStatus,
  findPad,
  repoDir,
  sorted,
  type Manifest,
  type Repo,
} from "./core.ts";

type Entry = { repo: Repo; status: ReturnType<typeof appStatus> };
const C = {
  purple: "#B598F2",
  cyan: "#6EE7DD",
  green: "#8CE9A2",
  red: "#F28F9B",
  muted: "#8992A7",
  white: "#F5F0E8",
  yellow: "#F4D18A",
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
}: {
  root: string;
  m: Manifest;
  entries: Entry[];
  selected?: number;
  interactive?: boolean;
}) {
  const running = entries.filter((e) => e.status.state === "running").length;
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
      <Text color={C.muted}>Launchpad {root}</Text>
      <Box
        flexDirection="column"
        borderStyle="round"
        borderColor={C.muted}
        paddingX={1}
        marginTop={1}
      >
        <Box marginBottom={1}>
          <Box width={25}>
            <Text color={C.muted}>APP</Text>
          </Box>
          <Box width={17}>
            <Text color={C.muted}>STATE</Text>
          </Box>
          <Text color={C.muted}>ROUTE</Text>
        </Box>
        {entries.length === 0 ? (
          <Text color={C.muted}>
            {" "}
            No apps yet. Add one with uppr add &lt;url&gt;.
          </Text>
        ) : (
          entries.map(({ repo: r, status: s }, i) => (
            <Box key={r.name} flexDirection="column" marginBottom={1}>
              <Box>
                <Box width={25}>
                  <Text color={selected === i ? C.cyan : C.muted}>
                    {selected === i ? "❯ " : "  "}
                  </Text>
                  <Text color={C.white} bold>
                    {r.name}
                  </Text>
                </Box>
                <Box width={17}>
                  <Badge state={s.state} />
                </Box>
                <Text color={C.cyan}>https://{r.domain}</Text>
              </Box>
              <Text color={C.muted}>
                {" "}
                Ports 127.0.0.1:{r.host_port} → container :{r.container_port}
              </Text>
              <Text color={C.muted}> Path {repoDir(root, r)}</Text>
              {s.health !== "—" && (
                <Text color={C.muted}>
                  {" "}
                  health {s.health} • service {s.service}
                </Text>
              )}
            </Box>
          ))
        )}
      </Box>
      {interactive && (
        <Box marginTop={1}>
          <Text color={C.muted}>
            ↑↓ select s start r restart x stop l logs e schema g generate schema
            q quit
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
  onAction: (action: string, name: string) => void;
}) {
  const { exit } = useApp();
  const [selected, setSelected] = useState(0),
    [tick, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((x) => x + 1), 3000);
    return () => clearInterval(id);
  }, []);
  const entries = sorted(m).map((repo) => ({
    repo,
    status: appStatus(m, repo),
  }));
  useInput((input, key) => {
    if (input === "q" || key.escape) {
      exit();
      return;
    }
    if (key.upArrow || input === "k") setSelected((x) => Math.max(0, x - 1));
    if (key.downArrow || input === "j")
      setSelected((x) => Math.min(entries.length - 1, x + 1));
    if (!entries.length) return;
    const name = entries[selected].repo.name;
    const actions: Record<string, string> = {
      s: "serve",
      r: "restart",
      x: "stop",
      l: "logs",
      e: "schema-edit",
      g: "schema-generate",
    };
    if (actions[input]) {
      onAction(actions[input], name);
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
    />
  );
}
export function showList() {
  const [root, m] = findPad();
  const entries = sorted(m).map((repo) => ({
    repo,
    status: appStatus(m, repo),
  }));
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
  let action: { kind: string; name: string } | null = null;
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
