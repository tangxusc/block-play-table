# 端到端测试与质量门禁

本文档定义 Block Play Table 的端到端验证、覆盖率、模糊测试和发布准入。任务主流程必须经过真实 A2A 协议栈；测试不得直接伪造已经下线的 Worker 任务消息。

## 1. 验证目标

端到端测试覆盖以下用户可感知闭环：

1. 通过 GraphQL/UI 创建 Project、注册和编辑 Worker、创建并分配 Task。
2. `startTask` 先原子保存 A2A round 与 dispatch intent，再由 Manager 通过 FRP 发现 Worker Agent Card，并使用官方 A2A SDK 发起流式执行。
3. Worker A2A Server 使用测试 Adapter 执行确定性 Codex/Claude fixture，将标准 Task 状态和 execution v1 DataPart/Artifact 投影为任务状态、日志、会话、交互和结果。
4. UI 展示 A2A 轮次、远端状态、日志、会话、结果和领域事件，并在订阅事件或兜底刷新后更新看板分组。
5. 用户回复交互时引用既有 A2A Task/Context；中断使用标准 `CancelTask`；Manager 重启后通过 `GetTask`/`SubscribeToTask` 恢复追踪。
6. Worker 断线、乱序、重复事件和部分 Artifact 不得破坏幂等、序列连续性和终态单调性。
7. 真实 Codex/Claude CLI 在发布门禁中使用同一 Adapter/A2A 主流程完成固定任务。

E2E 不替代字段级单元测试或安全渗透测试。系统仍是 trusted mode；`WORKER_TOKEN` 非空时，浏览器入口、Worker 注册/FRP 和 A2A Bearer 鉴权使用同一固定 token。

## 2. 测试分层

| 层级 | 入口 | 验证范围 | 默认时机 |
| --- | --- | --- | --- |
| L1 Go E2E | `make e2e` | 进程内 Manager、FRP、Worker A2A Server、纯 Go 测试 Adapter、GraphQL、持久化和恢复 | 每次交付、CI |
| L2 Playwright E2E | `npm run e2e` | 运行中的 Vue/Manager，加生产 Worker 与确定性假 CLI，覆盖主要 UI 工作流 | 每次交付、CI |
| L3 Real Agent E2E | `npm run e2e:real-agents` | 生产 Adapter、真实 Codex/Claude CLI、认证和完整 A2A/FRP 链路 | 涉及 Agent/Worker/发布流程时，发布前 |

关键测试文件：

- `manager/e2e/trusted_flow_test.go`：L1 A2A 任务下发、流式事件、日志/结果投影和 FRP/Review 闭环。
- `e2e/block_play_table.spec.ts`：L2 项目、Worker、任务、交互、日志、会话、结果、A2A 轮次、终端、Review 和分页流程。
- `e2e/board_status_groups.spec.ts`：L2 保持看板页面打开，通过详情页 Start 和审批操作驱动任务从 Ready 进入 In Progress 再进入 Done，验证真实 A2A 状态投影与订阅刷新。
- `scripts/real_agent_e2e.sh`：L3 启动生产 Manager/Worker，并分别验证 Codex 与 Claude。

测试 Adapter 只存在于测试代码。它实现与生产 Adapter 相同的 Runtime 边界，但输出确定性的 SDK Task/Artifact；Playwright 使用生产 Worker 进程和可执行假 CLI，因此 Agent Card、JSON-RPC/SSE、鉴权、FRP、SQLite TaskStore、幂等 inbox 和 Manager 投影均为真实实现。

## 3. 本地完整验证

安装依赖并启动完整栈。`make run-local` 会先停止状态文件记录的旧实例，并确认 Manager/UI 端口已经释放；若检测到未受状态文件管理的旧服务，或任一组件启动失败，脚本会失败并回收本次已启动的进程，避免多个同 ID Worker 互相替换连接。

```bash
npm install
make run-local
```

确认以下入口可用：

- UI：`http://localhost:18080`
- Manager：`http://localhost:8080`
- GraphQL：`http://localhost:8080/graphql`
- Worker 注册/心跳：`ws://localhost:8080/worker/ws`
- Worker FRP：`ws://localhost:8080/worker/frp`

随后运行：

