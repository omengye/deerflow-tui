"use client";

import type {
  ChatModelRunResult,
  ThreadAssistantMessagePart,
  ToolCallMessagePart,
} from "@assistant-ui/core";
import type { AgUiEvent, AgUiInterrupt } from "../types.js";
import type { Logger } from "../logger.js";

export const AG_UI_METADATA_NAMESPACE = "agui";

export type AgUiCustomMetadata = {
  interrupts?: AgUiInterrupt[];
  agentName?: string;
};

type Emit = (update: ChatModelRunResult) => void;

type ToolCallState = {
  toolCallId: string;
  toolCallName: string;
  argsText: string;
  parsedArgs: Record<string, unknown> | undefined;
  result: unknown;
  isError: boolean | undefined;
  parentMessageId?: string;
  toolMessageId?: string;
};

type ReasoningState = {
  buffer: string;
  active: boolean;
  eventType: "REASONING" | "THINKING";
  messageId?: string;
};

function normalizeReplayText(text: string): string {
  return text.replace(/\r\n/g, "\n").trim();
}

function filterReplayText(
  buffer: string,
  historicalTexts: readonly string[],
): string | undefined {
  const normalized = normalizeReplayText(buffer);
  if (!normalized) return undefined;

  for (const historicalText of historicalTexts) {
    if (!historicalText) continue;
    if (historicalText === normalized || historicalText.startsWith(normalized)) {
      return undefined;
    }
    if (normalized.startsWith(historicalText)) {
      const remainder = normalized.slice(historicalText.length).trimStart();
      return remainder.length > 0 ? remainder : undefined;
    }
  }

  return buffer;
}

export type RunAggregatorOptions = {
  showThinking: boolean;
  logger: Logger;
  emit: Emit;
  onServerMessageId?: (messageId: string) => void;
  replayFilter?: {
    texts: readonly string[];
    reasoning: readonly string[];
    toolCallIds: ReadonlySet<string>;
  };
};

/**
 * Collects AG-UI events into assistant-ui run snapshots that can be yielded from a ChatModelAdapter.
 *
 * The aggregator keeps a single assistant message worth of parts. Each incoming event updates the parts and
 * emits a fresh snapshot through the provided `emit` callback.
 */
export class RunAggregator {
  private readonly emitUpdate: Emit;
  private readonly showThinking: boolean;
  private readonly logger: Logger;
  private readonly onServerMessageId: ((messageId: string) => void) | undefined;
  private readonly replayFilter: NonNullable<RunAggregatorOptions["replayFilter"]>;

  private status: ChatModelRunResult["status"] | undefined;
  private interrupts: AgUiInterrupt[] | undefined;
  private readonly textParts = new Map<
    string,
    { buffer: string; touched: boolean }
  >();
  private activeTextMessageId: string | undefined;
  private readonly completedTextMessageIds = new Set<string>();
  private readonly ignoredTextMessageIds = new Set<string>();
  private readonly reasoningParts = new Map<string, ReasoningState>();
  private activeReasoningId: string | undefined;
  private readonly toolCalls = new Map<string, ToolCallState>();
  private readonly ignoredToolCallIds = new Set<string>();
  private readonly partOrder: (
    | { kind: "text"; key: string }
    | { kind: "reasoning"; key: string }
    | { kind: "tool-call"; toolCallId: string }
  )[] = [];
  private textPartCounter = 0;
  private reasoningPartCounter = 0;
  private serverMessageIdReported = false;
  private agentName: string | undefined;

  constructor(options: RunAggregatorOptions) {
    this.emitUpdate = options.emit;
    this.showThinking = options.showThinking;
    this.logger = options.logger;
    this.onServerMessageId = options.onServerMessageId;
    this.replayFilter = options.replayFilter ?? {
      texts: [],
      reasoning: [],
      toolCallIds: new Set(),
    };
  }

