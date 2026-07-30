# deerflow-tui

`deerflow-tui` 是一个基于 **AG-UI 协议** 的终端客户端，使用 **Go + Bubble Tea** 构建原生 TUI 界面，用于连接兼容 AG-UI 的 Agent 服务端（如 LangGraph、CrewAI、Mastra 等）。

## 环境要求

- Go 1.22+
- 一个兼容 AG-UI 协议的服务端，默认地址为 `http://localhost:8000/agent`

终端要求：

- Windows：建议使用 Windows Terminal，不建议使用传统 `cmd.exe` 或旧版 PowerShell 控制台
- Linux/macOS：使用现代 xterm 兼容终端即可

## 开发运行

先启动你的 AG-UI 服务端，然后：

```bash
go run ./cmd/deerflow-tui
```

或先构建：

```bash
go build -o deerflow-tui ./cmd/deerflow-tui
./deerflow-tui
```

退出程序：输入 `/exit` 或 `/quit`。

## 常用命令

| 命令 | 说明 |
| --- | --- |
| `go run ./cmd/deerflow-tui` | 直接运行 |
| `go test ./...` | 运行全部测试 |
| `go build ./...` | 编译所有包 |

## 项目结构

```text
cmd/
  deerflow-tui/
    main.go               # 入口点

internal/
  config/
    config.go             # 环境变量加载（AG_UI_ENDPOINT 等）
  agui/
    types.go              # AG-UI 协议类型、角色归一化、消息解析
    client.go             # HTTP/SSE 流式客户端（StartRun / ResumeRun）
    types_test.go         # 类型解析测试
    client_test.go        # SSE 解析测试
  tui/
    model.go              # Bubble Tea 主模型
    model_test.go         # TUI 行为测试

scripts/
  build-go.sh             # 跨平台构建脚本
```

## 功能特性

| 特性 | 说明 |
|------|------|
| AG-UI HTTP/SSE 流式接入 | ✅ |
| 消息列表视图（viewport） | ✅ |
| 文本输入框 | ✅ |
| 状态栏（端点、消息数、线程 ID） | ✅ |
| `/exit` / `/quit` | 退出程序 |
| `/cancel` | 取消当前 run |
| `/new` | 新建线程 |
| 左键点击选中消息 | ✅ |
| `Ctrl+C` 复制消息内容到剪贴板 | ✅ |
| 左键拖动选择文本（到视图区边缘自动滚动），右键或 `Ctrl+C` 复制 | ✅ |
| `Ctrl+V` 粘贴多行文本时显示 `[paste N lines]`，按 `Enter` 后作为一条消息发送 | ✅ |
| TEXT_MESSAGE 流式事件 | ✅ |
| REASONING 事件 | ✅ |
| THINKING 事件 | ✅ |
| TOOL_CALL 生命周期（start/args/end/result） | ✅ |
| STATE_SNAPSHOT / STATE_DELTA | ✅ |
| MESSAGES_SNAPSHOT 历史导入 | ✅ |
| RUN_STARTED / RUN_FINISHED / RUN_ERROR / RUN_CANCELLED | ✅ |
| 模型中断（Interrupt）与回复（Resume） | ✅ |
| 历史消息 replay 文本/ID 过滤 | ✅ |
| Stale stream 事件隔离（runSeq） | ✅ |
| SSE 断线续传（Last-Event-ID） | ✅ |
| 10 分钟指数退避重连 | ✅ |
| TUI 重启后恢复活动 run | ✅ |
| 终端宽度自适应与文本换行 | ✅ |
| `.env` 文件自动加载 | ✅ |

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `AG_UI_ENDPOINT` | `http://localhost:8000/agent` | AG-UI 服务端地址 |
| `AG_UI_HEADERS` | 空 | JSON 字符串，作为请求头传给 AG-UI 服务端 |
| `AG_UI_INITIAL_STATE` | 空 | JSON 对象字符串，作为初始 state 传给 Agent |
| `DEERFLOW_TUI_STATE_DIR` | 用户配置目录下的 `deerflow-tui` | 活动 run 会话文件目录 |

支持 `.env` 文件自动加载（best-effort，文件不存在时静默忽略）。

示例：

```bash
AG_UI_ENDPOINT=http://localhost:8000/agent \
AG_UI_HEADERS='{"Authorization":"Bearer xxx"}' \
go run ./cmd/deerflow-tui
```

## 跨平台构建

```bash
./scripts/build-go.sh                # 当前平台 dev 构建
./scripts/build-go.sh v1.0.0         # 指定版本
./scripts/build-go.sh v1.0.0 linux amd64   # Linux amd64
./scripts/build-go.sh v1.0.0 windows amd64 # Windows amd64
```

输出位置：`dist-go/deerflow-tui`（或 `dist-go/deerflow-tui.exe`）
