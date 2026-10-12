#!/usr/bin/env bun
import path from "node:path";
import { command, help } from "./commands.ts";
import { findPad, unitName } from "./core.ts";
import { startOperation } from "./restart-jobs.ts";
import {
  showAddApp,
  showContinue,
  showCurlPath,
  showDashboard,
  showEnvSetup,
  showHelp,
  showInitPad,
  showList,
  showObserve,
} from "./ui.tsx";

function clearScreen() {
  if (process.stdout.isTTY) process.stdout.write("\x1b[2J\x1b[H");
}

try {
  const [cmd, ...args] = process.argv.slice(2);
  if (!cmd) {
    let first = true;
    while (true) {
      if (!first) clearScreen();
      first = false;
      if (process.stdin.isTTY) {
        try {
          findPad();
        } catch (error) {
          if (
            !(error instanceof Error) ||
            !error.message.startsWith("launchpad not found")
          )
            throw error;
          const directory = await showInitPad();
          if (!directory) break;
          try {
            await command(["init", directory]);
            process.chdir(path.resolve(directory));
            await showContinue(
              "Launchpad created",
              "Press a to add your first app.",
            );
          } catch (initError) {
            await showContinue(
              "Could not create launchpad",
              initError instanceof Error
                ? initError.message
                : String(initError),
              true,
            );
          }
          continue;
        }
      }
      const action = await showDashboard();
      if (!action) break;
      const a = action as { kind: string; name?: string };
      if (
        ["restart", "pull", "serve", "launch-all", "pull-all"].includes(a.kind)
      ) {
        const [root, manifest] = findPad();
        const all = a.kind.endsWith("-all");
        const name = all ? "--all" : a.name!;
        const repo = all
          ? undefined
          : manifest.repos.find((repo) => repo.name === name)!;
        const verb =
          a.kind === "serve" || a.kind === "launch-all"
            ? "launch"
            : a.kind === "pull-all"
              ? "pull"
              : a.kind;
        startOperation(
          root,
          name,
          verb,
          [verb, name],
          repo && verb !== "pull" ? unitName(manifest, repo) : undefined,
        );
        continue;
      }
      clearScreen();
      if (a.kind === "observe") {
        await showObserve(a.name!);
        continue;
      }
      if (a.kind === "help") {
        await showHelp();
        continue;
      }
      try {
        if (a.kind === "curl") {
          const route = await showCurlPath(a.name!);
          if (route === null) continue;
          clearScreen();
          await command(["curl", a.name!, route]);
          await showContinue("Request complete");
          continue;
        }
        if (a.kind === "env-setup") {
          const saved = await showEnvSetup(a.name!);
          if (saved)
            await showContinue(
              "Environment saved",
              `Restart ${a.name} to apply the changes.`,
            );
          continue;
        }
        let argv: string[];
        if (a.kind === "add") {
          const input = await showAddApp();
          if (!input) continue;
          argv = input;
          clearScreen();
        } else if (a.kind === "schema-edit") argv = ["schema", "edit", a.name!];
        else if (a.kind === "schema-generate")
          argv = ["schema", "generate", a.name!];
        else if (a.kind === "logs") argv = ["logs", a.name!, "--no-follow"];
        else argv = a.name ? [a.kind, a.name] : [a.kind];
        console.log(`Running ${argv.join(" ")}...`);
        await command(argv);
        await showContinue(
          a.kind === "add" ? "App added" : "Action complete",
          a.kind === "add"
            ? "Select it and press Enter to pull the repository."
            : undefined,
        );
      } catch (error) {
        await showContinue(
          "Action failed",
          error instanceof Error ? error.message : String(error),
          true,
        );
      }
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
