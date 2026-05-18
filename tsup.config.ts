import { defineConfig } from "tsup";

export default defineConfig({
  entry: ["src/entry.tsx"],
  format: ["esm"],
  target: "node24",
  platform: "node",
  clean: true,
  sourcemap: true,
  dts: false,
  splitting: false,
  shims: true,
  bundle: true,
  noExternal: [
    "@ag-ui/client",
    "@assistant-ui/core",
    "@assistant-ui/react-ink",
    "@assistant-ui/react-ink-markdown",
    "assistant-cloud",
    "assistant-stream",
    "ink",
    "react",
    "react-devtools-core",
    "dotenv",
  ],
  banner: {
    js: [
      "#!/usr/bin/env node",
      "import { createRequire as __deerflowCreateRequire } from 'node:module';",
      "const require = __deerflowCreateRequire(import.meta.url);",
    ].join("\n"),
  },
});
