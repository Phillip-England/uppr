#!/usr/bin/env bun
import { command, help } from "./commands.ts";
import { showDashboard, showList } from "./ui.tsx";

try {
  const [cmd, ...args] = process.argv.slice(2);
  if (!cmd) {
    const action = await showDashboard();
    if (action) {
      const a = action as { kind: string; name: string };
      await command(
        a.kind === "schema-edit"
          ? ["schema", "edit", a.name]
          : a.kind === "schema-generate"
            ? ["schema", "generate", a.name]
            : [a.kind, a.name],
      );
    }
  } else if (cmd === "list") {
    showList();
  } else await command([cmd, ...args]);
} catch (error) {
  console.error(
    "uppr:",
    error instanceof Error ? error.message : String(error),
  );
  process.exitCode = 1;
}
