# Block Play Table A2A 任务协议设计

状态：已实现并通过交付门禁
版本：1.0
日期：2026-08-10

## 1. 目标

Manager 使用 A2A v1.0 向 Worker 下发、继续、追踪、交互和取消任务。Worker 通过独立 Adapter 运行 Codex 与 Claude Code，并使用官方 Go SDK `github.com/a2aproject/a2a-go/v2@v2.4.0` 提供 JSON-RPC 与 SSE。

本设计遵循以下边界：

1. `/worker/ws` 只负责 Worker 注册、心跳和能力发现。
2. `/worker/frp` 继续提供反向隧道，并承载 A2A HTTP/SSE、Terminal、Review 和 Web Preview。
3. 所有旧 `TASK_*` WebSocket 消息立即删除，不提供回退协议。
4. 一个 Worker 进程只连接一个 Manager；多 Manager 场景必须启动多个 Worker 进程，并使用不同的 Worker ID、工作目录和 SQLite 文件。
5. A2A 标准字段是 task ID、context ID、状态、Message 和 Artifact 的唯一权威来源；扩展不得重复定义这些字段。

不在本次范围内：Secret Broker、mTLS、每 Manager 独立凭据、旧新版本混跑、跨 Worker 迁移活跃 CLI 进程。

## 2. 总体架构

```text
GraphQL mutation
  -> Manager 持久化 Task + A2A dispatch intent
  -> A2A reconciler
  -> 官方 a2aclient
  -> FRP RoundTripper
  -> /worker/frp (yamux)
  -> Worker-local /a2a (JSON-RPC + SSE)
  -> a2asrv.AgentExecutor
  -> TaskRuntime
  -> Codex Adapter | Claude Adapter

Worker event/artifact
  -> SQLite TaskStore + event journal
  -> SSE
  -> Manager event inbox + Task projection
  -> GraphQL query/subscription
  -> UI task detail / board
```

Worker 在 `127.0.0.1` 的动态端口启动 A2A Server。注册消息通过 capabilities 上报：

| Key | 值 |
| --- | --- |
| `a2a_version` | `1.0` |
| `a2a_transport` | `JSONRPC` |
| `a2a_host` | `127.0.0.1` |
| `a2a_port` | 实际监听端口 |
| `a2a_card_path` | `/.well-known/agent-card.json` |
| `a2a_endpoint_path` | `/a2a` |
| `a2a_extension` | `https://tangxusc.github.io/block-play-table/a2a/extensions/execution/v1` |

监听配置默认使用 `WORKER_A2A_HOST=127.0.0.1`、`WORKER_A2A_PORT=0`（动态端口）；TaskStore 默认位于 `<WORKER_WORK_DIR>/a2a.db`，可用 `WORKER_A2A_DB_PATH` 覆盖。Host 必须解析为 loopback 地址，禁止把 Worker A2A 服务直接暴露到外部网络。

Manager 必须通过 FRP 访问上述地址，不得直接信任 Agent Card 中的任意外部主机。Manager 仅允许请求注册能力声明的回环主机、端口和标准路径。

## 3. Agent Card

每个 Worker 发布一个 Agent Card，声明：

- A2A protocol version `1.0`；
- 唯一、非空且与注册能力完全一致的 JSON-RPC endpoint `/a2a`；
- streaming capability；
- `block-play-table-codex` Skill；
- `block-play-table-claude` Skill；
- execution v1 扩展，且 `required=true`。

缺少必需扩展的普通文本请求必须返回 `ExtensionSupportRequired`，不能使用默认权限启动 CLI。Worker 启动时探测两种 CLI 的可执行文件和版本；探测失败时 Worker readiness 失败，不注册为可调度 Worker。认证状态在真正执行时检查，并映射为 `AUTH_REQUIRED` 或明确失败。

## 4. 身份与生命周期