```bash
make test
make coverage
make fuzz
make e2e
npm run e2e
```

涉及真实 Agent 执行、Worker Runtime、CLI 参数、worktree 或发布流程时，还必须运行：

```bash
npm run e2e:real-agents
```

完成后清理：

```bash
make stop-local
```

任何一步失败都必须先按 Observation、Hypothesis、Verification、Implementation、Evidence 顺序定位和修复，不能以静态检查代替主流程验证。

## 4. 常用环境变量

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `BPT_UI_URL` | `http://localhost:18080` | Playwright UI 地址 |
| `BPT_MANAGER_URL` | 从 GraphQL URL 推导 | UI 配置的 Manager 基础地址 |
| `BPT_MANAGER_GRAPHQL_URL` | `http://localhost:8080/graphql` | Playwright 数据准备与断言 |
| `BPT_MANAGER_TOKEN` | `WORKER_TOKEN` 或 `dev-worker-token` | 浏览器/GraphQL Bearer token |
| `MANAGER_ADDR` | `127.0.0.1:18081` | Real Agent E2E 隔离 Manager 地址，避免占用本地 UI 的 `18080` |
| `WORKER_TOKEN` | 本地栈使用 `dev-worker-token` | Manager、Worker 注册/FRP 与 A2A Bearer 鉴权 |
| `MANAGER_WS_URL` | `ws://localhost:8080/worker/ws` | 单个 Worker 的注册/心跳入口，并用于派生 FRP 地址 |
| `WORKER_ID` | `worker-local` | Worker 稳定身份 |
| `WORKER_WORK_DIR` | `./worker-data` | worktree 与 Worker A2A SQLite 数据目录 |
| `WORKER_A2A_HOST` | `127.0.0.1` | Worker A2A loopback 监听地址；非 loopback 会拒绝启动 |
| `WORKER_A2A_PORT` | `0` | Worker A2A 监听端口；`0` 表示动态分配 |
| `WORKER_A2A_DB_PATH` | `<WORKER_WORK_DIR>/a2a.db` | Worker A2A Task、binding 和 journal 的 SQLite 文件 |
| `REAL_AGENT_CODEX_MODEL` | 空 | L3 Codex 显式模型 |
| `REAL_AGENT_CLAUDE_MODEL` | 空 | L3 Claude 显式模型 |

Worker 固定探测并发布 Codex 与 Claude 两种 Adapter，不提供按环境变量裁剪能力的启动模式。L2 使用两个确定性假 CLI，L3 必须同时提供已认证的真实 Codex 和 Claude CLI。

一个 Worker 进程只连接一个 Manager。测试多个 Manager 时必须启动相互隔离的 Worker 进程和数据目录，避免 A2A Task、command inbox 或 worktree 所有权混淆。

## 5. 覆盖率与模糊测试

`make coverage` 串联三类独立 80% 门禁：

1. Go 语句覆盖率：Manager、Worker、共享包完整插桩，再按标准 `// Code generated ... DO NOT EDIT.` 标记精确过滤文件。GraphQL 包中的手写 resolver/helper 仍在分母。
2. Go 分支覆盖率：固定 `gobco v1.3.4`，按整包插桩和测试，解析 `-stats` JSON 中每个条件的 true/false 方向，再精确过滤生成文件。gobco 必须整包运行；逐文件插桩会丢失同包类型信息。
3. Vue 覆盖率：Vitest/Istanbul 对 statement、branch、function、line 分别执行 80% 阈值。

覆盖产物：

- `coverage-manager.out`、`coverage-worker.out`、`coverage-pkg.out`
- `coverage-manager.txt`、`coverage-worker.txt`、`coverage-pkg.txt`、`coverage.txt`
- `.coverage/go-statement/`
- `.coverage/go-branch/report.tsv`
- `ui/coverage/`

`make fuzz` 自动发现仓库内所有 `Fuzz*` 并逐个运行。CI 默认使用有界时长；快速本地复现可使用：

```bash
FUZZ_TIME=1x make fuzz
```

新增解析器、协议事件或日志脱敏逻辑时，必须同时考虑 seed corpus、随机输入上限、panic、UTF-8、超大 payload 和敏感值跨分块场景。

