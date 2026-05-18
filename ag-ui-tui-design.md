# AG-UI 终端客户端设计与实现方案

> 基于 Ink + assistant-ui + AG-UI 协议，构建终端内的 AI Agent 流式交互客户端

> **状态更新（vendor 迁移后）**：本文档中所有 `from "@assistant-ui/react-ag-ui"` 的代码示例为历史设计快照。
> 实际实现中 `useAgUiRuntime` 等运行时已 fork 到 `src/vendor/react-ag-ui/`，
> 现代实际 import 路径为 `from "../vendor/react-ag-ui/index.js"`。
> 详情见 `AG_UI_DISPLAY_VENDOR.md`。

---

## 一、方案概述

### 1.1 技术栈选型

```
┌────────────────────────────────────────────────────┐
│                    用户终端                         │
│  ┌──────────────────────────────────────────────┐  │
│  │          TUI 应用 (Node.js + TypeScript)      │  │
│  │  ┌────────────────────────────────────────┐  │  │
│  │  │  Ink 6.x (React 组件 → 终端渲染)        │  │  │
│  │  │  + @assistant-ui/react-ink (0.0.16)    │  │  │
│  │  │  + @assistant-ui/react-ag-ui (0.0.30)  │  │  │
│  │  │  + @assistant-ui/react-ink-markdown    │  │  │
│  │  │  + @inkjs/ui (可选组件库)               │  │  │
│  │  └────────────────────────────────────────┘  │  │
│  └──────────────────────┬───────────────────────┘  │
│                         │                          │
│             AgUiRuntime (useAgUiRuntime)            │
│                         │                          │
│             HttpAgent (SSE consumer)                │
└─────────────────────────┼──────────────────────────┘
                          │ HTTP + SSE
                          │ AG-UI 事件流
┌─────────────────────────┼──────────────────────────┐
│                  AG-UI Server                       │
│  ┌──────────────────────────────────────────────┐  │
│  │  LangGraph / CrewAI / Mastra / 自定义 Agent   │  │
│  └──────────────────────────────────────────────┘  │
│  端点: http://localhost:8000/agent                  │
└────────────────────────────────────────────────────┘
```

### 1.2 核心 npm 包清单

| 包名 | 版本 | 用途 |
|------|------|------|
| `ink` | ^6.x | React → 终端渲染引擎 |
| `react` | ^19 | UI 组件框架 |
| `@assistant-ui/react-ink` | 0.0.16 | assistant-ui 的 Ink 终端绑定 |
| `@assistant-ui/react-ag-ui` | 0.0.30 | AG-UI 协议适配器 |
| `@assistant-ui/react-ink-markdown` | 0.0.15 | 终端 Markdown/代码高亮 |
| `@ag-ui/client` | 0.0.53 | AG-UI 官方客户端（HttpAgent + SSE） |
| `@inkjs/ui` | ^2.0.0 | Ink 官方组件库（Spinner/Select/TextInput 等增强；旧 `ink-ui` 包已弃用） |
| `fullscreen-ink` | 0.1.0 | 全屏终端模式 |

---

## 二、架构设计

### 2.1 分层架构

```
┌─────────────────────────────────────────────┐
│               UI 层 (Presentation)           │
│  ChatPanel  │  ToolPanel  │  StatePanel      │
│  Composer   │  StatusBar  │  Sidebar         │
├─────────────────────────────────────────────┤
│          运行时层 (Runtime Bridge)           │
│  useAgUiRuntime  →  AgUiRuntime             │
│  HttpAgent       →  SSE Event Parser        │
│  Message Store   →  State Store             │
├─────────────────────────────────────────────┤
│          AG-UI 协议层 (Protocol)             │
│  @ag-ui/client   ← SSE stream               │
│  Event: TEXT_MESSAGE / TOOL_CALL / STATE_*   │
└─────────────────────────────────────────────┘
```

### 2.2 数据流

```
用户输入 (Composer)
    │
    ▼
Runtime.append(message)
    │
    ▼
HttpAgent.sendMessage() ─── HTTP POST ───► AG-UI Server
                                              │
    ┌─────────────────────────────────────────┘
    │  SSE Stream (事件流)
    ▼
Event: RUN_STARTED
Event: TEXT_MESSAGE_START
Event: TEXT_MESSAGE_CONTENT (delta)  ──►  逐字更新消息内容 → Ink 重渲染
Event: TEXT_MESSAGE_CONTENT (delta)
Event: TEXT_MESSAGE_END
Event: TOOL_CALL_START                ──►  工具调用卡片出现
Event: TOOL_CALL_ARGS  (streaming)    ──►  参数逐步展示
Event: TOOL_CALL_END
Event: STATE_DELTA  (JSON Patch)      ──►  状态面板增量更新
Event: RUN_FINISHED
    │
    ▼
Runtime 自动重建 Message 对象
    │
    ▼
ThreadPrimitive.Messages 自动重新渲染
```