| 标识 | 生成方 | 语义 |
| --- | --- | --- |
| Manager Task ID | Manager | 长期业务任务 |
| execution ID | Manager | 一次初始执行或 retry；continue 复用 |
| attempt | Manager | 从 1 开始，retry 加 1 |
| turn | Manager | 同一 execution 内从 1 开始，continue 加 1 |
| round ID | Manager | 一条 GraphQL A2A 执行轮次记录 |
| command ID | Manager | 等于 A2A Message `messageId`，用于语义幂等 |
| A2A task ID | Worker/SDK | 一个 turn 对应一个 A2A Task |
| A2A context ID | Worker/SDK | 同一 execution 的多个 turn 共享 |
| CLI session ID | Adapter | Codex thread ID 或 Claude session ID |
| event ID | Worker | UUIDv7，重放时保持不变 |
| sequence | Worker | execution 内从 1 严格递增，跨 turn 连续 |

生命周期规则：

1. `START` 创建 execution、attempt 1、turn 1、新 A2A context/task、新 worktree 和新 CLI session。
2. `RETRY` 创建新 execution、attempt 加 1、turn 1、新 context/task/worktree/session；旧终态 Task 不复活。
3. `CONTINUE` 只允许 Manager Task 为 `COMPLETED`；复用 execution/context/worktree/session，turn 加 1，并创建新 A2A Task。
4. `INTERACTION_RESPONSE` 使用相同 A2A task ID 和 context ID 的新 Message，不创建新 turn。
5. interrupt 使用标准 `CancelTask`，不定义扩展取消命令。
6. Manager 对同一 Task 的执行 SSE 流实行独占调度；`INTERACTION_RESPONSE` 必须等待旧对账流退出后接管该流，`CancelTask` 可与活跃流并发以保证可中断性。

## 5. Execution v1 扩展

扩展 URI：

```text
https://tangxusc.github.io/block-play-table/a2a/extensions/execution/v1
```

Schema：

- `docs/a2a/extensions/execution/v1/request.schema.json`
- `docs/a2a/extensions/execution/v1/event.schema.json`
- `docs/a2a/extensions/execution/v1/artifact.schema.json`

运行时不得从公网下载 Schema。Schema 用于开发、契约测试、文档发布和第三方实现校验。

### 5.1 请求 Message

初次执行和 continue Message 包含一个用户 `TextPart` 以及一个必需的 request `DataPart`。请求字段包括：

- `command`：command ID、操作和发出时间；
- `scope`：本地 Task、execution、attempt、turn 和预期 Worker；
- `task`：标题、描述、分支及前后置命令；
- `agent`：Codex/Claude 类型、工作模式和强类型配置；
- `project`：项目 ID、Git URL、默认分支和 worktree 前缀；
- `environment`：普通和 sensitive 运行时变量；
- `resume`：continue 时的 worktree/session 预期值；
- `interaction`：交互回复时的 interaction ID、decision、message 和 payload。

Worker 必须校验 `scope.expectedWorkerId`。continue 中的 worktree/session 仅用于一致性校验，Worker 本地 runtime binding 才是权威来源；Worker 必须解析 `WORKER_WORK_DIR` 和 worktree 的符号链接后再校验真实路径边界，拒绝悬空链接及任何指向根目录外的路径。

### 5.2 事件

每个可投影事件包含：

- 固定 `kind=bpt.execution.event` 和 `version=1.0`；
- `event.id`、`sequence`、`type` 和 `occurredAt`；
- Manager 本地关联 scope；
- 可选的 CLI session/worktree runtime 信息；
- 按事件类型定义的 payload。

事件类型闭集：

```text
execution.accepted
workspace.ready
agent.session.started
agent.session.updated
log.chunk
conversation.message
interaction.requested
interaction.resolved
result.updated
execution.diagnostic
execution.terminal
```

Manager 以 `(roundId, eventId)` 去重，并校验相同 event ID 的规范化内容哈希。内容不一致属于 `A2A_PROTOCOL_CONFLICT`。日志、会话和诊断等业务投影的记录 ID 也必须包含 round ID，不能把仅在 round 内幂等的 event ID 当作全局主键。sequence 缺口不得跳过，必须通过 `GetTask` 的 Artifact 快照补齐。`execution.diagnostic` 的脱敏 `errorCode`、`message` 和 `retryable` 必须写入对应 round；`AUTH_REQUIRED` 只进入等待状态，不得提前结束 round。后续 `FAILED` 终态必须沿用已持久化的诊断信息。

