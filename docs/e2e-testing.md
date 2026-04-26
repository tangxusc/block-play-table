# 端到端测试文档

本文档定义 Block Play Table 的端到端测试策略、运行入口、覆盖矩阵、发布准入和排障方法。端到端测试用于验证 Manager、Worker、Flutter Web UI、GraphQL、Worker WebSocket、持久化、领域事件和真实 Agent CLI 在可信模式下可以形成完整闭环。

## 测试目标与边界

端到端测试重点验证用户可感知的业务闭环，而不是替代单元测试、组件测试或覆盖率门禁。

目标：

- 验证 Project、Worker、Task、Settings、Board 等核心对象可以通过 UI/API 完成主要工作流。
- 验证 Manager GraphQL 与 Worker WebSocket 的协议交互、状态流转、日志、会话、结果和领域事件持久化。
- 验证 Flutter Web UI 可以加载、展示看板状态分组，并在订阅事件或兜底刷新后呈现最新状态。
- 验证真实 Codex/Claude CLI 在发布前可以通过 Worker 执行固定任务，并产出可追踪结果。

边界：

- E2E 不覆盖所有字段级校验，字段级分支主要由 Go 单元测试和 Flutter widget 测试覆盖。
- E2E 不承担安全渗透测试。当前系统处于 trusted mode，GraphQL/UI 未启用运行时鉴权。
- 真实 Agent E2E 依赖本机 CLI、凭据、网络和模型服务，作为发布必跑项，不作为每次本地文档或代码变更的默认验证。

## 测试分层与现有入口

| 层级 | 入口 | 用途 | 默认运行时机 |
| --- | --- | --- | --- |
| L1 Go in-process E2E | `make e2e` | 使用 `httptest` 在进程内验证 Manager GraphQL、Worker WebSocket、领域事件和日志落库 | 本地变更、CI、发布前 |
| L2 Playwright UI E2E | `npm run e2e` | 连接运行中的 UI/Manager，验证 Flutter Web UI、GraphQL 种子数据、模拟 Worker 生命周期、看板状态分组 | UI/API 变更、CI、发布前 |
| L3 Real Agent Release Gate | `npm run e2e:real-agents` | 启动真实 Manager/Worker，并使用 Codex/Claude CLI 跑通固定任务 | 发布必跑 |

现有测试文件：

- `manager/e2e/trusted_flow_test.go`：L1，进程内构造 Manager、Worker WebSocket、GraphQL 创建 Project/Task、启动任务、上报 Worker 事件并验证完成与日志。
- `e2e/block_play_table.spec.ts`：L2，打开 Flutter Web UI，通过 GraphQL 创建 Project/Worker/Task，模拟 Worker WebSocket 上报 `TASK_STARTED`、`TASK_LOG`、`TASK_CONVERSATION`、`TASK_RESULT`、`TASK_COMPLETED`，验证任务状态、日志、会话和领域事件。
- `e2e/board_status_groups.spec.ts`：L2，构造 pending/running/complete 三类任务，打开看板并生成截图 `board-status-groups.png`。
- `scripts/real_agent_e2e.sh`：L3，检查 `codex` 与 `claude` 命令存在，启动 trusted-mode Manager/Worker；任务创建和结果校验需要通过 UI、GraphQL 或后续 Playwright/API 流程完成。

## 本地环境准备

基础依赖：

- Go：用于 Manager、Worker 和 Go E2E。若本机 Go 环境有 `GOROOT` 冲突，使用 `GO_TEST_ENV='env -u GOROOT'`。
- Node.js 与 npm：用于安装和运行 Playwright。
- Playwright：通过 `npm install` 安装 `@playwright/test`。
- Docker：用于本地启动 Manager、Worker、UI、PostgreSQL，或运行 Flutter 测试镜像。
- Codex CLI：真实 Agent E2E 需要 `codex exec` 可用。
- Claude CLI：真实 Agent E2E 需要 `claude -p` 可用。
- Worker 工作目录：真实或容器 Worker 需要可写目录，例如 `./worker-data` 或 `worker-data-real-e2e`。