### 2.3 目录结构

```
ag-ui-tui/
├── package.json
├── tsconfig.json
├── src/
│   ├── entry.tsx                 # 入口 + Ink render()
│   ├── app.tsx                   # App 根组件 (布局编排)
│   ├── runtime/
│   │   └── agent-runtime.ts      # HttpAgent + useAgUiRuntime 封装
│   ├── components/
│   │   ├── chat/
│   │   │   ├── Thread.tsx        # 对话容器 (ScrollView + Messages)
│   │   │   ├── Message.tsx       # 单条消息渲染 (用户/AI/工具调用)
│   │   │   ├── UserMessage.tsx   # 用户消息气泡
│   │   │   ├── AssistantMessage.tsx # AI 回应 (Markdown + 流式)
│   │   │   └── StreamingText.tsx # 流式文字逐字渲染
│   │   ├── tools/
│   │   │   ├── ToolCallCard.tsx  # 工具调用卡片
│   │   │   └── ToolResultCard.tsx # 工具返回结果
│   │   ├── composer/
│   │   │   └── InputBar.tsx      # 输入栏 + 发送按钮
│   │   ├── panels/
│   │   │   ├── StatePanel.tsx    # 共享状态面板 (STATE_SNAPSHOT/DELTA)
│   │   │   └── StatusBar.tsx     # 底部状态栏 (连接状态/运行状态)
│   │   └── common/
│   │       ├── Box.tsx           # 基础布局封装
│   │       └── Spinner.tsx       # 加载指示器
│   └── hooks/
│       ├── useStreaming.ts       # 流式文本状态 hook
│       └── useTerminalSize.ts    # 终端尺寸监听
```

---

## 三、核心 API 说明

### 3.1 `useAgUiRuntime` — AG-UI 运行时

```typescript
// runtime/agent-runtime.ts
import { useMemo } from "react";
import { useAgUiRuntime } from "@assistant-ui/react-ag-ui"; // 注意：不是 react-ink
import { HttpAgent } from "@ag-ui/client";

export function useAgentRuntime() {
  const agent = useMemo(
    () =>
      new HttpAgent({
        url: process.env.AG_UI_ENDPOINT || "http://localhost:8000/agent",
      }),
    []
  );

  return useAgUiRuntime({
    agent,
    showThinking: true,   // 展示 THINKING/REASONING 事件
    onError: (e) => console.error("[AG-UI]", e),
    adapters: {
      // 可选: attachments, history, speech, feedback, threadList
    },
  });
}
```

**关键点**：
- `useAgUiRuntime` 来自 `@assistant-ui/react-ag-ui` 包，不是 `@assistant-ui/react-ink`
- `HttpAgent` 基于 rxjs `Observable` 封装 SSE 连接；**未提供内置重连**，断线恢复需上层结合 `STATE_SNAPSHOT` 自行实现或由用户手动重发
- `useAgUiRuntime` 内部解析所有 AG-UI 事件（`TEXT_MESSAGE_*`、`TOOL_CALL_*`、`STATE_*`、`THINKING_*`），将其转换为 assistant-ui 统一的 Message 模型
- 返回的 `runtime` 对象直接传给 `AssistantRuntimeProvider`
- **互操作风险**：`react-ag-ui` 的官方示例基于 Web 版 `@assistant-ui/react`，与 `@assistant-ui/react-ink` 的组合未被官方文档背书；Phase 1 第一步先做最小 spike 验证 runtime 能挂到 Ink 的 `AssistantRuntimeProvider`

### 3.2 核心 Primitives

assistant-ui 提供了一套声明式组件，无需手写消息状态管理：

| Primitive | 作用 |
|-----------|------|
| `ThreadPrimitive.Root` | 对话线程容器 |
| `ThreadPrimitive.Messages` | 消息列表，children 为渲染函数 |
| `ThreadPrimitive.Empty` | 无消息时的占位内容 |
| `ThreadPrimitive.ScrollToBottom` | 自动滚动到底部 |
| `ComposerPrimitive.Input` | 输入框组件 |
| `ComposerPrimitive.Send` | 发送按钮 |
| `MessagePrimitive.Root` | 消息容器（但 Ink 版建议用 `useAuiState`） |

