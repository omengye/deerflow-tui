# AG-UI Event Display Patch

This project intentionally patches `@assistant-ui/react-ag-ui` runtime files under
`node_modules` to support independent AG-UI event display in the TUI.

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

- `node_modules/@assistant-ui/react-ag-ui/src/runtime/adapter/run-aggregator.ts`
- `node_modules/@assistant-ui/react-ag-ui/dist/runtime/adapter/run-aggregator.js`
- `node_modules/@assistant-ui/react-ag-ui/src/runtime/AgUiThreadRuntimeCore.ts`
- `node_modules/@assistant-ui/react-ag-ui/dist/runtime/AgUiThreadRuntimeCore.js`
- `src/components/chat/Message.tsx`

## Behavior

The patch adds `unstable_agui` metadata to emitted assistant-ui message parts:

- `TEXT_MESSAGE` parts include `messageId`.
- `REASONING` and `THINKING` parts are split by `messageId` when present, or by a
  generated id when absent.
- `TOOL_CALL` parts include `toolCallId`, `parentMessageId`, and optional tool
  result message id.
- `STATE_SNAPSHOT` and `STATE_DELTA` are emitted as synthetic `agui-state` parts.

The TUI message renderer displays each part as its own block with a header like:

```text
[TEXT_MESSAGE] #message1
```

## Maintenance Note

Because some changes are inside `node_modules`, reinstalling dependencies can
overwrite them. After running `pnpm install`, verify or reapply this patch before
shipping.

Validation commands used after the change:

```powershell
pnpm.cmd typecheck
pnpm.cmd build
```
