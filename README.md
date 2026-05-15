# deerflow-tui

`deerflow-tui` 是一个基于 **AG-UI 协议** 的终端客户端，使用 **Ink + React** 构建 TUI 界面，用于连接兼容 AG-UI 的 Agent 服务端，例如 LangGraph、CrewAI、Mastra 或自定义服务。

项目支持两种使用方式：

- 作为 Node.js CLI 运行
- 打包为单文件可执行程序，目前支持 Windows `.exe` 和 Linux 可执行文件

## 环境要求

- Node.js >= 22
- pnpm 11.x
- 一个兼容 AG-UI 协议的服务端，默认地址为 `http://localhost:8000/agent`

终端要求：

- Windows：建议使用 Windows Terminal，不建议使用传统 `cmd.exe` 或旧版 PowerShell 控制台
- Linux/macOS：使用现代 xterm 兼容终端即可，例如 Terminal、iTerm2、Alacritty、WezTerm 等

## 安装依赖

```bash
pnpm install
```

## 开发运行

先启动你的 AG-UI 服务端，例如监听在：

```text
http://localhost:8000/agent
```

然后启动 TUI：

Windows PowerShell：

```powershell
$env:AG_UI_ENDPOINT = "http://localhost:8000/agent"
pnpm dev
```

Linux/macOS：

```bash
AG_UI_ENDPOINT=http://localhost:8000/agent pnpm dev
```

退出程序：

```text
Ctrl+C
```

## 常用命令

| 命令 | 说明 |
| --- | --- |
| `pnpm dev` | 直接运行源码 |
| `pnpm dev:watch` | 以 watch 模式运行源码 |
| `pnpm typecheck` | 执行 TypeScript 类型检查 |
| `pnpm build` | 构建普通 Node.js CLI 产物到 `dist/` |
| `pnpm start` | 运行 `dist/entry.js` |
| `pnpm build:sea` | 构建 Node SEA 使用的入口文件 |
| `pnpm package:win` | 构建 Windows 单文件 `.exe` |
| `pnpm package:linux` | 构建 Linux 单文件可执行程序 |

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `AG_UI_ENDPOINT` | `http://localhost:8000/agent` | AG-UI 服务端地址 |
| `AG_UI_HEADERS` | 空 | JSON 字符串，作为请求头传给 AG-UI 服务端 |
| `AG_UI_INITIAL_STATE` | 空 | JSON 对象字符串，作为初始 state 传给 Agent |

示例：

```bash
AG_UI_ENDPOINT=http://localhost:8000/agent \
AG_UI_HEADERS='{"Authorization":"Bearer xxx"}' \
pnpm dev
```

## 普通构建

普通构建会生成 Node.js CLI 入口文件：

```bash
pnpm build
```

产物位置：

```text
dist/entry.js
```

运行方式：

```bash
node dist/entry.js
```

## Windows 可执行文件构建

Windows 打包基于 Node.js SEA，最终会生成一个 `.exe` 文件。

在 Windows 环境中执行：

```powershell
pnpm install
pnpm package:win
```

产物位置：

```text
dist-sea/deerflow-tui.exe
```

运行：

```powershell
$env:AG_UI_ENDPOINT = "http://localhost:8000/agent"
.\dist-sea\deerflow-tui.exe
```

说明：

- `package:win` 会先执行 `build:sea`
- 然后通过 `node --experimental-sea-config sea-config.json` 生成 SEA blob
- 最后执行 `scripts/package-win-sea.ps1`，复制当前 Windows 环境的 `node.exe` 并注入 SEA blob

## Linux 可执行文件构建

Linux 打包同样基于 Node.js SEA，最终会生成无扩展名的 Linux 可执行文件。

在 Linux 环境中执行：

```bash
pnpm install
pnpm package:linux
```

产物位置：

```text
dist-sea/deerflow-tui
```

运行：

```bash
AG_UI_ENDPOINT=http://localhost:8000/agent ./dist-sea/deerflow-tui
```

说明：

- `package:linux` 会先执行 `build:sea`
- 然后通过 `node --experimental-sea-config sea-config.json` 生成 SEA blob
- 最后执行 `scripts/package-linux-sea.sh`，复制当前 Linux 环境的 `node` 并注入 SEA blob
- 脚本会自动执行 `chmod +x dist-sea/deerflow-tui`

注意：

- Linux 构建需要在 Linux 环境中执行，推荐使用真实 Linux、WSL 或 CI Linux runner
- 默认使用当前环境中的 `node` 作为可执行文件基底
- 建议 Windows 和 Linux 构建都使用相同主版本的 Node.js 22
- 常规 Linux x64 发行版通常使用 glibc；Alpine Linux 等 musl 环境需要单独验证

## 项目结构

```text
src/
  entry.tsx                       # Ink render() 入口
  app.tsx                         # 根布局和 AssistantRuntimeProvider
  runtime/
    agent-runtime.ts              # AG-UI HttpAgent 运行时适配
  components/
    chat/
      Message.tsx                 # 消息渲染
    composer/
      InputBar.tsx                # 输入栏
    panels/
      StatusBar.tsx               # 状态栏

scripts/
  package-win-sea.ps1             # Windows SEA 打包脚本
  package-linux-sea.sh            # Linux SEA 打包脚本

dist/                             # 普通 Node.js CLI 构建产物
dist-sea/                         # SEA 构建和可执行文件产物
```

## SEA 打包说明

本项目使用 Node.js SEA 生成单文件可执行程序。核心文件如下：

- `tsup.sea.config.ts`：将入口和依赖打包为 `dist-sea/entry.mjs`
- `sea-config.json`：声明 SEA 主入口和资源
- `scripts/sea-bootstrap.cjs`：从 SEA asset 中读取 `entry.mjs` 并动态加载
- `postject`：将 `deerflow-tui.blob` 注入到 Node 可执行文件中

打包后的可执行文件仍然需要在交互式 TTY 中运行。
