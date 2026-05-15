import { config as loadDotenv } from "dotenv";
loadDotenv();
import { render } from "ink";
import { App } from "./app.js";
import type { Instance } from "ink";

if (!process.stdin.isTTY) {
  console.error("deerflow-tui: must run in an interactive terminal (TTY).");
  process.exit(1);
}

process.on("unhandledRejection", (reason) => {
  const msg = reason instanceof Error ? reason.message : String(reason);
  console.error(`\n[unhandledRejection] ${msg}`);
});

let instance: Instance | undefined;

process.on("uncaughtException", (err) => {
  console.error(`\n[uncaughtException] ${err.message}`);
  instance?.unmount();
  process.exit(1);
});

instance = render(<App />, {
  exitOnCtrlC: true,
});

process.on("SIGINT", () => {
  instance?.unmount();
  process.exit(0);
});

async function main() {
  try {
    await instance?.waitUntilExit();
    process.exit(0);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    console.error(`\n[exit] ${msg}`);
    process.exit(1);
  }
}

void main();