推荐本地启动方式：

```bash
make run-local
```

该命令使用 `docker-compose.team.yml` 启动 PostgreSQL、Manager、Worker 和 UI。默认端口：

- UI：`http://localhost:3000`
- Manager：`http://localhost:8080`
- GraphQL：`http://localhost:8080/graphql`
- Worker WebSocket：`ws://localhost:8080/worker/ws`

结束本地栈：

```bash
make stop-local
```

清理本地栈和 Worker 数据：

```bash
make clean-local
```

## 关键环境变量

| 变量 | 默认值或来源 | 用途 |
| --- | --- | --- |
| `BPT_UI_URL` | `http://localhost:3000` | Playwright `baseURL`，指定 UI 地址 |
| `BPT_MANAGER_GRAPHQL_URL` | `http://localhost:8080/graphql` | Playwright 测试访问 Manager GraphQL 的地址 |
| `BPT_MANAGER_WS_URL` | 由 GraphQL URL 推导为 `/worker/ws` | Playwright 模拟 Worker 连接的 WebSocket 地址 |
| `BPT_MANAGER_WS_TOKEN` | 空 | Playwright 模拟 Worker 连接时追加到 `token` 查询参数 |
| `WORKER_TOKEN` | 本地 Makefile 默认 `dev-worker-token` | Manager 启用 Worker WebSocket token 校验，真实 Worker 使用同值连接 |
| `DB_DRIVER` | Manager 默认 `sqlite` | Manager 存储驱动，可选 `sqlite`、`postgres`、`memory` |
| `DB_DSN` | SQLite 默认 `./data/manager.db` | Manager 数据源地址；PostgreSQL 模式必须显式提供 |
| `GO_BIN` | `go` | `scripts/real_agent_e2e.sh` 用于启动 Manager/Worker 的 Go 命令 |
| `WORKER_DIR` | `./worker-data-real-e2e` | 真实 Agent E2E 使用的 Worker 工作目录 |

与 Worker 本体相关的常用变量还包括 `MANAGER_WS_URL`、`WORKER_ID`、`WORKER_NAME`、`WORKER_WORK_DIR`、`WORKER_SUPPORTED_AGENTS`、`WORKER_PROJECT_BINDING_MODE`、`WORKER_BOUND_PROJECT_IDS`。

## 运行方式

### L1 Go in-process E2E

```bash
make e2e
```

等价于：

```bash
GOTOOLCHAIN=local go test ./manager/e2e -count=1
```

在有 Go 环境冲突的机器上：

```bash
make e2e GO=go GO_TEST_ENV='env -u GOROOT'
```

期望结果：

- `TestTrustedManagerWorkerFlow` 通过。
- GraphQL 创建 Project/Task 成功。
- Worker WebSocket 收到 `TASK_START`。
- Manager 接收 Worker 运行事件后，将任务推进到 `COMPLETED`。
- `taskLogs` 能查询到 Worker 日志。

### L2 Playwright UI E2E

先启动本地 UI/Manager/Worker：

```bash
make run-local
```

安装 Node 依赖：

```bash
npm install
```

运行全部 Playwright E2E：

```bash
npm run e2e
```

连接非默认地址时：

```bash
BPT_UI_URL=http://localhost:3000 \
BPT_MANAGER_GRAPHQL_URL=http://localhost:8080/graphql \
BPT_MANAGER_WS_URL=ws://localhost:8080/worker/ws \
BPT_MANAGER_WS_TOKEN=dev-worker-token \
npm run e2e
```

列出现有用例：

```bash
npm run e2e -- --list
```

失败后检查：

- Playwright trace：配置为 `on-first-retry`，失败重试时会生成 trace。
- Playwright output：`test-results/` 下包含截图和失败上下文。
- 看板截图：`board-status-groups.png` 会写入当前 Playwright 用例输出目录。

### L3 真实 Agent 发布准入

真实 Agent E2E 是发布必跑项。发布前必须分别使用 Codex 与 Claude 跑通固定任务，并验证任务最终状态、日志、会话、结果和 worktree。