  handle(event: AgUiEvent): void {
    if (event.agentName) {
      this.agentName = event.agentName;
    }

    switch (event.type) {
      case "RUN_STARTED": {
        this.clearTextParts();
        this.completedTextMessageIds.clear();
        this.ignoredTextMessageIds.clear();
        this.reasoningParts.clear();
        this.toolCalls.clear();
        this.ignoredToolCallIds.clear();
        this.partOrder.length = 0;
        this.textPartCounter = 0;
        this.reasoningPartCounter = 0;
        this.activeTextMessageId = undefined;
        this.activeReasoningId = undefined;
        this.interrupts = undefined;
        this.serverMessageIdReported = false;
        this.agentName = undefined;
        this.status = { type: "running" };
        this.emit();
        break;
      }
      case "RUN_FINISHED": {
        if (event.outcome?.type === "interrupt") {
          this.interrupts = event.outcome.interrupts;
          this.status = { type: "requires-action", reason: "interrupt" };
          this.emit();
          break;
        }

        this.interrupts = undefined;
        const hasUnresolvedToolCalls = Array.from(this.toolCalls.values()).some(
          (tc) => tc.result === undefined,
        );

        this.status =
          event.outcome?.type === "success" || !hasUnresolvedToolCalls
            ? { type: "complete", reason: "unknown" }
            : { type: "requires-action", reason: "tool-calls" };
        this.emit();
        break;
      }
      case "RUN_ERROR": {
        this.status = {
          type: "incomplete",
          reason: "error",
          ...(event.message !== undefined ? { error: event.message } : {}),
        };
        this.emit();
        break;
      }
      case "RUN_CANCELLED": {
        this.status = { type: "incomplete", reason: "cancelled" };
        this.emit();
        break;
      }

      case "TEXT_MESSAGE_START": {
        this.reportServerMessageId(event.messageId);
        if (this.shouldIgnoreTextMessageStart(event.messageId)) break;
        const id = this.startTextMessage(event.messageId);
        if (id) {
          this.markTextPartTouched(id);
        }
        this.emit();
        break;
      }
      case "TEXT_MESSAGE_CONTENT":
      case "TEXT_MESSAGE_CHUNK": {
        const incomingId = "messageId" in event ? event.messageId : undefined;
        this.reportServerMessageId(incomingId);
        if (this.shouldIgnoreTextMessageContent(incomingId)) break;
        if (!event.delta) break;
        const id = this.resolveTextMessageId(incomingId);
        this.appendText(id, event.delta);
        this.emit();
        break;
      }
      case "TEXT_MESSAGE_END": {
        this.reportServerMessageId(event.messageId);
        if (event.messageId && this.ignoredTextMessageIds.has(event.messageId)) {
          this.ignoredTextMessageIds.delete(event.messageId);
          break;
        }
        if (event.messageId) {
          this.completedTextMessageIds.add(event.messageId);
        }
        if (event.messageId && this.activeTextMessageId === event.messageId) {
          this.activeTextMessageId = undefined;
        }
        this.emit();
        break;
      }

      case "THINKING_START":
      case "THINKING_TEXT_MESSAGE_START":
      case "REASONING_START":
      case "REASONING_MESSAGE_START":
        this.handleReasoningStart(event);
        break;
      case "THINKING_TEXT_MESSAGE_CONTENT":
      case "REASONING_MESSAGE_CONTENT":
        this.handleReasoningContent(event);
        break;
      case "THINKING_TEXT_MESSAGE_END":
      case "THINKING_END":
      case "REASONING_MESSAGE_END":
      case "REASONING_END":
        this.handleReasoningEnd(event);
        break;

      case "TOOL_CALL_START": {
        this.reportServerMessageId(event.parentMessageId);
        this.startToolCall(
          event.toolCallId,
          event.toolCallName,
          event.parentMessageId,
        );
        this.emit();
        break;
      }
      case "TOOL_CALL_ARGS":
      case "TOOL_CALL_CHUNK": {
        if (event.type === "TOOL_CALL_CHUNK") {
          this.reportServerMessageId(event.parentMessageId);
        }
        if (event.toolCallId && this.ignoredToolCallIds.has(event.toolCallId)) {
          break;
        }
        if (!event.delta) break;
        this.appendToolArgs(event.toolCallId, event.delta);
        this.emit();
        break;
      }
      case "TOOL_CALL_END": {
        if (event.toolCallId && this.ignoredToolCallIds.has(event.toolCallId)) {
          this.ignoredToolCallIds.delete(event.toolCallId);
          break;
        }
        this.emit();
        break;
      }
      case "TOOL_CALL_RESULT": {
        if (this.ignoredToolCallIds.has(event.toolCallId)) {
          break;
        }
        this.finishToolCall(
          event.toolCallId,
          event.content ?? "",
          event.role === "tool" ? false : undefined,
          event.messageId,
        );
        this.emit();
        break;
      }

      default: {
        if (event.type === "RAW" && event.event && typeof event.event === "object" && typeof event.event.name === "string") {
          this.agentName = event.event.name;
          this.emit();
        }
        this.logger.debug?.("[agui] aggregator ignored event", event);
      }
    }
  }

  private reportServerMessageId(messageId: string | undefined): void {
    if (this.serverMessageIdReported || !messageId) return;
    this.serverMessageIdReported = true;
    this.onServerMessageId?.(messageId);
  }

  private clearTextParts(): void {
    this.textParts.clear();
  }

  private generateTextKey(): string {
    this.textPartCounter += 1;
    return `text-${this.textPartCounter}`;
  }