## 6. A2A 主流程断言

每个 START/RETRY/CONTINUE 轮次至少断言：

1. Manager 在网络发送前已持久化唯一 `commandId`、round 和 `PENDING` intent。
2. Worker 注册 capability、Agent Card、协议版本、JSON-RPC endpoint 和 required execution extension 完全一致。
3. 首个流事件在控制超时内到达，Task ID/Context ID 一旦绑定便不可变。
4. Worker 接受 execution 后，`execution.accepted`、`workspace.ready`、日志、会话、结果和 `execution.terminal` 的 sequence 连续且可重放；接受前的标准 `REJECTED` 不创建 execution event。
5. 重复 command 返回同一 A2A Task；相同 event ID/内容幂等，冲突内容被拒绝。
6. 终态只允许 `COMPLETED`、`FAILED`、`REJECTED` 或 `CANCELED`，且不会回退到非终态。
7. 日志分块不超过 32 KiB；敏感环境变量在 Manager/Worker 持久化、错误和 Artifact 中均为 `[REDACTED]`。
8. Task Detail 的 A2A 轮次、任务状态、日志、会话、结果和领域事件最终一致。

恢复与异常测试至少覆盖：

- Manager 在 dispatch 前、绑定远端 Task 后、投影中途和终态前重启。
- SSE 中断或 idle timeout 后 `GetTask` 对账，再重新订阅。
- Artifact 序列缺口触发快照恢复，不猜测或跳过缺失事件。
- Worker 重启把内存中非终态 A2A Task 收敛为稳定失败，并保留恢复摘要。
- 无法确认远端 Task 不存在时保持可恢复状态；确认不存在后使用稳定错误码失败。

## 7. Playwright 主要工作流

浏览器门禁必须覆盖并通过：

1. 首次进入配置 Manager URL/token。
2. Project 创建、编辑和归档。
3. Worker 注册、编辑、Project 绑定和能力展示。
4. Task 创建、分配、Agent 配置，以及从 Task Detail 发起 Start。
5. 同一 Task Detail 弹窗在审批和 continue 后自动刷新 A2A task/context、START/CONTINUE 轮次状态、日志、会话和结果。
6. 看板四组状态及 Ready → In Progress → Done 的订阅换列、Calendar 和 Archived 删除流程。
7. Task/Worker terminal 可用性与基本交互。
8. Review diff、stage/unstage、discard/restore、fetch/rebase、commit 和 publish 防护。

真实 Agent 的 CONTINUE 还必须保持首次执行的 `agentSessionId` 与 `worktreePath`，并确认审批轮次创建的 token 命名标记文件在续接后仍存在；仅验证第二轮文本结果不足以证明会话和工作区连续性。L3 使用 `touch` 创建标记文件，避免不同 Claude Code 版本对 shell 输出重定向实施额外路径检查而绕开标准 `--allowedTools` 恢复语义。Codex Adapter 固定使用 `approvalsReviewer=user`，因此 L3 即使运行在配置了本机 `auto_review` 的主机上，也必须由 Manager 收到并完成审批。

用例必须使用唯一 Worker ID、工作目录和 A2A SQLite 路径，并在 `finally`/fixture teardown 中终止测试 Worker、关闭 FRP、删除临时资源。失败时保留 Playwright trace、截图、Manager/Worker 日志和 A2A round 标识。

## 8. CI 与发布证据

CI 顺序要求：

1. `npm ci`
2. `make test`
3. `make coverage`
4. `make fuzz`
5. `npm run validate:a2a`
6. `make build`、UI typecheck/build、Docker build
7. `make e2e`
8. `make run-local` 后运行 `npm run e2e`
9. 使用 `always()` 执行 `make stop-local` 并上传日志、覆盖率、Playwright trace/report

Pages 工作流必须把 `docs/a2a/extensions/` 复制到发布根目录的 `/a2a/extensions/`，并在构建前执行 schema/示例校验，确保 Agent Card 中的稳定 extension URI 可访问。

交付证据至少包含每条门禁命令的退出码和关键统计。若本地环境无法监听端口、缺少 CLI/凭据或无法下载依赖，必须明确列出未通过命令、实际错误和需要补跑的环境，不能标记为完成。
