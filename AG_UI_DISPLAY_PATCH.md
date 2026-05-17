# AG-UI Event Display Patch

This project patches `@assistant-ui/react-ag-ui` to support independent AG-UI
event display in the TUI.

`node_modules/@assistant-ui/react-ag-ui/dist` is the published runtime entry for
the dependency package, not this project's generated `dist` directory. The patch
therefore includes both the dependency `src` copy and the dependency `dist`
runtime entry because the package `exports` points to `./dist/index.js`.

## Why

The upstream `RunAggregator` groups one run into a single assistant message. In
the default behavior:

- `TEXT_MESSAGE_*` events are buffered by `messageId`, but displayed as parts of
  one assistant message.
- `REASONING_*` and `THINKING_*` events share one reasoning buffer, so different
  `messageId` values can be merged.
- `TOOL_CALL_*` events are grouped by `toolCallId`.
- `STATE_SNAPSHOT` and `STATE_DELTA` update internal state but are not displayed
  as message content.

For this TUI, different AG-UI event types and different IDs should be visible as
separate display blocks.

## Patched Files

The checked-in pnpm patch is:

- `patches/@assistant-ui__react-ag-ui@0.0.30.patch`

It currently patches these dependency-package files:

- `src/runtime/adapter/run-aggregator.ts`
- `dist/runtime/adapter/run-aggregator.js`

The older display patch also touched these files in the installed dependency and
local renderer:

- `src/runtime/AgUiThreadRuntimeCore.ts`
- `dist/runtime/AgUiThreadRuntimeCore.js`
- `src/components/chat/Message.tsx`

## Behavior

The patch adds `unstable_agui` metadata to emitted assistant-ui message parts:

- `TEXT_MESSAGE` parts include `messageId`.
- `REASONING` parts are split by `messageId` when present. The aggregator keeps
  a `Map` of reasoning parts instead of one shared `reasoningBuffer`, and emits
  one assistant-ui `reasoning` part per reasoning id.
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

## 2026-05-17 Reasoning Split Update

The local dependency patch was refreshed after confirming the installed
`RunAggregator` still had a single shared reasoning buffer:

- Replaced `reasoningBuffer`, `reasoningActive`, and `hasReasoningPart` with
  `reasoningParts: Map<string, ReasoningState>`, `activeReasoningId`, and a
  generated reasoning counter.
- Changed `REASONING_START`, `REASONING_MESSAGE_CONTENT`, and `REASONING_END`
  handling to pass the full event object so `event.messageId` can be used as
  the bucket key.
- Made `REASONING_MESSAGE_CONTENT` create its part lazily, so content-only
  sequences are still grouped by `messageId`.
- Updated emission to push multiple `reasoning` parts, each with
  `unstable_agui: { eventType: "REASONING", messageId }`.
- Kept fallback handling for `THINKING_*` events through generated ids because
  the current dependency event types do not carry thinking `messageId`.

Validation included a direct aggregator smoke test with two `REASONING`
message ids:

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

## Maintenance Note

The current durable patch method is pnpm's built-in patch flow:

```powershell
pnpm.cmd patch @assistant-ui/react-ag-ui@0.0.30
# edit the generated package copy
pnpm.cmd patch-commit "<generated patch directory>"
pnpm.cmd install
```

The patch is wired through `pnpm-workspace.yaml`:

```yaml
patchedDependencies:
  '@assistant-ui/react-ag-ui@0.0.30': patches/@assistant-ui__react-ag-ui@0.0.30.patch
```

After `pnpm install`, verify that
`node_modules/@assistant-ui/react-ag-ui/dist/runtime/adapter/run-aggregator.js`
contains `reasoningParts` before shipping.

Validation commands used after the change:

```powershell
pnpm.cmd typecheck
pnpm.cmd build
```
