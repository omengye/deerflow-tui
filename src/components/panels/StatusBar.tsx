import { Box, Text } from "ink";
import { useAuiState } from "@assistant-ui/react-ink";

export function StatusBar() {
  const isRunning = useAuiState((s) => s.thread.isRunning);
  const msgCount = useAuiState((s) => s.thread.messages.length);
  const endpoint = process.env.AG_UI_ENDPOINT ?? "http://localhost:8000/agent";

  return (
    <Box justifyContent="space-between" paddingX={1} marginTop={1}>
      <Text dimColor>{isRunning ? "Running" : "Idle"}</Text>
      <Text dimColor>Messages: {msgCount}</Text>
      <Text dimColor>{endpoint}</Text>
    </Box>
  );
}
