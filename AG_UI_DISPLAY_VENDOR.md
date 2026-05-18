# AG-UI Event Display Vendoring

This project vendors the runtime layer of `@assistant-ui/react-ag-ui` into
`src/vendor/react-ag-ui/` so the AG-UI event aggregator can be customized
without `pnpm patch`. The upstream package is no longer a runtime dependency.

## Why

The upstream `RunAggregator` groups one run into a single assistant message.
In the default behavior:

- `TEXT_MESSAGE_*` events are buffered by `messageId`, but displayed as parts of
  one assistant message.
- `REASONING_*` and `THINKING_*` events share one reasoning buffer, so different
  `messageId` values can be merged.
- `TOOL_CALL_*` events are grouped by `toolCallId`.
- `STATE_SNAPSHOT` and `STATE_DELTA` update internal state but are not displayed
  as message content.

For this TUI, different AG-UI event types and different IDs should be visible as
separate display blocks. The aggregator has no extension point for that, so a
local fork is required. Vendoring is preferred over `pnpm patch` because:

- The published `exports` only ship `./dist/index.js`, so a patch had to modify
  both `src/` and `dist/` of the installed package (~520 lines, including
  transpiled output).
- 500+ line patches break on any whitespace drift between upstream releases.
- TypeScript cannot type-check patched files inside `node_modules`.

The vendored copy is plain TypeScript in `src/`, fully type-checked, and
review-friendly.

## Layout

```
src/vendor/react-ag-ui/
├── index.ts
├── useAgUiRuntime.ts
└── runtime/
    ├── AgUiThreadRuntimeCore.ts
    ├── event-parser.ts
    ├── logger.ts
    ├── types.ts
    └── adapter/
        ├── conversions.ts
        ├── run-aggregator.ts   <-- customized
        └── subscriber.ts
```

`src/runtime/agent-runtime.ts` imports `useAgUiRuntime` from
`../vendor/react-ag-ui/index.js`.

Direct runtime dependencies needed by the vendored code (declared in
`package.json`):

- `@ag-ui/client`
- `@assistant-ui/core`
- `assistant-stream`
- `react`

## Customizations

All customizations live in
`src/vendor/react-ag-ui/runtime/adapter/run-aggregator.ts`. The aggregator adds
`unstable_agui` metadata to emitted assistant-ui message parts:

- `TEXT_MESSAGE` parts include `messageId`. Duplicate `TEXT_MESSAGE_*` sequences
  for an already-completed `messageId` are dropped via
  `completedTextMessageIds` / `ignoredTextMessageIds`.
- `REASONING` parts are split by `messageId` when present. The aggregator keeps
  a `Map<string, ReasoningState>` (`reasoningParts`) instead of one shared
  `reasoningBuffer`, and emits one assistant-ui `reasoning` part per reasoning
  id.
- `THINKING` parts use the same reasoning-part path, but upstream
  `THINKING_TEXT_MESSAGE_*` event types do not currently expose `messageId`.
  They fall back to a generated id when no active reasoning id is available.
- `TOOL_CALL` parts include `toolCallId`, `parentMessageId`, and optional tool
  result message id.
- `STATE_SNAPSHOT` and `STATE_DELTA` are emitted as synthetic `agui-state` parts.

The TUI message renderer displays each part as its own block with a header like:

```text
[TEXT_MESSAGE] #message1
```

Validation snapshot for the reasoning split (two `REASONING` message ids):

```json
[
  {
    "type": "reasoning",
    "text": "one",
    "unstable_agui": { "eventType": "REASONING", "messageId": "m1" }
  },
  {
    "type": "reasoning",
    "text": "two",
    "unstable_agui": { "eventType": "REASONING", "messageId": "m2" }
  }
]
```

## Maintenance

### Editing customizations

Edit the vendored file directly — typically only
`src/vendor/react-ag-ui/runtime/adapter/run-aggregator.ts`. After changes:

```powershell
pnpm.cmd typecheck
pnpm.cmd build
```

No reinstall is needed; the vendor lives inside `src/`.

### Upgrading upstream

When `@assistant-ui/react-ag-ui` releases a relevant version:

1. Add the package to a scratch install (or read its tarball) to get the new
   `src/` tree. The package ships its `.ts` sources alongside `dist/`.
2. Diff each file under `src/runtime/` and `src/useAgUiRuntime.ts` against the
   vendored copy.
3. Apply the diff to the vendored files. For
   `runtime/adapter/run-aggregator.ts` resolve conflicts manually — keep the
   customizations listed above.
4. Re-add `.js` extensions on relative imports (NodeNext requirement; upstream
   ships extension-less imports). The current vendored files already follow
   this convention.
5. Re-add explicit parameter types where the project's strict tsconfig requires
   them (currently the three callbacks in
   `AgUiThreadRuntimeCore.ts#startRun` and `agent-runtime.ts#onError`).
6. Run `pnpm typecheck` and `pnpm build` to verify.

### When to consider going back upstream

If `@assistant-ui/react-ag-ui` ever exposes a `RunAggregator` factory or
documented extension point for the splits above (the `unstable_agui` metadata
name suggests upstream is open to it), the vendor can be deleted and replaced
with a direct dependency plus a small adapter.
