import { Box, Text } from "ink";
import { useAuiState } from "@assistant-ui/react-ink";
import type { ThreadMessage, ToolCallMessagePart } from "@assistant-ui/react-ink";
import type { ReactNode } from "react";

type AgUiPartMetadata = {
  eventType?: string;
  messageId?: string;
  toolCallId?: string;
  parentMessageId?: string;
  toolMessageId?: string;
};

type AssistantMessage = Extract<ThreadMessage, { role: "assistant" }>;
type AssistantContent = AssistantMessage["content"];

export function Message() {
  const message = useAuiState((s) => s.message);

  const isUser = message.role === "user";
  const isAssistant = message.role === "assistant";

  if (isAssistant && !hasRenderableContent(message)) {
    return null;
  }

  const agentName = (message as any).name || (message.metadata?.custom?.agui as any)?.agentName;
  const label = isUser ? "You" : isAssistant ? (agentName || "AI") : message.role;
  const color = isUser ? "green" : isAssistant ? "blue" : "magenta";
  const assistantBlocks = isAssistant ? splitAssistantBlocks(message) : [];

  if (isAssistant && assistantBlocks.length > 1) {
    return (
      <>
        {assistantBlocks.map((block) => (
          <MessageBlock key={block.key} label={label} color={color}>
            <MessageContentParts parts={block.parts} />
          </MessageBlock>
        ))}
      </>
    );
  }

  return (
    <MessageBlock label={label} color={color}>
      <MessageContent message={message} />
    </MessageBlock>
  );
}

function MessageContent({ message }: { message: ThreadMessage }) {
  if (!message.content || message.content.length === 0) {
    return <Text dimColor>(empty)</Text>;
  }

  return <MessageContentParts parts={message.content as AssistantContent} />;
}

function MessageContentParts({ parts }: { parts: AssistantContent }) {
  if (!parts || parts.length === 0) {
    return <Text dimColor>(empty)</Text>;
  }

  return (
    <Box flexDirection="column">
      {parts.map((part, index) => renderMessagePart(part, index))}
    </Box>
  );
}

function MessageBlock({
  children,
  label,
  color,
}: {
  children: ReactNode;
  label: string;
  color: string;
}) {
  return (
    <Box flexDirection="column" marginBottom={1}>
      <Text bold color={color}>{label}:</Text>
      <Box paddingLeft={2}>
        {children}
      </Box>
    </Box>
  );
}

function renderMessagePart(part: AssistantContent[number], index: number) {
  const metadata = getAgUiMetadata(part);

  if (part.type === "text") {
    return (
      <EventBlock
        key={index}
        metadata={metadata}
        fallbackType="TEXT_MESSAGE"
        fallbackId={`text-${index + 1}`}
      >
        <Text wrap="wrap">{part.text}</Text>
      </EventBlock>
    );
  }

  if (part.type === "reasoning") {
    return (
      <EventBlock
        key={index}
        metadata={metadata}
        fallbackType="REASONING"
        fallbackId={`reasoning-${index + 1}`}
      >
        <Text dimColor wrap="wrap">
          {part.text}
        </Text>
      </EventBlock>
    );
  }

  if (part.type === "tool-call") {
    return (
      <EventBlock
        key={index}
        metadata={metadata}
        fallbackType="TOOL_CALL"
        fallbackId={part.toolCallId}
      >
        <CompactToolCall toolCall={part} />
      </EventBlock>
    );
  }

  if (isAgUiStatePart(part as unknown)) {
    const statePart = part as unknown as { type: "agui-state"; text: string };
    return (
      <EventBlock
        key={index}
        metadata={metadata}
        fallbackType="STATE"
        fallbackId={`state-${index + 1}`}
      >
        <Text dimColor wrap="wrap">
          {truncateLines(statePart.text, 8)}
        </Text>
      </EventBlock>
    );
  }

  return null;
}

