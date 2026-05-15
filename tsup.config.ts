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
  shims: false,
  bundle: true,
  noExternal: [
    "@ag-ui/client",
    "@assistant-ui/react-ag-ui",
    "@assistant-ui/react-ink",
    "@assistant-ui/react-ink-markdown",
    "assistant-cloud",
    "ink",
    "react",
    "react-devtools-core",
  ],
  banner: {
    js: "#!/usr/bin/env node",
  },
});
