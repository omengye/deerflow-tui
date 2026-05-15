import { Box, Text } from "ink";
import { useApp } from "ink";
import { ComposerPrimitive, useAui } from "@assistant-ui/react-ink";

export function InputBar() {
  const { exit } = useApp();
  const aui = useAui();

  const handleSubmit = (text: string) => {
    const command = text.trim().toLowerCase();

    if (command === "/exit" || command === "/quit") {
      exit();
      return;
    }

    if (command === "/new") {
      aui.composer().setText("");
      aui.threads().switchToNewThread();
      return;
    }

    aui.composer().send();
  };

  return (
    <Box
      borderStyle="round"
      borderColor="gray"
      paddingX={1}
      marginTop={1}
    >
      <Text color="gray">{"> "}</Text>
      <ComposerPrimitive.Input
        submitOnEnter
        placeholder="Type a message (/new, /exit, /quit)..."
        autoFocus
        onSubmit={handleSubmit}
      />
    </Box>
  );
}