Worker 在创建 Runtime binding 或 `execution.accepted` 前拒绝请求时，返回标准 `REJECTED` Task 和文本原因，不伪造 extension event、sequence 或 Artifact。Manager 只在该 Task 没有任何 execution event 且没有 Artifact 时接受这种早期拒绝；一旦 Worker 已发出 `execution.accepted`，所有 `COMPLETED`、`FAILED`、`REJECTED`、`CANCELED` 都必须由相同 `execution.terminal` Message 与 manifest Artifact 配对。

### 5.3 Artifact

Artifact role 闭集：

| Role | 用途 |
| --- | --- |
| `manifest` | execution 当前 sequence、turn、session、worktree 和压缩信息 |
| `log` | stdout、stderr、system 的可重放分块 |
| `conversation` | Manager 所需的规范化会话投影 |
| `interaction` | 交互请求与已解决记录 |
| `result` | turn 最终结果 |
| `output` | 文件、补丁、报告等标准 FilePart 输出 |
| `diagnostic` | 脱敏错误、阶段、错误码和 retryable 标记 |

日志每块最大 32 KiB，保留原始顺序和稳定 chunk index。CLI 单条 JSON 事件默认最大 16 MiB，加入 execution envelope 和 runtime metadata 后的完整事件也不得超过该限制；任一阶段超限都返回 `AGENT_EVENT_TOO_LARGE`，取消 Agent 子 context，并发布有界的 diagnostic 与 `FAILED` 终态，不能由 `bufio.Scanner` 静默丢弃或让 Task 停留在 `WORKING`。

## 6. 状态映射

| A2A 状态 | Manager 状态 | 规则 |
| --- | --- | --- |
| `SUBMITTED` | `STARTING` | 已接受但尚未开始 Agent |
| `WORKING` | `RUNNING` | worktree 准备完成或 Agent 已运行 |
| `INPUT_REQUIRED` | `WAITING_INPUT` | 标准状态 Message 携带交互 DataPart |
| `AUTH_REQUIRED` | `WAITING_INPUT` | UI 展示认证诊断；完成外部 CLI 认证后取消当前 round，并通过 retry 创建新 execution |
| `COMPLETED` | `COMPLETED` | 终态，不允许回退 |
| `FAILED` | `FAILED` | 使用 diagnostic 错误码 |
| `REJECTED` | `FAILED` | 接受前可使用标准文本拒绝；接受后必须携带配对 terminal |
| `CANCELED` | `INTERRUPTED` | 用户取消完成 |
| `UNKNOWN` / `UNSPECIFIED` | 保持当前状态 | 查询确认或超时后才失败 |

本地 `INTERRUPTING` 是 CancelTask 已请求但远端尚未确认的中间状态。

固定错误码至少包括：

```text
WORKER_RESTARTED
WORKER_UNREACHABLE_TIMEOUT
A2A_TASK_NOT_FOUND
A2A_PROTOCOL_CONFLICT
A2A_MIGRATION_RETRY_REQUIRED
AGENT_EVENT_TOO_LARGE
AGENT_UNAVAILABLE
```

## 7. 持久化与事务

### 7.1 Worker SQLite

Worker 使用 `<WORKER_WORK_DIR>/a2a.db`，至少保存：

- SDK Task 快照与 OCC version；
- command inbox 和非敏感规范化 payload hash；
- execution/turn/runtime binding；
- event journal、sequence 和 Artifact；
- retention/compaction 状态。

TaskStore 的 `Update` 必须比较 `PrevVersion`，版本冲突返回 SDK `taskstore.ErrConcurrentModification`。同 command ID、相同非敏感 payload 返回原 Task；同 ID、不同 payload 返回 conflict。