### 3.3 `useAuiState` — 状态选择器

```typescript
// 获取当前消息
const message = useAuiState((s) => s.message);

// 获取线程运行状态
const isRunning = useAuiState((s) => s.thread.isRunning);

// 获取全部消息
const messages = useAuiState((s) => s.thread.messages);
```

这是 Ink 版本推荐的数据访问方式，类似 Zustand selector。

---

## 四、核心实现

### 4.1 入口文件

```typescript
// src/entry.tsx
import { render } from "ink";
import { App } from "./app.js";

// 检测是否在终端环境
if (!process.stdin.isTTY) {
  console.error("This app must run in a terminal.");
  process.exit(1);
}

const { unmount, waitUntilExit } = render(<App />);

process.on("SIGINT", () => {
  unmount();
  process.exit();
});

await waitUntilExit();
```

### 4.2 App 根组件

```typescript
// src/app.tsx
import { Box } from "ink";
import { AssistantRuntimeProvider } from "@assistant-ui/react-ink";
// useAgUiRuntime 在 agent-runtime.ts 里从 @assistant-ui/react-ag-ui 引入
import { useAgentRuntime } from "./runtime/agent-runtime.js";
import { Thread } from "./components/chat/Thread.js";
import { StatePanel } from "./components/panels/StatePanel.js";
import { StatusBar } from "./components/panels/StatusBar.js";

export function App() {
  const runtime = useAgentRuntime();

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <Box flexDirection="column" height="100%">
        {/* 主聊天区 */}
        <Box flexGrow={1} flexDirection="row">
          <Box flexGrow={3} flexDirection="column">
            <Thread />
          </Box>
          {/* 状态侧边栏 (可选) */}
          <Box flexGrow={1} borderStyle="single" borderColor="gray">
            <StatePanel />
          </Box>
        </Box>
        {/* 底部状态栏 */}
        <StatusBar />
      </Box>
    </AssistantRuntimeProvider>
  );
}
```

### 4.3 消息组件 — 核心难点

```typescript
// src/components/chat/Message.tsx
import { Box, Text } from "ink";
import { useAuiState } from "@assistant-ui/react-ink";
import { MarkdownText } from "@assistant-ui/react-ink-markdown";

export const Message = () => {
  const message = useAuiState((s) => s.message);

  // 用户消息
  if (message.role === "user") {
    const text = getTextFromMessage(message);
    return (
      <Box marginBottom={1}>
        <Text bold color="green">You: </Text>
        <Text wrap="wrap">{text}</Text>
      </Box>
    );
  }

  // AI 消息 — 可能包含 text + tool-call 混合内容
  return (
    <Box flexDirection="column" marginBottom={1}>
      <Text bold color="blue">AI:</Text>
      {message.content.map((part, i) => {
        if (part.type === "text") {
          // 流式友好：Ink 自动处理增量渲染
          return <MarkdownText key={i} text={part.text} />;
        }
        if (part.type === "tool-call") {
          return <ToolCallCard key={i} toolCall={part} />;
        }
        return null;
      })}
    </Box>
  );
};
```

**流式渲染的关键**：Ink 是 React reconciliation 驱动的，当 `useAuiState` 返回的新 message 对象引用变化时，Ink 自动 diff + 重渲染。AG-UI 的每个 `TEXT_MESSAGE_CONTENT` delta 都会更新 `message.content[n].text`，React 检测到变化后 Ink 重新绘制该行。

### 4.4 工具调用卡片

```typescript
// src/components/tools/ToolCallCard.tsx
import { Box, Text } from "ink";
import { useState, useEffect } from "react";

export function ToolCallCard({ toolCall }: { toolCall: any }) {
  const [collapsed, setCollapsed] = useState(true);

  return (
    <Box flexDirection="column" marginY={1}>
      <Box>
        <Text color="yellow">🔧 </Text>
        <Text bold color="yellow">{toolCall.toolName}</Text>
        <Text dimColor>
          {toolCall.status === "running" ? " (running...)" : " ✓"}
        </Text>
      </Box>
      {!collapsed && toolCall.args && (
        <Box marginLeft={4}>
          <Text dimColor>{JSON.stringify(toolCall.args, null, 2)}</Text>
        </Box>
      )}
      {toolCall.result && (
        <Box marginLeft={4}>
          <Text color="green">
            {typeof toolCall.result === "string"
              ? toolCall.result.slice(0, 200)
              : JSON.stringify(toolCall.result).slice(0, 200)}
          </Text>
        </Box>
      )}
    </Box>
  );
}
```