前置检查：

```bash
command -v codex
command -v claude
codex --version
claude --version
```

启动真实 Agent E2E 栈：

```bash
GO_BIN=go \
WORKER_DIR=./worker-data-real-e2e \
npm run e2e:real-agents
```

如需规避本机 `GOROOT` 冲突：

```bash
GO_BIN=go \
GOROOT= \
GOTOOLCHAIN=local \
WORKER_DIR=./worker-data-real-e2e \
npm run e2e:real-agents
```

发布前需要完成两条固定任务：

- Codex 任务：`agentType=codex`，使用固定测试仓库或本地 fixture，要求 Worker 创建 worktree、执行前置命令、运行 `codex exec`、写入日志/会话、执行后置命令，并以 `COMPLETED` 结束。
- Claude 任务：`agentType=claude`，使用同一类固定输入，要求 Worker 创建 worktree、执行前置命令、运行 `claude -p`、写入日志/会话、执行后置命令，并以 `COMPLETED` 结束。

验收查询应确认：

- `task.status == COMPLETED`
- `task.result` 非空，并包含固定任务的预期结果摘要
- `taskLogs(taskId)` 包含 Agent 启动、前置命令、后置命令和关键输出
- `taskConversations(taskId)` 包含 Agent 回复内容
- `taskEvents(taskId)` 至少包含 `TaskCreated`、`TaskAssigned`、`TaskStartRequested`、`TaskStarted`、`TaskCompleted`
- Worker 工作目录下存在对应任务的 worktree，且没有污染其他任务目录

## 全量场景矩阵