  private shouldIgnoreTextMessageStart(messageId?: string): boolean {
    if (!messageId) return false;
    if (!this.completedTextMessageIds.has(messageId)) return false;
    this.ignoredTextMessageIds.add(messageId);
    this.logger.debug?.("[agui] ignoring duplicate TEXT_MESSAGE sequence", {
      messageId,
    });
    return true;
  }

  private shouldIgnoreTextMessageContent(messageId?: string): boolean {
    if (!messageId) return false;
    if (this.ignoredTextMessageIds.has(messageId)) return true;
    if (!this.completedTextMessageIds.has(messageId)) return false;
    this.logger.debug?.("[agui] ignoring duplicate TEXT_MESSAGE content", {
      messageId,
    });
    return true;
  }

  private startTextMessage(messageId?: string): string {
    const id = messageId ?? this.generateTextKey();
    this.ensureTextPart(id);
    this.activeTextMessageId = id;
    return id;
  }

  private resolveTextMessageId(messageId?: string): string {
    if (messageId) {
      this.ensureTextPart(messageId);
      this.activeTextMessageId = messageId;
      return messageId;
    }

    if (this.activeTextMessageId) {
      return this.activeTextMessageId;
    }

    const generated = this.generateTextKey();
    this.ensureTextPart(generated);
    this.activeTextMessageId = generated;
    return generated;
  }

  private ensureTextPart(id: string): void {
    if (!this.textParts.has(id)) {
      this.textParts.set(id, { buffer: "", touched: false });
      if (
        !this.partOrder.some((part) => part.kind === "text" && part.key === id)
      ) {
        this.partOrder.push({ kind: "text", key: id });
      }
    }
  }

  private markTextPartTouched(id: string): void {
    const entry = this.textParts.get(id);
    if (!entry) return;
    entry.touched = true;
  }

  private appendText(id: string, delta: string): void {
    this.ensureTextPart(id);
    const entry = this.textParts.get(id);
    if (!entry) return;
    entry.buffer += delta;
    entry.touched = true;
  }

  private startToolCall(
    id: string | undefined,
    name?: string,
    parentMessageId?: string,
  ) {
    if (!id) return;
    if (this.replayFilter.toolCallIds.has(id)) {
      this.ignoredToolCallIds.add(id);
      return;
    }
    if (
      !this.partOrder.some(
        (part) => part.kind === "tool-call" && part.toolCallId === id,
      )
    ) {
      this.partOrder.push({ kind: "tool-call", toolCallId: id });
    }
    const state: ToolCallState = {
      toolCallId: id,
      toolCallName: name ?? "tool",
      argsText: "",
      parsedArgs: undefined,
      result: undefined,
      isError: undefined,
    };
    if (parentMessageId) {
      state.parentMessageId = parentMessageId;
    }
    this.toolCalls.set(id, state);
  }

  private appendToolArgs(id: string | undefined, delta: string) {
    const entry = id ? this.toolCalls.get(id) : undefined;
    if (!entry) return;
    entry.argsText += delta;
    try {
      const parsed = JSON.parse(entry.argsText);
      if (parsed && typeof parsed === "object") {
        entry.parsedArgs = parsed as Record<string, unknown>;
      } else {
        entry.parsedArgs = undefined;
      }
    } catch {
      entry.parsedArgs = undefined;
    }
  }

  private finishToolCall(
    id: string,
    content: string,
    isError?: boolean,
    toolMessageId?: string,
  ) {
    if (!id) return;
    let entry = this.toolCalls.get(id);
    if (!entry) {
      entry = {
        toolCallId: id,
        toolCallName: "tool",
        argsText: "",
        parsedArgs: undefined,
        result: undefined,
        isError: undefined,
      };
      this.toolCalls.set(id, entry);
    }
    if (
      !this.partOrder.some(
        (part) => part.kind === "tool-call" && part.toolCallId === id,
      )
    ) {
      this.partOrder.push({ kind: "tool-call", toolCallId: id });
    }
    entry.result = this.tryParseJSON(content);
    entry.isError = isError;
    if (toolMessageId) {
      entry.toolMessageId = toolMessageId;
    }
  }

  private tryParseJSON(value: string): unknown {
    if (!value) return value;
    try {
      return JSON.parse(value);
    } catch {
      return value;
    }
  }

