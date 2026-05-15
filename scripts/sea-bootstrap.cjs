const { getAsset } = require("node:sea");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const entrySource = getAsset("entry.mjs", "utf8");

const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "deerflow-tui-"));
const tmpEntry = path.join(tmpDir, "entry.mjs");
fs.writeFileSync(tmpEntry, entrySource);

const cleanup = () => {
  try {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  } catch {}
};
process.on("exit", cleanup);
process.on("SIGINT", () => {
  cleanup();
  process.exit(130);
});
process.on("SIGTERM", () => {
  cleanup();
  process.exit(143);
});

import(pathToFileURL(tmpEntry).href).catch((error) => {
  console.error(error);
  cleanup();
  process.exitCode = 1;
});
