import { useCallback, useMemo, useState } from "react";
import { useAgUiRuntime } from "../vendor/react-ag-ui/index.js";
import { HttpAgent } from "@ag-ui/client";
import type { RunAgentInput, RunAgentParameters } from "@ag-ui/client";

const DEFAULT_ENDPOINT = "http://localhost:8000/agent";

const ALLOWED_ROLES = new Set([
  "developer",
  "system",
  "assistant",
  "user",
  "tool",
  "activity",
  "reasoning",
]);

const KNOWN_REMAPS: Record<string, string> = {
  ai: "assistant",
  human: "user",
  function: "tool",
};

const seenRoleRewrites = new Set<string>();

type CompatRunAgentInput = RunAgentInput & {
  resume?: unknown;
};

type HistoricalAssistantMessages = {
  ids: ReadonlySet<string>;
  texts: ReadonlySet<string>;
  orderedTexts: readonly string[];
};

function normalizeUrl(raw: string): string {
  try {
    const url = new URL(raw);
    return `${url.origin}${url.pathname.replace(/\/+$/, "")}`;
  } catch {
    return raw.replace(/[?#].*$/, "").replace(/\/+$/, "");
  }
}

function installFetchPatch() {
  const g = globalThis as typeof globalThis & { __deerflowFetchPatched?: boolean };
  if (g.__deerflowFetchPatched) return;
  g.__deerflowFetchPatched = true;

  const originalFetch = globalThis.fetch;

  type FetchInput = Parameters<typeof fetch>[0];
  type FetchInit = Parameters<typeof fetch>[1];

  globalThis.fetch = (async (input: FetchInput, init?: FetchInit) => {
    const targetUrl = normalizeUrl(process.env.AG_UI_ENDPOINT ?? DEFAULT_ENDPOINT);
    const rawUrl =
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.href
          : (input as Request).url;

    let requestBody: any = init?.body;
    if (!requestBody && input && typeof input === "object" && "clone" in input) {
      try {
        const clonedRequest = (input as Request).clone();
        requestBody = await clonedRequest.text();
      } catch {
        // Ignore requests whose bodies cannot be cloned.
      }
    }

    const response = await originalFetch(input, init);

    if (normalizeUrl(rawUrl) !== targetUrl || !response.body) return response;
    const contentType = response.headers.get("content-type") ?? "";
    if (!contentType.includes("event-stream")) return response;

    const historicalAssistantMessages = getHistoricalAssistantMessages(requestBody);

    return new Response(rewriteSseStream(response.body, historicalAssistantMessages), {
      status: response.status,
      statusText: response.statusText,
      headers: cloneStreamHeaders(response.headers),
    });
  }) as typeof fetch;
}

installFetchPatch();

function rewriteSseStream(
  body: ReadableStream<Uint8Array>,
  historicalAssistantMessages: HistoricalAssistantMessages,
): ReadableStream<Uint8Array> {
  const decoder = new TextDecoder();
  const encoder = new TextEncoder();
  let buffer = "";
  const rewriter = new SseEventRewriter(historicalAssistantMessages);

  const transformer = new TransformStream<Uint8Array, Uint8Array>({
    transform(chunk, controller) {
      buffer += decoder.decode(chunk, { stream: true });
      let idx = findSseEventBoundary(buffer);

      while (idx !== -1) {
        const eventText = buffer.slice(0, idx.eventEnd);
        for (const rewritten of rewriter.rewrite(eventText)) {
          controller.enqueue(encoder.encode(`${rewritten}\n\n`));
        }
        buffer = buffer.slice(idx.nextEventStart);
        idx = findSseEventBoundary(buffer);
      }
    },
    flush(controller) {
      const tail = decoder.decode();
      if (tail) buffer += tail;
      if (buffer.length > 0) {
        for (const rewritten of rewriter.rewrite(buffer)) {
          controller.enqueue(encoder.encode(`${rewritten}\n\n`));
        }
        buffer = "";
      }
      for (const rewritten of rewriter.flush()) {
        controller.enqueue(encoder.encode(`${rewritten}\n\n`));
      }
    },
  });

  return body.pipeThrough(transformer);
}

type ParsedSseEvent = {
  eventText: string;
  data: string;
  value: unknown;
  serialize: (data: string) => string;
};

type BufferedTextMessage = {
  events: string[];
  text: string;
  startEvent?: string;
  endEvent?: string;
  contentTemplate?: Record<string, unknown>;
};

class SseEventRewriter {
  private readonly historicalIds: ReadonlySet<string>;
  private readonly historicalTexts: ReadonlySet<string>;
  private readonly orderedHistoricalTexts: readonly string[];
  private readonly pendingTextMessages = new Map<string, BufferedTextMessage>();

  constructor(history: HistoricalAssistantMessages) {
    this.historicalIds = history.ids;
    this.historicalTexts = history.texts;
    this.orderedHistoricalTexts = history.orderedTexts;
  }

  rewrite(eventText: string): string[] {
    const parsed = parseSseEvent(eventText);
    if (!parsed) return [eventText];

    const rewritten = rewriteJsonValue(parsed.value);
    if (rewritten === undefined) return [];

    const normalizedEvent =
      rewritten.changed ? parsed.serialize(JSON.stringify(rewritten.value)) : eventText;

    if (shouldDropHistoricalTextMessageEvent(rewritten.value, this.historicalIds)) {
      return [];
    }

    const textEvent = getTextMessageEvent(rewritten.value);
    if (!textEvent) return [normalizedEvent];

    const key = textEvent.messageId ?? "__default_text_message__";
    let pending = this.pendingTextMessages.get(key);

    if (!pending) {
      pending = { events: [], text: "" };
      this.pendingTextMessages.set(key, pending);
    }

    pending.events.push(normalizedEvent);
    if (textEvent.delta) pending.text += textEvent.delta;
    if (textEvent.kind === "start") pending.startEvent = normalizedEvent;
    if (textEvent.kind === "end") pending.endEvent = normalizedEvent;
    if (textEvent.kind === "content" && isRecord(rewritten.value)) {
      pending.contentTemplate = rewritten.value;
    }

    if (textEvent.kind !== "end") return [];

    this.pendingTextMessages.delete(key);
    if (this.historicalTexts.has(normalizeHistoricalTextContent(pending.text))) {
      return [];
    }

    const strippedText = stripHistoricalReplayPrefix(
      pending.text,
      this.orderedHistoricalTexts,
    );
    if (strippedText !== pending.text) {
      if (!strippedText.trim()) return [];
      return serializeStrippedTextMessage(pending, strippedText);
    }

    return pending.events;
  }

  flush(): string[] {
    const events = Array.from(this.pendingTextMessages.values()).flatMap(
      (entry) => entry.events,
    );
    this.pendingTextMessages.clear();
    return events;
  }
}

function findSseEventBoundary(
  text: string,
): { eventEnd: number; nextEventStart: number } | -1 {
  const match = /\r?\n\r?\n/.exec(text);
  if (!match || match.index === undefined) return -1;
  return {
    eventEnd: match.index,
    nextEventStart: match.index + match[0].length,
  };
}

function cloneStreamHeaders(headers: Headers): Headers {
  const cloned = new Headers(headers);
  cloned.delete("content-length");
  cloned.delete("content-encoding");
  return cloned;
}

function parseSseEvent(eventText: string): ParsedSseEvent | null {
  const lines = eventText.split(/\r?\n/);
  const dataLineIndexes: number[] = [];
  const dataParts: string[] = [];
  let firstDataPrefix = "data:";

  lines.forEach((line, index) => {
    if (!line.startsWith("data:")) return;
    const dataPrefix = line.match(/^data:\s*/)?.[0] ?? "data:";
    if (dataLineIndexes.length === 0) firstDataPrefix = dataPrefix;
    dataLineIndexes.push(index);
    dataParts.push(line.slice(dataPrefix.length));
  });

  if (dataLineIndexes.length === 0) return null;

  const data = dataParts.join("\n");

  try {
    const value = JSON.parse(data) as unknown;

    return {
      eventText,
      data,
      value,
      serialize: (rewrittenData: string) => {
        if (rewrittenData === data) return eventText;

        const skipDataIndexes = new Set(dataLineIndexes.slice(1));
        const rewrittenLines = lines.flatMap((line, index) => {
          if (index === dataLineIndexes[0]) return [`${firstDataPrefix}${rewrittenData}`];
          if (skipDataIndexes.has(index)) return [];
          return [line];
        });

        return rewrittenLines.join(eventText.includes("\r\n") ? "\r\n" : "\n");
      },
    };
  } catch {
    return null;
  }
}

function rewriteJsonValue(value: unknown): { value: unknown; changed: boolean } | undefined {
  if (isRecord(value) && value.type === "MESSAGES_SNAPSHOT") {
    return undefined;
  }
  return normalizeAgUiRoles(value);
}

function getTextMessageEvent(
  value: unknown,
): { kind: "start" | "content" | "end"; messageId?: string; delta?: string } | null {
  if (!isRecord(value)) return null;

  if (value.type === "TEXT_MESSAGE_START") {
    return {
      kind: "start",
      ...(typeof value.messageId === "string" ? { messageId: value.messageId } : {}),
    };
  }

  if (value.type === "TEXT_MESSAGE_CONTENT" || value.type === "TEXT_MESSAGE_CHUNK") {
    return {
      kind: "content",
      ...(typeof value.messageId === "string" ? { messageId: value.messageId } : {}),
      ...(typeof value.delta === "string" ? { delta: value.delta } : {}),
    };
  }

  if (value.type === "TEXT_MESSAGE_END") {
    return {
      kind: "end",
      ...(typeof value.messageId === "string" ? { messageId: value.messageId } : {}),
    };
  }

  return null;
}

function stripHistoricalReplayPrefix(
  text: string,
  orderedHistoricalTexts: readonly string[],
): string {
  let remaining = text;

  for (const historicalText of orderedHistoricalTexts) {
    const trimmedHistory = historicalText.trim();
    if (!trimmedHistory) continue;

    const leadingWhitespace = remaining.match(/^\s*/)?.[0] ?? "";
    const candidate = remaining.slice(leadingWhitespace.length);

    if (!candidate.startsWith(trimmedHistory)) {
      continue;
    }

    remaining = candidate.slice(trimmedHistory.length);
  }

  return remaining.replace(/^\s+/, "");
}

function serializeStrippedTextMessage(
  pending: BufferedTextMessage,
  text: string,
): string[] {
  if (!pending.contentTemplate) return pending.events;

  const contentEvent = {
    ...pending.contentTemplate,
    delta: text,
  };

  return [
    ...(pending.startEvent ? [pending.startEvent] : []),
    `data: ${JSON.stringify(contentEvent)}`,
    ...(pending.endEvent ? [pending.endEvent] : []),
  ];
}

function shouldDropHistoricalTextMessageEvent(
  value: unknown,
  ignoredTextMessageIds: ReadonlySet<string>,
): boolean {
  if (ignoredTextMessageIds.size === 0 || !isRecord(value)) return false;
  if (
    value.type !== "TEXT_MESSAGE_START" &&
    value.type !== "TEXT_MESSAGE_CONTENT" &&
    value.type !== "TEXT_MESSAGE_CHUNK" &&
    value.type !== "TEXT_MESSAGE_END"
  ) {
    return false;
  }

  return (
    typeof value.messageId === "string" &&
    ignoredTextMessageIds.has(normalizeHistoricalTextMessageId(value.messageId))
  );
}

function normalizeHistoricalTextMessageId(id: string): string {
  const parts = id.split(":");
  if (parts.length >= 3) {
    return parts.slice(2).join(":");
  }
  return id;
}

function getHistoricalAssistantMessages(body: unknown): HistoricalAssistantMessages {
  const empty: HistoricalAssistantMessages = {
    ids: new Set(),
    texts: new Set(),
    orderedTexts: [],
  };

  if (!body) return empty;

  let bodyStr = "";
  if (typeof body === "string") {
    bodyStr = body;
  } else if (body instanceof Uint8Array || body instanceof ArrayBuffer) {
    try {
      bodyStr = new TextDecoder().decode(body);
    } catch {
      return empty;
    }
  } else {
    try {
      bodyStr = String(body);
    } catch {
      return empty;
    }
  }

  try {
    const parsed = JSON.parse(bodyStr) as unknown;
    if (!isRecord(parsed) || !Array.isArray(parsed.messages)) {
      return empty;
    }

    const assistantMessages = parsed.messages
      .filter((message): message is Record<string, unknown> => isRecord(message))
      .filter((message) => message.role === "assistant" || message.role === "ai");

    const ids = assistantMessages
      .map((message) => message.id)
      .filter((id): id is string => typeof id === "string" && id.length > 0);

    const orderedTexts = assistantMessages
      .map((message) => extractHistoricalTextContent(message.content))
      .filter((text): text is string => text !== undefined && text.length > 0)
      .map(normalizeHistoricalTextContent);

    return {
      ids: new Set(ids.map(normalizeHistoricalTextMessageId)),
      texts: new Set(orderedTexts),
      orderedTexts,
    };
  } catch {
    return empty;
  }
}

function extractHistoricalTextContent(content: unknown): string | undefined {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return undefined;

  const text = content
    .filter(
      (part): part is { type: "text"; text: string } =>
        isRecord(part) && part.type === "text" && typeof part.text === "string",
    )
    .map((part) => part.text)
    .join("\n");

  return text.length > 0 ? text : undefined;
}

function normalizeHistoricalTextContent(text: string): string {
  return text.replace(/\r\n/g, "\n").trim();
}

function normalizeAgUiRoles(value: unknown): { value: unknown; changed: boolean } {
  if (!value || typeof value !== "object") {
    return { value, changed: false };
  }

  if (Array.isArray(value)) {
    return normalizeMessages(value);
  }

  const event = value as Record<string, unknown>;
  let changed = false;
  const output: Record<string, unknown> = { ...event };

  if (typeof event.role === "string" && !ALLOWED_ROLES.has(event.role)) {
    output.role = mapRole(event.role);
    changed = true;
  }

  if (Array.isArray(event.messages)) {
    const rewritten = normalizeMessages(event.messages);
    output.messages = rewritten.value;
    changed ||= rewritten.changed;
  }

  if (isRecord(event.input) && Array.isArray(event.input.messages)) {
    const rewritten = normalizeMessages(event.input.messages);
    output.input = { ...event.input, messages: rewritten.value };
    changed ||= rewritten.changed;
  }

  return { value: changed ? output : value, changed };
}

function normalizeMessages(messages: unknown[]): { value: unknown[]; changed: boolean } {
  let changed = false;
  const output = messages.map((message) => {
    if (!isRecord(message)) return message;

    const role = message.role;
    if (typeof role !== "string" || ALLOWED_ROLES.has(role)) return message;

    changed = true;
    return { ...message, role: mapRole(role) };
  });

  return { value: changed ? output : messages, changed };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function mapRole(role: string): string {
  const mapped = KNOWN_REMAPS[role] ?? "user";
  const key = `${role}->${mapped}`;

  if (!seenRoleRewrites.has(key)) {
    seenRoleRewrites.add(key);
    console.error(`[deerflow-tui] SSE role rewrite: "${role}" -> "${mapped}"`);
  }

  return mapped;
}

class CompatHttpAgent extends HttpAgent {
  private readonly fallbackState: Record<string, unknown>;

  constructor(config: ConstructorParameters<typeof HttpAgent>[0] & {
    fallbackState?: Record<string, unknown>;
  }) {
    super(config);
    this.fallbackState = config.fallbackState ?? {};
  }

  protected override prepareRunAgentInput(
    parameters?: RunAgentParameters,
  ): RunAgentInput {
    const input = super.prepareRunAgentInput(parameters);

    if (isRunAgentInput(parameters)) {
      const compatParameters = parameters as CompatRunAgentInput;
      return {
        ...input,
        runId: compatParameters.runId,
        threadId: compatParameters.threadId,
        state: compatParameters.state,
        messages: compatParameters.messages,
        tools: compatParameters.tools,
        context: compatParameters.context,
        forwardedProps: compatParameters.forwardedProps,
        ...(compatParameters.resume !== undefined
          ? { resume: compatParameters.resume }
          : {}),
      };
    }

    return input;
  }

  protected override requestInit(input: RunAgentInput): RequestInit {
    const safeInput: RunAgentInput = {
      ...input,
      state:
        input.state === null || input.state === undefined
          ? this.fallbackState
          : input.state,
    };

    return {
      method: "POST",
      headers: {
        ...this.headers,
        "Content-Type": "application/json",
        Accept: "text/event-stream",
      },
      body: JSON.stringify(safeInput),
      signal: this.abortController.signal,
    };
  }
}

function isRunAgentInput(value: unknown): value is RunAgentInput {
  return (
    isRecord(value) &&
    typeof value.runId === "string" &&
    typeof value.threadId === "string" &&
    Array.isArray(value.messages)
  );
}

export function useAgentRuntime() {
  const [threadId, setThreadId] = useState(() => createThreadId());

  const handleSwitchToNewThread = useCallback(() => {
    setThreadId(createThreadId());
  }, []);

  const agent = useMemo(
    () =>
      new CompatHttpAgent({
        threadId,
        url: process.env.AG_UI_ENDPOINT ?? DEFAULT_ENDPOINT,
        headers: parseHeaders(process.env.AG_UI_HEADERS),
        fallbackState: parseInitialState(process.env.AG_UI_INITIAL_STATE) ?? {},
      }),
    [threadId],
  );

  const adapters = useMemo(
    () => ({
      threadList: {
        threadId,
        onSwitchToNewThread: handleSwitchToNewThread,
      },
    }),
    [threadId, handleSwitchToNewThread],
  );

  const onError = useCallback((e: Error) => {
    console.error("[ag-ui]", e.message);
  }, []);

  return useAgUiRuntime({
    agent,
    adapters,
    showThinking: true,
    onError,
  });
}

function createThreadId(): string {
  return `thread-${crypto.randomUUID()}`;
}

function parseHeaders(raw: string | undefined): Record<string, string> | undefined {
  if (!raw) return undefined;
  try {
    const parsed = JSON.parse(raw);
    if (isStringRecord(parsed)) return parsed;
  } catch {
    // Ignore malformed JSON.
  }
  return undefined;
}

function parseInitialState(raw: string | undefined): Record<string, unknown> | undefined {
  if (!raw) return undefined;
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    // Ignore malformed JSON.
  }
  return undefined;
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return (
    isRecord(value) &&
    Object.values(value).every((item) => typeof item === "string")
  );
}
