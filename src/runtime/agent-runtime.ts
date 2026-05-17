import { useCallback, useMemo, useState } from "react";
import { useAgUiRuntime } from "@assistant-ui/react-ag-ui";
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

    const response = await originalFetch(input, init);

    if (normalizeUrl(rawUrl) !== targetUrl || !response.body) return response;
    const contentType = response.headers.get("content-type") ?? "";
    if (!contentType.includes("event-stream")) return response;

    const ignoredTextMessageIds = getHistoricalAssistantMessageIds(init?.body);

    return new Response(rewriteSseStream(response.body, ignoredTextMessageIds), {
      status: response.status,
      statusText: response.statusText,
      headers: cloneStreamHeaders(response.headers),
    });
  }) as typeof fetch;
}

installFetchPatch();

function rewriteSseStream(
  body: ReadableStream<Uint8Array>,
  ignoredTextMessageIds: ReadonlySet<string>,
): ReadableStream<Uint8Array> {
  const decoder = new TextDecoder();
  const encoder = new TextEncoder();
  let buffer = "";

  const transformer = new TransformStream<Uint8Array, Uint8Array>({
    transform(chunk, controller) {
      buffer += decoder.decode(chunk, { stream: true });
      let idx = findSseEventBoundary(buffer);

      while (idx !== -1) {
        const eventText = buffer.slice(0, idx.eventEnd);
        const rewritten = rewriteSseEvent(eventText, ignoredTextMessageIds);
        if (rewritten !== undefined) {
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
        const rewritten = rewriteSseEvent(buffer, ignoredTextMessageIds);
        if (rewritten !== undefined) {
          controller.enqueue(encoder.encode(rewritten));
        }
        buffer = "";
      }
    },
  });

  return body.pipeThrough(transformer);
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

function rewriteSseEvent(
  eventText: string,
  ignoredTextMessageIds: ReadonlySet<string>,
): string | undefined {
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

  if (dataLineIndexes.length === 0) return eventText;

  const data = dataParts.join("\n");
  const rewritten = rewriteJsonData(data, ignoredTextMessageIds);

  if (rewritten === undefined) return undefined;

  if (rewritten === data) return eventText;

  const skipDataIndexes = new Set(dataLineIndexes.slice(1));
  const rewrittenLines = lines.flatMap((line, index) => {
    if (index === dataLineIndexes[0]) return [`${firstDataPrefix}${rewritten}`];
    if (skipDataIndexes.has(index)) return [];
    return [line];
  });

  return rewrittenLines.join(eventText.includes("\r\n") ? "\r\n" : "\n");
}

function rewriteJsonData(
  data: string,
  ignoredTextMessageIds: ReadonlySet<string>,
): string | undefined {
  try {
    const parsed = JSON.parse(data) as unknown;
    if (shouldDropHistoricalTextMessageEvent(parsed, ignoredTextMessageIds)) {
      return undefined;
    }
    const rewritten = normalizeAgUiRoles(parsed);
    return rewritten.changed ? JSON.stringify(rewritten.value) : data;
  } catch {
    return data;
  }
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
    ignoredTextMessageIds.has(value.messageId)
  );
}

function getHistoricalAssistantMessageIds(body: unknown): Set<string> {
  if (typeof body !== "string") return new Set();

  try {
    const parsed = JSON.parse(body) as unknown;
    if (!isRecord(parsed) || !Array.isArray(parsed.messages)) return new Set();

    const ids = parsed.messages
      .filter((message): message is Record<string, unknown> => isRecord(message))
      .filter((message) => message.role === "assistant")
      .map((message) => message.id)
      .filter((id): id is string => typeof id === "string" && id.length > 0);

    return new Set(ids);
  } catch {
    return new Set();
  }
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

  return useAgUiRuntime({
    agent,
    adapters: {
      threadList: {
        threadId,
        onSwitchToNewThread: handleSwitchToNewThread,
      },
    },
    showThinking: true,
    onError: (e) => {
      console.error("[ag-ui]", e.message);
    },
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