Worker 启动恢复必须先处理 CONTINUE 的落盘窗口：如果 binding 已推进到新 turn，但对应 A2A Task 尚未落盘，则严格校验已脱敏 command 的 execution/context/attempt/turn/Worker/Agent 与 resume 信息，并把 binding 以 CAS 回滚到同 context 的上一终态 Task；回滚保留原 `last_sequence`、session 和 worktree。随后才清理缺失 Task 对应的 command 与无法回滚的孤儿 binding；START/RETRY 的孤儿记录不复用旧运行时。

Worker 启动时把未终态 Task 原子更新为 `FAILED/WORKER_RESTARTED`，并连续写入 `execution.diagnostic`、`execution.terminal`、diagnostic Artifact、manifest Artifact 和携带相同 terminal 事件的标准 `FAILED` 状态 Message，确保重启恢复仍满足终态配对规则。启动恢复事务后立即执行一次 retention/compaction；进程存活期间每 24 小时重复执行。终态 Task 的完整 Artifact 和事件保留 90 天，超过期限后仅保留最新 manifest、result、diagnostic Artifact 与终态状态，同时保留 execution binding 中的 context、session 和 worktree，保证续接与审计身份不丢失。event journal 只有在事件自身 `created_at` 已过期且其 `(executionId, turn)` 已有终态 Task 时才删除；同 execution 的后续活跃 turn 即使事件时间已过期也必须保留。command inbox 只清空过期 `request_json`，继续保留 command hash、task/context/execution 等幂等摘要。

### 7.2 Manager Store

Manager 至少保存：

- A2A round：execution、attempt、turn、Worker、A2A task/context、远端状态、sequence、同步时间和错误；
- dispatch intent：command ID、操作、期望状态和发送状态；
- event inbox：event ID、内容哈希、sequence 和投影状态。

GraphQL mutation 必须先在同一事务中保存 Task、Worker、领域事件和 dispatch intent，然后才能发送网络请求。A2A event inbox、Task/日志/会话/交互/结果投影、round cursor 和领域事件也必须在同一事务提交，禁止沿用“先去重、后业务更新”的顺序。

## 8. 执行与交互

`TaskRuntime` 生命周期绑定 Worker 进程，而不是 WebSocket、FRP、HTTP 或 SSE 请求 context。请求 context 取消只解除订阅。

Codex Adapter 继续使用 `codex app-server --listen stdio://`，Claude Adapter 继续使用 `claude -p --output-format=stream-json --verbose`。Adapter 负责 CLI 协议，TaskRuntime 负责 worktree、前后置命令、环境变量、Review、事件 journal、交互路由和进程取消。

Codex 请求审批时，Runtime 保持 app-server 进程和等待通道；AgentExecutor 发送 `INPUT_REQUIRED` 后结束当前流。下一条同 Task Message 投递 response 并重新订阅 runtime。Claude permission denial 保留 session 和允许工具集合，响应后继续原 execution。

## 9. 网络、恢复与超时

- 普通 A2A 控制调用 deadline 为 15 秒。
- 任务和用户交互不设总超时。
- Worker 使用 SDK `WithTransportKeepAlive(15s)` 发送 SSE comment。
- Manager 的每条流具有独立字节探针；HTTP Client 流式请求总超时为 0。
- 60 秒没有任何网络字节时，Manager 先用独立 15 秒 context 调用 `GetTask`。
- Task 非终态且查询成功时重建 `SubscribeToTask`；无法订阅时退化为定期 `GetTask`。
- `GetTask` 返回活跃状态后，Task 可能在 `SubscribeToTask` 建立前结束；订阅返回 `TaskNotFound` 时必须立即重查一次快照，连续无法订阅后才退化为轮询。
- `GetTask` 返回 `INPUT_REQUIRED` 或 `AUTH_REQUIRED` 时，Manager 完成快照投影后结束当前恢复流，不再对没有活跃 SDK execution 的等待态 Task 重复订阅；后续回复、取消或 retry 使用独立控制命令。
- Worker/FRP 断线后本地 Task 保持 60 秒，期间重连和对账；超时后失败为 `WORKER_UNREACHABLE_TIMEOUT`。
- 确认 Task 不存在时失败为 `A2A_TASK_NOT_FOUND`。