### 4.5 输入栏

```typescript
// src/components/composer/InputBar.tsx
import { Box, Text } from "ink";
import { ComposerPrimitive } from "@assistant-ui/react-ink";

export function InputBar() {
  return (
    <Box borderStyle="round" borderColor="gray" paddingX={1} marginTop={1}>
      <Text color="gray">{"> "}</Text>
      <ComposerPrimitive.Input
        submitOnEnter
        placeholder="Ask anything... (Ctrl+D to exit)"
        autoFocus
      />
    </Box>
  );
}
```

### 4.6 状态栏

```typescript
// src/components/panels/StatusBar.tsx
import { Box, Text } from "ink";
import { useAuiState } from "@assistant-ui/react-ink";

export function StatusBar() {
  // 注意：以下 selector 路径需对照 @assistant-ui/store 的 ThreadsState 类型校对，
  // 实际结构可能是 s.thread.state.isRunning / s.thread.state.messages，
  // Phase 1 spike 中以 IDE 类型推断为准。
  const isRunning = useAuiState((s) => s.thread.isRunning);
  const msgCount = useAuiState((s) => s.thread.messages.length);

  return (
    <Box justifyContent="space-between" paddingX={1}>
      <Text dimColor>
        {isRunning ? "● Running" : "○ Idle"}
      </Text>
      <Text dimColor>Messages: {msgCount}</Text>
      <Text dimColor>AG-UI v0.0.53</Text>
    </Box>
  );
}
```

---

## 五、关键技术决策

### 5.1 为什么选 `useAgUiRuntime` 而非 `useLocalRuntime`？

| 方案 | 场景 | 优劣势 |
|------|------|--------|
| `useLocalRuntime(ChatModelAdapter)` | 自己的 LLM API | 需手写 streaming adapter，不支持 AG-UI 事件 |
| `useAgUiRuntime({ agent })` | AG-UI 兼容的服务端 | 自动解析 SSE 事件，内置 TOOL_CALL/STATE 处理 |

**结论**：选 `useAgUiRuntime`，因为它直接对接 AG-UI 协议，无需手动处理 SSE 事件解析和 Message 重建。

### 5.2 流式渲染如何实现？

Ink 自带 React reconciliation：

1. AG-UI 的 `TEXT_MESSAGE_CONTENT` delta 到达 → HttpAgent 更新内部状态
2. AgUiRuntime 更新 message 对象（引用变化）
3. `useAuiState` selector 返回新值
4. React 触发 re-render
5. Ink diff 出增量的文本片段，在终端对应位置重绘

**无需额外代码**，Ink + React 的声明式模型已经处理了。

### 5.3 多面板布局

Ink 支持 Flexbox 布局（`Box flexDirection="row"`）。可以用：

```
┌──────────────────────────────────────┐
│  Chat Panel (flexGrow: 3)            │
│  用户: xxx                            │
│  AI: yyy (流式)                       │
│  🔧 search_web (running...)          │
│                                      │
├─────────────────────┬────────────────┤
│  State Panel (1/4)  │  Tools (1/4)  │
│  shared_state: {    │  - tool_1 ✓    │
│    phase: "research" │  - tool_2 ...  │
│  }                  │               │
├─────────────────────┴────────────────┤
│  ● Running  |  Messages: 12           │
└──────────────────────────────────────┘
```

### 5.4 终端尺寸适配

```typescript
import { useStdout } from "ink";

const { stdout } = useStdout();
// stdout.columns, stdout.rows 实时更新
```

结合 `process.stdout.on("resize", ...)` 监听窗口变化。

---

## 六、实施路线图

### Phase 0: 互操作 Spike — 0.5 天（前置门禁）

- [ ] 最小化验证 `useAgUiRuntime`(来自 `@assistant-ui/react-ag-ui`) + `AssistantRuntimeProvider`(来自 `@assistant-ui/react-ink`) + `ThreadPrimitive.Messages` 三者能正确耦合
- [ ] 跑通一次「发送 hello → 接收一段流式文本」的最短链路
- [ ] 若耦合失败：回退方案为基于 `useLocalRuntime` + 手写 SSE adapter（参考 `@ag-ui/client` 的 `Observable<AgUiEvent>` 自行映射成 `ChatModelAdapter` 的 stream）