function splitAssistantBlocks(message: ThreadMessage): Array<{
  key: string;
  parts: AssistantContent;
}> {
  if (message.role !== "assistant" || !message.content || message.content.length === 0) {
    return [];
  }

  const blocks: Array<{ key: string; parts: AssistantContent }> = [];

  for (const [index, part] of message.content.entries()) {
    const metadata = getAgUiMetadata(part);
    const messageId = metadata?.messageId;
    const blockKey = messageId ?? `part-${index}`;
    const current = blocks.at(-1);

    if (!current || current.key !== blockKey) {
      blocks.push({ key: blockKey, parts: [part] });
      continue;
    }

    current.parts = [...current.parts, part];
  }

  return blocks;
}

function EventBlock({
  children,
  metadata,
  fallbackType,
  fallbackId,
}: {
  children: ReactNode;
  metadata: AgUiPartMetadata | undefined;
  fallbackType: string;
  fallbackId: string;
}) {
  const eventType = metadata?.eventType ?? fallbackType;
  const id =
    metadata?.messageId ??
    metadata?.toolCallId ??
    metadata?.parentMessageId ??
    fallbackId;

  return (
    <Box flexDirection="column" marginBottom={1}>
      <Text dimColor>
        [{eventType}] #{shortId(id)}
      </Text>
      <Box paddingLeft={2} flexDirection="column">
        {children}
      </Box>
    </Box>
  );
}

function getAgUiMetadata(part: unknown): AgUiPartMetadata | undefined {
  if (!part || typeof part !== "object") return undefined;
  const metadata = (part as { unstable_agui?: unknown }).unstable_agui;
  if (!metadata || typeof metadata !== "object") return undefined;
  return metadata as AgUiPartMetadata;
}

function isAgUiStatePart(part: unknown): part is { type: "agui-state"; text: string } {
  return (
    Boolean(part) &&
    typeof part === "object" &&
    (part as { type?: unknown }).type === "agui-state" &&
    typeof (part as { text?: unknown }).text === "string"
  );
}

function hasRenderableContent(message: ThreadMessage): boolean {
  return Boolean(
    message.content?.some((part) => {
      if (part.type === "text" || part.type === "reasoning") {
        return part.text.trim().length > 0;
      }

      return part.type === "tool-call";
    }),
  );
}

function CompactToolCall({ toolCall }: { toolCall: ToolCallMessagePart }) {
  const status = getToolStatus(toolCall);
  const statusColor =
    status === "error" ? "red" : status === "done" ? "green" : "yellow";
  const args = toolCall.argsText || stringifyCompact(toolCall.args);
  const result =
    "result" in toolCall ? stringifyCompact(toolCall.result) : undefined;

  return (
    <Box flexDirection="column" marginY={0}>
      <Box>
        <Text color={statusColor}>[{status}] </Text>
        <Text color="yellow">{toolCall.toolName}</Text>
        <Text dimColor> #{shortId(toolCall.toolCallId)}</Text>
      </Box>
      {args ? (
        <Text dimColor wrap="wrap">
          args: {truncateLines(formatJsonish(args), 4)}
        </Text>
      ) : null}
      {result !== undefined ? (
        <Text wrap="wrap" color={toolCall.isError ? "red" : undefined}>
          result: {truncateLines(formatJsonish(result), 6)}
        </Text>
      ) : null}
    </Box>
  );
}

function getToolStatus(toolCall: ToolCallMessagePart): "running" | "done" | "error" {
  if (toolCall.isError) return "error";
  if ("result" in toolCall && toolCall.result !== undefined) return "done";
  return "running";
}

function stringifyCompact(value: unknown): string {
  if (value === undefined) return "";
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function formatJsonish(value: string): string {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

function truncateLines(value: string, maxLines: number): string {
  const lines = value.split("\n");
  if (lines.length <= maxLines) return value;
  return `${lines.slice(0, maxLines).join("\n")}\n...`;
}

function shortId(value: string | undefined): string {
  if (!value) return "tool";
  if (value.length <= 8) return value;
  return value.slice(0, 8);
}