SSE 断开不能取消 Agent。Manager 重启后从非终态 round 恢复订阅并先执行 `GetTask` 对账。

## 10. 鉴权与敏感数据

设置 `WORKER_TOKEN` 时，Manager 与 Worker 必须使用相同值；Manager 对 Worker A2A 请求使用 `Authorization: Bearer <token>`，WS 与 FRP 保持现有 token 校验。空 token 仅允许两端同时用于既有 trusted-local 模式，Worker interceptor 仍为请求设置固定的已认证本地主体，不能使用 SDK 匿名空用户。

sensitive 环境变量允许经认证 FRP 传输，但只进入 Runtime 内存：

1. Worker TaskStore 保存前必须把 value 替换为固定占位符；
2. command payload hash包含 key、sensitive 标记，不包含原值；
3. 日志、错误、Artifact、trace 和结构化日志不得包含原值；
4. 日志分块脱敏必须处理 secret 跨块边界；
5. Worker 重启后不恢复 secret，旧活跃任务直接失败。

## 11. GraphQL 与 UI

新增查询：

```graphql
taskA2AExecutions(taskId: ID!): [TaskA2AExecution!]!
```

`TaskA2AExecution` 字段：

```text
id executionId attempt turn operation workerId
a2aTaskId contextId remoteStatus lastSequence lastSyncedAt
errorCode errorMessage createdAt completedAt
```

现有 mutation 保持不变。任务详情新增紧凑轮次表，展示 attempt/turn、远端状态、A2A task/context、同步时间和错误；领域事件触发后与日志、会话、交互、结果一起刷新。

## 12. 升级与兼容

Manager 与 Worker 必须停机同步升级。新 Manager 只调度声明 A2A v1.0 必需扩展的 Worker。

启动迁移查找没有 A2A round 且状态为 `STARTING/RUNNING/WAITING_INPUT/INTERRUPTING` 的旧任务，将其置为 `FAILED/A2A_MIGRATION_RETRY_REQUIRED`，释放 Worker current task 占用并写入领域事件。用户 retry 后创建全新 execution。

旧的逗号分隔多 Manager 配置已删除；检测到该配置时 Worker 启动失败，并提示为每个 Manager 启动独立 Worker 和独立数据目录。

## 13. 验证门禁

必须通过：

```text
make test
make coverage
make fuzz
make e2e
make run-local
npm run e2e
npm run e2e:real-agents
```

Go 非生成生产代码语句覆盖率和 `gobco@v1.3.4` 可识别分支汇总均不低于 80%。UI 手写代码的 statements、branches、functions、lines 均不低于 80%。`gobco` 不覆盖 `select`、`range`，短路表达式 branch 模式只统计整体结果；这些路径必须由定向测试与 fuzz 补足，不能从分母中进一步排除。

## 14. 实施前自检

自检于 2026-08-10 完成。JSON 语法、Schema 关键枚举、敏感字段持久化标记、扩展 ID 和文档链接均已通过自动断言。

- [x] A2A 标准字段与扩展字段无重复权威来源。
- [x] START、RETRY、CONTINUE、交互和取消的 ID/状态规则闭合。
- [x] SSE 断流、Manager 重启、Worker 重启和 60 秒失联均有确定收敛路径。
- [x] sensitive 原值不会进入 Worker 持久化、日志或 Artifact。
- [x] command/event 幂等、OCC、sequence 缺口和终态防回退规则明确。
- [x] 单 Manager/Worker、90 天压缩和旧任务迁移规则明确。
- [x] GraphQL/UI、测试、覆盖率、fuzz 和真实 Agent 验收范围明确。
- [x] 三个 JSON Schema 可由标准 JSON 解析器读取，且 `$id` 与发布 URI 一致。
