import { memo } from "react";
import { Box, Text } from "ink";
import {
  AssistantRuntimeProvider,
  ThreadPrimitive,
  useAuiState,
} from "@assistant-ui/react-ink";
import { useAgentRuntime } from "./runtime/agent-runtime.js";
import { Message } from "./components/chat/Message.js";
import { InputBar } from "./components/composer/InputBar.js";
import { StatusBar } from "./components/panels/StatusBar.js";

const MessagesList = memo(function MessagesList() {
  const isRunning = useAuiState((s) => s.thread.isRunning);
  return (
    <ThreadPrimitive.Messages
      components={{ Message }}
      windowSize={isRunning ? 1 : 0}
      windowOverscan={0}
    />
  );
});

export function App() {
  const runtime = useAgentRuntime();

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <Box flexDirection="column" paddingX={1}>
        <Box marginBottom={1}>
          <Text bold color="cyan">deerflow-tui</Text>
          <Text dimColor> - AG-UI terminal client (Phase 0 spike)</Text>
        </Box>

        <ThreadPrimitive.Root flexDirection="column">
          <ThreadPrimitive.Empty>
            <Box marginY={1}>
              <Text dimColor>(No messages yet. Type to start.)</Text>
            </Box>
          </ThreadPrimitive.Empty>

          <MessagesList />
        </ThreadPrimitive.Root>

        <StatusBar />
        <InputBar />
      </Box>
    </AssistantRuntimeProvider>
  );
}
