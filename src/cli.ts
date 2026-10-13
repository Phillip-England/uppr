#!/usr/bin/env bun
import { command } from "./commands.ts";

try {
  await command(process.argv.slice(2).length ? process.argv.slice(2) : ["help"]);
} catch (error) {
  console.error("uppr:", error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
}