### Phase 1: MVP (最小可行产品) — 1-2 天

- [ ] 项目脚手架：`npx assistant-ui@latest create --ink ag-ui-tui`
- [ ] 配置 AG-UI endpoint 环境变量
- [ ] 实现单列聊天界面：消息列表 + 输入框
- [ ] 验证流式文本输出正常
- [ ] 验证工具调用展示

### Phase 2: 增强交互 — 2-3 天

- [ ] 工具调用折叠/展开
- [ ] 思考链(thinking)展示
- [ ] 多行输入支持 (Shift+Enter)
- [ ] Ctrl+C 中断运行
- [ ] 消息历史上下翻页

### Phase 3: 高级特性 — 3-5 天

- [ ] 多面板布局（State 面板 + Tool 面板）
- [ ] STATE_SNAPSHOT / STATE_DELTA 可视化
- [ ] 多线程切换 (threadList adapter)
- [ ] 键盘快捷键系统 (类似 Hermes)
- [ ] 主题定制 (terminal colors)

### Phase 4: 生产就绪 — 持续

- [ ] 终端 resize 自适应
- [ ] 断线重连 + 状态恢复
- [ ] 日志记录
- [ ] 打包为单二进制 (pkg / bun compile)
- [ ] CI/CD + npm 发布

---

## 七、对标参考

| 项目 | 技术栈 | 参考价值 |
|------|--------|----------|
| **Hermes Agent TUI** | Ink + JSON-RPC | 全功能 TUI 交互模式：slash 命令、非阻塞输入、提示覆盖层、补全系统、多面板 |
| **assistant-ui ink demo** | Ink + ChatModelAdapter | 最小化 demo，验证基础链路 |
| **TAUI Standards** | Ink + TAUI 协议 | Grid/Flexbox 布局在终端中的实现方式 |
| **@agentick/tui** | Ink + Agentick | Agent TUI 组件设计（0.14.64 版本，较成熟） |

---

## 八、风险与对策

| 风险 | 影响 | 对策 |
|------|------|------|
| `@assistant-ui/react-ink` 版本过低 (0.0.16) | API 可能不稳定 | 锁定版本，关注上游更新 |
| `react-ag-ui` ↔ `react-ink` 互操作未官方背书 | runtime 可能无法挂到 Ink 的 Provider | **Phase 0 spike 优先验证**；失败则回退到 `useLocalRuntime` + 手写 SSE adapter |
| `@assistant-ui/react-ag-ui` 与 Ink 版的事件覆盖 | 可能存在未覆盖的 AG-UI 事件 | 验证 `TEXT_MESSAGE_*`、`TOOL_CALL_*`、`STATE_*` 三类核心事件 |
| Ink 的终端兼容性 | 部分终端渲染异常 | 仅支持 Windows Terminal / 现代 \*nix 终端，最低依赖 xterm-256color |
| SSE 连接不稳定 | 消息丢失 | `HttpAgent` **无内置重连**；上层自行实现指数退避重连 + 通过 `STATE_SNAPSHOT` 恢复线程状态 |
| `useAuiState` selector 路径与文档不符 | StatusBar/StatePanel 取值失败 | 以 `@assistant-ui/store` 的 `ThreadsState` 类型为准，spike 阶段对齐 |

---

## 九、环境要求

- Node.js ≥ 22（Ink 6 / 7 要求）
- React 19（react-ink peerDep）
- 终端支持 256 色与 ANSI 转义
- **Windows 平台仅支持 Windows Terminal（ConPTY）**；不支持 cmd.exe、传统 PowerShell 控制台、Git Bash + winpty 等环境
- macOS / Linux：iTerm2、Terminal.app、Alacritty、WezTerm、现代 xterm 兼容终端
- AG-UI 兼容的服务端（LangGraph / CrewAI / Mastra / 自定义）

---

## 十、打包与安装

### 10.1 项目配置

**`package.json` 关键字段**

```jsonc
{
  "name": "ag-ui-tui",
  "version": "0.1.0",
  "type": "module",
  "bin": {
    "ag-ui-tui": "./dist/entry.js"
  },
  "files": ["dist"],
  "engines": { "node": ">=22" },
  "scripts": {
    "dev": "tsx watch src/entry.tsx",
    "build": "tsup src/entry.tsx --format esm --target node24 --clean --dts=false",
    "start": "node dist/entry.js",
    "typecheck": "tsc --noEmit",
    "prepublishOnly": "pnpm build"
  }
}
```