| 领域 | 场景 | 层级 | 状态 |
| --- | --- | --- | --- |
| Manager | `/healthz` 返回 200 | L1 | [待补齐] |
| Manager | `/readyz` 在存储可用时返回 200、存储不可用时返回 503 | L1 | [待补齐] |
| Manager | GraphQL trusted mode 不要求鉴权头 | L1 | [待补齐] |
| Project | 创建 Project 并使用默认分支/worktree 前缀 | L1/L2 | [已实现] |
| Project | 更新 Project 名称、Git URL、默认分支、worktree 前缀 | L1/L2 | [待补齐] |
| Project | 归档 Project 后默认列表不展示，includeArchived 可查询 | L1/L2 | [待补齐] |
| Project | Worker 绑定 SPECIFIC_PROJECTS 时只接收绑定 Project 的任务 | L1/L2 | [已实现] |
| Worker | 通过 GraphQL 注册 Worker | L2 | [已实现] |
| Worker | 通过 Worker WebSocket `WORKER_REGISTER` 注册 Worker | L1 | [已实现] |
| Worker | 设置 `WORKER_TOKEN` 后，无 token 连接被拒绝，正确 token 可连接 | L1/L2 | [待补齐] |
| Worker | Worker 上报心跳并更新 `lastHeartbeatAt` | L1 | [待补齐] |
| Worker | Worker 断线后被标记为 `OFFLINE` | L1/L2 | [待补齐] |
| Worker | Worker 重连后恢复 `ONLINE` 并可继续接收任务 | L1/L2 | [待补齐] |
| Worker | 禁用 Worker 后不参与自动分配，启用后恢复可用 | L1/L2 | [待补齐] |
| Worker | 删除空闲 Worker 后列表移除并产生领域事件 | L1/L2 | [待补齐] |
| Worker | 更新 Worker 项目绑定为 ALL_PROJECTS 与 SPECIFIC_PROJECTS | L1/L2 | [待补齐] |
| Task | 创建未分配、无 Agent 的任务 | L1/L2 | [待补齐] |
| Task | 创建指定 Worker 与 Agent 的任务后状态为 `ASSIGNED` | L2 | [已实现] |
| Task | 自动分配只选择在线、空闲、支持 Agent、允许 Project 的 Worker | L1/L2 | [待补齐] |
| Task | 启动已分配任务，Manager 向 Worker 下发 `TASK_START` | L1/L2 | [已实现] |
| Task | Worker 上报 `TASK_ACCEPTED` 后保持启动流程可追踪 | L2 | [已实现] |
| Task | Worker 上报 `TASK_STARTED` 后任务进入 `RUNNING` 并记录 worktree | L1/L2 | [已实现] |
| Task | Worker 上报 `TASK_LOG` 后日志可在 API/UI 查询 | L1/L2 | [已实现] |
| Task | Worker 上报 `TASK_CONVERSATION` 后会话可在 API/UI 查询 | L2 | [已实现] |
| Task | Worker 上报 `TASK_RESULT` 后结果暂存到任务 | L2 | [已实现] |
| Task | Worker 上报 `TASK_COMPLETED` 后任务进入 `COMPLETED` 并释放 Worker | L1/L2 | [已实现] |
| Task | Worker 上报 `TASK_FAILED` 后任务进入 `FAILED` 并保留失败原因 | L1/L2 | [待补齐] |
| Task | Worker 上报 `TASK_WAITING_INPUT` 后任务进入 `WAITING_INPUT` | L1/L2 | [待补齐] |
| Task | 中断运行中任务，下发 `TASK_INTERRUPT`，Worker 上报 `TASK_INTERRUPTED` | L1/L2 | [待补齐] |
| Task | 删除或取消等待任务时下发 `TASK_CANCEL` | L1 | [待补齐] |
| Task | 已完成、失败、中断任务可重试并清理旧 Worker/worktree/result | L1/L2 | [待补齐] |
| Task | Created/Completed/Failed/Interrupted 任务可归档并进入完成列 | L2 | [已实现] |
| Task | 重复 Worker messageId 被幂等处理 | L1 | [待补齐] |
| UI | Flutter Web 首屏可加载并显示 `flutter-view` | L2 | [已实现] |
| UI | 看板将 CREATED/ASSIGNED/STARTING、RUNNING/WAITING/INTERRUPTING、COMPLETED/FAILED/INTERRUPTED/ARCHIVED 分组成三列 | L2 | [已实现] |
| UI | 任务详情展示状态、日志、会话和最终结果 | L2 | [待补齐] |
| UI | GraphQL subscription 事件到达后看板刷新 | L2 | [待补齐] |
| UI | subscription 失败时触发兜底 reload/refresh | L2 | [待补齐] |
| UI | Project/Worker/Task 创建与编辑弹窗完成真实 API 写入 | L2 | [待补齐] |
| Settings | Agent runtime env var 在 UI/API 中遮蔽敏感值 | L1/L2 | [待补齐] |
| Settings | 编辑公开 env var 时保留可见值，编辑敏感 env var 时保留空值语义 | L1/L2 | [待补齐] |
| Settings | 启动任务时仅注入 enabled env vars 到 Worker payload | L1/L3 | [待补齐] |
| Storage | SQLite 模式跨重启保留 Project/Worker/Task/Event/Log | L1 | [待补齐] |
| Storage | PostgreSQL 模式应用迁移并通过 readiness | L1/L2 | [待补齐] |
| Storage | 领域事件可按 aggregateId/aggregateType/eventType 过滤 | L1/L2 | [待补齐] |
| Storage | Outbox message 可查询 pending/published 状态 | L1/L2 | [待补齐] |
| Real Agent | Codex CLI 完成固定 fixture 任务 | L3 | [发布必跑] |
| Real Agent | Claude CLI 完成固定 fixture 任务 | L3 | [发布必跑] |
| Real Agent | 前置命令失败时任务失败并记录 stderr | L3 | [待补齐] |
| Real Agent | 后置命令失败时任务失败并记录 stderr | L3 | [待补齐] |
| Real Agent | Agent 输出中的敏感 env var 值被遮蔽 | L3 | [待补齐] |

## 发布前准入流程

发布候选必须按顺序完成：

1. Go 单元测试与覆盖率门禁：

   ```bash
   make test-go
   make coverage
   ```

2. L1 Go E2E：

   ```bash
   make e2e
   ```