  private emit(): void {
    const snapshot: ThreadAssistantMessagePart[] = [];

    for (const part of this.partOrder) {
      if (part.kind === "reasoning") {
        const entry = this.reasoningParts.get(part.key);
        const text = entry
          ? filterReplayText(entry.buffer, this.replayFilter.reasoning)
          : undefined;
        if (
          entry &&
          this.showThinking &&
          text !== undefined &&
          (entry.active || text.length > 0)
        ) {
          snapshot.push({
            type: "reasoning",
            text,
            unstable_agui: {
              eventType: entry.eventType,
              messageId: entry.messageId ?? part.key,
            },
          } as ThreadAssistantMessagePart & {
            unstable_agui: {
              eventType: "REASONING" | "THINKING";
              messageId: string;
            };
          });
        }
        continue;
      }

      if (part.kind === "text") {
        const entry = this.textParts.get(part.key);
        const text = entry
          ? filterReplayText(entry.buffer, this.replayFilter.texts)
          : undefined;
        if (entry?.touched && text !== undefined) {
          snapshot.push({
            type: "text",
            text,
            unstable_agui: {
              eventType: "TEXT_MESSAGE",
              messageId: part.key,
            },
          } as ThreadAssistantMessagePart & {
            unstable_agui: {
              eventType: "TEXT_MESSAGE";
              messageId: string;
            };
          });
        }
        continue;
      }

      const entry = this.toolCalls.get(part.toolCallId);
      if (!entry) continue;
      const toolPart: ToolCallMessagePart = {
        type: "tool-call",
        toolCallId: entry.toolCallId,
        toolName: entry.toolCallName,
        args: (entry.parsedArgs ?? {}) as any,
        argsText: entry.argsText,
        ...(entry.result !== undefined ? { result: entry.result } : {}),
        ...(entry.isError !== undefined ? { isError: entry.isError } : {}),
        ...(entry.parentMessageId ? { parentId: entry.parentMessageId } : {}),
        ...(entry.toolMessageId
          ? { unstable_toolMessageId: entry.toolMessageId }
          : {}),
      } as ToolCallMessagePart & { unstable_toolMessageId?: string };
      snapshot.push(toolPart);
    }

    const result: ChatModelRunResult = {
      content: snapshot,
      ...(this.status ? { status: this.status } : undefined),
      metadata: {
        custom: {
          [AG_UI_METADATA_NAMESPACE]: {
            ...(this.interrupts ? { interrupts: this.interrupts } : {}),
            ...(this.agentName ? { agentName: this.agentName } : {}),
          } satisfies AgUiCustomMetadata,
        },
      },
    };
    this.emitUpdate(result);
  }

  private handleReasoningStart(event: AgUiEvent): void {
    if (!this.showThinking) return;
    const id = this.resolveReasoningId(event);
    const entry = this.reasoningParts.get(id);
    if (entry) {
      entry.active = true;
    }
    this.emit();
  }

  private handleReasoningContent(
    event: Extract<
      AgUiEvent,
      | { type: "THINKING_TEXT_MESSAGE_CONTENT" }
      | { type: "REASONING_MESSAGE_CONTENT" }
    >,
  ): void {
    if (!this.showThinking || !event.delta) return;
    const id = this.resolveReasoningId(event);
    const entry = this.reasoningParts.get(id);
    if (entry) {
      entry.buffer += event.delta;
    }
    this.emit();
  }

  private handleReasoningEnd(event: AgUiEvent): void {
    if (!this.showThinking) return;
    const id = this.resolveReasoningId(event);
    const entry = this.reasoningParts.get(id);
    if (entry) {
      entry.active = false;
    }
    if (this.activeReasoningId === id) {
      this.activeReasoningId = undefined;
    }
    this.emit();
  }

  private resolveReasoningId(event: AgUiEvent): string {
    const messageId = "messageId" in event ? event.messageId : undefined;
    const eventType = event.type.startsWith("REASONING")
      ? "REASONING"
      : "THINKING";

    if (messageId) {
      this.ensureReasoningPart(messageId, eventType, messageId);
      this.activeReasoningId = messageId;
      return messageId;
    }

    if (this.activeReasoningId) {
      return this.activeReasoningId;
    }

    const generated = this.generateReasoningKey(eventType);
    this.ensureReasoningPart(generated, eventType);
    this.activeReasoningId = generated;
    return generated;
  }

  private generateReasoningKey(eventType: "REASONING" | "THINKING"): string {
    this.reasoningPartCounter += 1;
    return `${eventType.toLowerCase()}-${this.reasoningPartCounter}`;
  }

  private ensureReasoningPart(
    id: string,
    eventType: "REASONING" | "THINKING",
    messageId?: string,
  ): void {
    const existing = this.reasoningParts.get(id);
    if (existing) {
      existing.eventType = eventType;
      if (messageId) {
        existing.messageId = messageId;
      }
      return;
    }

    this.reasoningParts.set(id, {
      buffer: "",
      active: false,
      eventType,
      ...(messageId ? { messageId } : {}),
    });

    // ensure reasoning appears before the first text segment if possible
    const textIndex = this.partOrder.findIndex((part) => part.kind === "text");
    if (textIndex === -1) {
      this.partOrder.push({ kind: "reasoning", key: id });
    } else {
      this.partOrder.splice(textIndex, 0, { kind: "reasoning", key: id });
    }
  }
}
