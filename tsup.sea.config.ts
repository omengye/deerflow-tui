import { defineConfig } from "tsup";

const bundledDependencies = [
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
];

export default defineConfig({
  entry: ["src/entry.tsx"],
  outDir: "dist-sea",
  outExtension: () => ({ js: ".mjs" }),
  format: ["esm"],
  target: "node24",
  platform: "node",
  clean: false,
  sourcemap: false,
  dts: false,
  splitting: false,
  shims: false,
  bundle: true,
  noExternal: bundledDependencies,
  banner: {
    js: [
      "import { createRequire as __deerflowCreateRequire } from 'node:module';",
      "const require = __deerflowCreateRequire(process.execPath);",
    ].join("\n"),
  },
});