3. 本地完整栈启动：

   ```bash
   make run-local
   ```

4. L2 Playwright UI E2E：

   ```bash
   npm install
   npm run e2e
   ```

5. L3 真实 Agent 发布准入：

   ```bash
   GO_BIN=go WORKER_DIR=./worker-data-real-e2e npm run e2e:real-agents
   ```

   在脚本启动 Manager/Worker 后，分别创建 `agentType=codex` 和 `agentType=claude` 的固定任务，并按 L3 验收查询确认两条任务完成。

6. 关闭本地栈：

   ```bash
   make stop-local
   ```

发布阻断条件：

- 任一 L1/L2 自动化用例失败。
- Codex 或 Claude 任一真实 Agent 固定任务未完成。
- 真实 Agent 任务没有日志、会话或最终结果。
- Worker 工作目录出现跨任务污染或无法解释的残留。
- `readyz` 在目标存储模式下不稳定。

## 测试数据、隔离与清理

- 自动化用例使用时间戳后缀创建 Project、Worker 和 Task，避免名称冲突。
- Playwright 模拟 Worker 使用唯一 `workerId`，并在用例结束时关闭 WebSocket。
- Worker 工作目录使用测试专属路径，例如 `/tmp/e2e-worker`、`/tmp/e2e-board-groups-worker` 或 `./worker-data-real-e2e`。
- 真实 Agent 发布测试使用固定 fixture 仓库或本地 fixture，不能直接使用生产仓库。
- PostgreSQL 本地验证结束后可使用 `make clean-local` 清理 volume 和 Worker 数据。
- 失败现场需要保留 Playwright trace、截图、Manager 日志、Worker 日志和 Worker 工作目录，直到问题完成归因。

## 截图、Trace、日志与排障

常见失败与排查方向：

| 现象 | 优先检查 |
| --- | --- |
| UI 打不开或 `flutter-view` 不可见 | `BPT_UI_URL`、UI 容器端口、Flutter web build、浏览器控制台 |
| GraphQL 请求失败 | `BPT_MANAGER_GRAPHQL_URL`、Manager `/healthz`、Manager 日志、GraphQL response errors |
| Worker WebSocket 连接失败 | `BPT_MANAGER_WS_URL`、`WORKER_TOKEN` 与 `BPT_MANAGER_WS_TOKEN` 是否一致、`/worker/ws` 查询参数 |
| 任务停在 `ASSIGNED` | Worker 是否在线、是否支持目标 Agent、是否绑定目标 Project、是否空闲 |
| 任务停在 `STARTING` | Worker 是否收到 `TASK_START`、是否上报 `TASK_ACCEPTED`/`TASK_STARTED` |
| 日志或会话缺失 | Worker 是否上报 `TASK_LOG`/`TASK_CONVERSATION`，Manager 是否拒绝了消息或 messageId 被去重 |
| Playwright 看板截图为空 | UI 是否加载完成、测试数据是否写入 Manager、浏览器 viewport 是否为 `1400x900` |
| 真实 Agent 任务失败 | CLI 是否登录、fixture 是否可访问、worktree 是否创建成功、前置/后置命令输出、Agent stderr |
| PostgreSQL readiness 失败 | `DB_DSN`、PostgreSQL healthcheck、迁移日志、网络连通性 |

建议保留的失败证据：

- `test-results/` 下的 Playwright trace、截图和视频。
- Manager 启动日志与 GraphQL 错误响应。
- Worker stdout/stderr 与 Worker 工作目录。
- 失败任务的 `taskEvents`、`taskLogs`、`taskConversations` 查询结果。

## 文档维护规则

- 新增 E2E 用例时，同时更新“现有测试文件”和“全量场景矩阵”状态。
- 新增环境变量或测试入口时，同时更新“关键环境变量”和 README 中的简要入口。
- 将已经自动化的场景从 `[待补齐]` 改为 `[已实现]`，将发布人工准入场景标记为 `[发布必跑]`。
- 真实 Agent E2E 自动化增强后，仍保留 Codex 与 Claude 双 Agent 发布准入要求。