**入口文件 shebang**——`src/entry.tsx` 顶部必须加：

```tsx
#!/usr/bin/env node
// 其余 import + render(<App />) 内容...
```

打包后 `dist/entry.js` 会保留该 shebang，确保 `npm install -g` 后能直接作为命令行执行。

**`tsconfig.json` 关键项**

```jsonc
{
  "compilerOptions": {
    "target": "ES2023",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "jsx": "react-jsx",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "outDir": "dist"
  },
  "include": ["src"]
}
```

### 10.2 本地开发

```bash
# 安装依赖
pnpm install

# 启动 AG-UI 服务端（另一终端）
# 例如：python -m my_langgraph_agent  → http://localhost:8000/agent

# 配置端点
export AG_UI_ENDPOINT="http://localhost:8000/agent"   # macOS/Linux
# 或在 Windows Terminal:
$env:AG_UI_ENDPOINT = "http://localhost:8000/agent"

# 启动开发模式（tsx 热重载）
pnpm dev
```

**`.env` 加载**（可选）：若使用 `.env`，在 `entry.tsx` 顶部 `import "dotenv/config";` 并加 `dotenv` 依赖。

### 10.3 发布到 npm（推荐方案）

最常规、跨平台兼容性最好的发布方式：

```bash
pnpm build               # 产出 dist/
pnpm publish --access public
```

终端用户通过以下任一方式使用：

```bash
# 方式 1：免安装直接运行（推荐）
npx ag-ui-tui

# 方式 2：全局安装为命令
npm install -g ag-ui-tui
ag-ui-tui

# 方式 3：pnpm 用户
pnpm dlx ag-ui-tui
```

**优点**：跨平台、安装快、可随时更新；**缺点**：用户机器需有 Node ≥ 22。

### 10.4 单二进制打包（可选）

适合分发给没有 Node 运行时的用户。三种主流方案对比：

| 方案 | 适用场景 | 优势 | 局限 |
|------|----------|------|------|
| **Bun `bun build --compile`** | 推荐首选 | 单命令产出独立可执行文件，体积 ~50MB，支持 ESM/TS 原生 | 需 Bun 工具链；某些 Node 原生模块兼容性需测试 |
| **Node.js SEA（Single Executable Applications）** | 官方原生方案 | 不依赖第三方工具，与 Node 同步升级 | API 仍属 stability 1（实验性），打包步骤繁琐 |
| **`pkg`** | 历史方案 | 上手简单 | 已停止维护，不支持 Node 22+ ESM——**不推荐** |

**Bun 打包示例**（推荐）：

```bash
# 安装 Bun
curl -fsSL https://bun.sh/install | bash   # macOS/Linux
# Windows: powershell -c "irm bun.sh/install.ps1 | iex"

# 为各平台产出可执行文件
bun build src/entry.tsx --compile --target=bun-linux-x64   --outfile dist/ag-ui-tui-linux
bun build src/entry.tsx --compile --target=bun-darwin-arm64 --outfile dist/ag-ui-tui-macos-arm64
bun build src/entry.tsx --compile --target=bun-windows-x64 --outfile dist/ag-ui-tui.exe
```

产物可直接通过 GitHub Releases 分发，用户下载后赋予执行权限即可运行：

```bash
chmod +x ag-ui-tui-linux && ./ag-ui-tui-linux
```

**Node SEA 示例**（备选）：参考 [Node.js SEA 文档](https://nodejs.org/api/single-executable-applications.html)，需要 `sea-config.json` + `postject` 注入步骤，本文不展开。

### 10.5 验证安装

```bash
ag-ui-tui --version          # 应输出 package.json 中的 version
AG_UI_ENDPOINT=http://localhost:8000/agent ag-ui-tui
# 进入 TUI 后输入 "hello" 验证流式输出
```

### 10.6 发布检查清单

- [ ] `pnpm typecheck` 无错误
- [ ] `pnpm build` 产出 `dist/entry.js` 且首行包含 `#!/usr/bin/env node`
- [ ] 在 Windows Terminal、macOS Terminal.app、Linux 终端各跑一次冒烟测试
- [ ] `package.json` 的 `files` 字段仅包含 `dist`（不发布 `src` 与 `node_modules`）
- [ ] `engines.node` 锁定 `>=22`，防止低版本 Node 误装
- [ ] README 标注 Windows Terminal 限制
- [ ] 单二进制方案：每平台产物在干净机器（无 Node 环境）跑过一次
