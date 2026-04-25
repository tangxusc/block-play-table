# Block Play Table 系统设计文档

## 1. 背景与目标

Block Play Table 是一个面向 AI Agent 任务编排、执行和观察的平台。用户通过 Web、桌面端或移动端创建任务，Manager 负责统一管理任务、Worker、看板、系统配置和领域事件，Worker 负责在具体执行环境中运行任务，包括创建 Git Worktree、初始化环境、启动命令、调用 Codex / Claude 等 Agent，并持续向 Manager 上报日志、状态和执行结果。

当前技术栈：

| 模块 | 技术 |
| --- | --- |
| 用户界面 | Flutter |
| API | GraphQL |
| GraphQL 服务端 | gqlgen |
| Manager / Worker | Golang |
| Manager 与 Worker 通信 | WebSocket |
| Manager 存储 | SQLite / PostgreSQL |

核心模块：

1. 用户界面：负责任务创建、任务观察、看板展示、Project 管理、Worker 管理和系统设置。
2. Manager：系统控制中心，负责业务编排、项目管理、任务调度、状态管理、事件管理和数据持久化。
3. Worker：任务执行节点，负责执行 Manager 下发的任务并持续上报执行过程。

Manager 采用 DDD 设计，`domain` 是系统核心，领域事件是一等公民。所有关键业务行为都应产生领域事件，并支持持久化、订阅、审计和后续扩展。

## 2. 业务架构

### 2.1 业务目标

系统需要支持以下核心业务能力：

1. 用户创建、编辑、启动、中断和归档 AI Agent 任务。
2. 用户通过任务列表、任务详情、日历视图和 Kanban 看板观察任务。
3. Manager 统一管理 Worker 的注册、连接、心跳、状态和任务占用。
4. Manager 根据 Worker 状态进行手动或自动任务分配。
5. Project 统一管理 Git URL、默认分支和 Git Worktree 名称前缀。
6. Worker 创建 Git Worktree，准备任务执行环境，并启动 Codex / Claude 等 Agent。
7. Worker 将命令输出、AI 对话、任务状态和最终结果实时上报给 Manager。
8. Manager 将领域事件、任务日志、AI 对话和任务状态持久化。
9. 系统设置支持配置 Agent 运行时环境变量，并在任务启动时下发给 Worker。
10. 用户界面通过 GraphQL Query / Mutation / Subscription 与 Manager 交互。
11. 系统支持本地轻量部署和团队服务端部署。

### 2.2 业务角色

#### 用户

用户是任务的发起方、观察方和控制方。

主要操作：

1. 创建任务。
2. 设置任务标题、任务描述、Project、目标分支、Agent 类型和执行命令。
3. 选择 Worker 或使用 Manager 自动分配 Worker。
4. 启动任务。
5. 查看任务状态、日志、AI 对话、执行结果和领域事件。
6. 中断任务、重试任务或归档任务。
7. 管理 Project、Worker 和系统配置。

#### Manager

Manager 是业务控制中心。

主要职责：

1. 接收用户界面的 GraphQL 请求。
2. 管理任务生命周期。
3. 管理 Worker 注册、心跳、在线状态和负载状态。
4. 选择合适 Worker 并下发任务。
5. 接收 Worker 上报的任务事件、日志、对话和结果。
6. 持久化聚合状态、领域事件、任务日志和 AI 对话。
7. 将任务变化通过 GraphQL Subscription 推送给用户界面。
8. 通过 WebSocket 与 Worker 建立双向通信。

#### Worker

Worker 是具体任务执行节点。

主要职责：

1. 主动连接 Manager。
2. 定期发送心跳。
3. 上报自身能力，例如支持的 Agent、工作目录和系统信息。
4. 接收 Manager 下发的任务启动、中断、取消等命令。
5. 根据 Project 的 Git URL 和 worktree 名称前缀创建 Git Worktree。
6. 初始化任务环境。
7. 注入系统设置中的 Agent 运行时环境变量。
8. 执行前置命令和后置命令。
9. 启动 Codex / Claude 等 Agent。
10. 持续读取 Agent 输出。
11. 将日志、AI 对话、状态变化和最终结果上报给 Manager。

## 3. 业务模块

### 3.1 用户界面模块

用户界面使用 Flutter 实现，支持 Web、桌面端和 App。

主要页面：

| 页面 | 说明 |
| --- | --- |
| 任务列表 | 查看所有任务，支持状态、Project、Worker、时间过滤 |
| 任务创建页 | 创建任务，选择 Project、Agent 和 Worker |
| 任务详情页 | 查看任务基础信息、状态、日志、AI 对话和结果 |
| 看板页 | 以 Kanban 或日历方式查看任务 |
| Worker 管理页 | 查看 Worker 列表、状态、能力、当前任务 |
| Project 管理页 | 管理项目 Git URL、默认分支、worktree 名称前缀 |
| 系统设置页 | 管理数据库、Token、安全策略、Agent 运行时环境变量 |

### 3.2 Manager 模块

Manager 是系统核心，采用 DDD 架构。

主要子模块：

| 模块 | 说明 |
| --- | --- |
| GraphQL API | 为用户界面提供 Query、Mutation、Subscription |
| Worker Gateway | 管理 Worker WebSocket 连接和消息收发 |
| Project Application Service | 管理项目 Git URL、默认分支、worktree 命名前缀 |
| Task Application Service | 编排任务创建、分配、启动、中断和完成 |
| Worker Application Service | 编排 Worker 注册、心跳、状态变更 |
| Settings Application Service | 管理系统配置和 Agent 运行时环境变量 |
| Domain Event Store | 持久化领域事件 |
| Event Bus | 分发领域事件到订阅、WebSocket、异步处理器 |
| Repository | 屏蔽 SQLite / PostgreSQL 差异 |
| Scheduler | 自动选择可分配 Worker，调度任务执行 |

### 3.3 Worker 管理

Worker 管理包括：

1. Worker 列表。
2. Worker 新增、修改、删除。
3. Worker 启用和禁用。
4. Worker 在线状态观察。
5. Worker 心跳时间观察。
6. Worker 支持的 Agent 类型管理。
7. Worker 工作目录管理。
8. Worker 可绑定到一个或多个具体 Project，也可以设置为所有 Project 共用。
9. Worker 当前任务观察。
10. Worker 异常状态处理。

Worker 推荐状态：

| 状态 | 说明 |
| --- | --- |
| `REGISTERED` | 已注册但尚未连接 |
| `ONLINE` | 已连接 Manager |
| `OFFLINE` | 心跳超时或连接断开 |
| `ERROR` | Worker 异常 |
| `DISABLED` | 被禁用，不参与调度 |

Worker 状态只描述连接和可用生命周期，不描述任务占用。Worker 没有 `IDLE` 状态，也不通过 `BUSY` 状态表达正在执行任务；是否可分配由 `currentTaskId` 派生：

| 派生状态 | 判断规则 | 说明 |
| --- | --- | --- |
| 可分配 | `status = ONLINE` 且 `currentTaskId = null` | 可以接收新任务 |
| 已占用 | `currentTaskId != null` | 正在执行或关联某个任务 |

Worker 与 Project 的绑定模式：

| 绑定模式 | 说明 |
| --- | --- |
| `ALL_PROJECTS` | Worker 可执行所有 Project 的任务 |
| `SPECIFIC_PROJECTS` | Worker 只能执行绑定 Project 的任务 |

调度时必须同时满足 Worker 在线、`currentTaskId` 为空、支持目标 Agent、Project 绑定范围匹配。

### 3.4 任务管理

任务是系统的核心业务对象。

任务包含：

1. 任务标题。
2. 任务描述。
3. Project ID。
4. 基础分支。
5. 目标分支。
6. Git Worktree 路径。
7. 选择的 Agent 类型，例如 `codex`、`claude`。
8. 绑定的 Worker。
9. 前置命令。
10. 后置命令。
11. 任务状态。
12. 执行日志。
13. AI 对话记录。
14. 任务结果。
15. 创建时间和更新时间。

任务不保存 Git 仓库信息。Git URL、默认分支和 worktree 命名前缀都从 Project 获取，任务只引用 `projectId` 并保存本次执行生成的 `worktreePath`。

推荐任务状态：

| 状态 | 说明 |
| --- | --- |
| `CREATED` | 已创建 |
| `ASSIGNED` | 已分配 Worker |
| `STARTING` | 正在启动 |
| `RUNNING` | 正在执行 |
| `WAITING_INPUT` | 等待用户输入或确认 |
| `INTERRUPTING` | 正在中断 |
| `INTERRUPTED` | 已中断 |
| `COMPLETED` | 已完成 |
| `FAILED` | 已失败 |
| `ARCHIVED` | 已归档 |

### 3.5 Project 管理

Project 是 Git 仓库级别的业务对象。任务必须归属于某个 Project，Worker 调度也可以基于 Project 进行约束。

Project 包含：

1. Project 名称。
2. Git URL。
3. 默认分支。
4. Git Worktree 名称前缀。
5. 初始化命令。
6. 创建时间和更新时间。

Project 关键规则：

1. 一个任务只引用一个 Project。
2. 任务不再重复保存 Git URL。
3. Worker 执行任务时使用 Project 的 Git URL 获取代码。
4. Worker 创建 worktree 时使用 Project 的 worktree 名称前缀生成目录名。
5. Project 归档或删除前需要确认没有未完成任务。

worktree 命名建议：

```text
{project.worktree_prefix}-{task_id}-{short_time}
```

例如：

```text
block-play-table-task_01HR9A-04251030
```

### 3.6 系统设置

系统设置用于管理跨 Project 的全局运行配置。

主要能力：

1. 数据库配置。
2. 访问 Token 和 Worker Token。
3. 安全策略。
4. Agent 运行时环境变量。

Agent 运行时环境变量用于在 Worker 启动 Codex / Claude 等 Agent 时注入进程环境。

环境变量设计规则：

1. 支持设置变量名、变量值、说明、是否启用、是否敏感。
2. 敏感变量在 UI、日志和事件中必须脱敏展示。
3. Manager 在下发 `TASK_START` 时将启用的 Agent 环境变量发送给 Worker。
4. Worker 合并系统环境变量与本机环境变量后启动 Agent。
5. 环境变量变更应产生领域事件，便于审计。

### 3.7 Workspace 功能取舍

由于 Project 已经保存 Git URL、默认分支和 worktree 名称前缀，独立的 Workspace 业务功能在当前阶段不是必要模块。

推荐设计：

1. Project 是业务概念，表示一个 Git 项目。
2. Worker 的 `workDir` 是技术概念，表示 Worker 本地执行根目录。
3. 每次任务执行生成的 Git Worktree 是运行时资源，记录在 Task 的 `worktreePath` 中。
4. 不单独设计 Workspace 聚合、Workspace 页面和 Workspace 数据表。

后续只有在出现以下需求时再引入 Workspace 或类似概念：

1. 同一个 Project 需要在不同 Worker 上维护不同的本地仓库缓存配置。
2. 一个 Project 包含多个可独立执行的子目录、子仓库或模块。
3. 不同任务需要复用长期存在的固定工作区，而不是每次创建临时 worktree。
4. 需要对本地工作目录进行容量、清理、锁定和生命周期管理。

在当前设计中，Workspace 应被理解为 Worker 本地运行目录和任务 worktree，不作为用户侧独立管理对象。

### 3.8 看板管理

看板用于展示任务流转。

支持视图：

1. 列表视图。
2. 日历视图。
3. Kanban Board 视图。

推荐 Kanban 列：

| 列 | 说明 |
| --- | --- |
| 待处理 | 用户已创建，但尚未分配 Worker |
| 已分配 | 已绑定 Worker，但尚未启动 |
| 执行中 | Worker 正在执行 |
| 等待输入 | Agent 等待用户补充信息或确认 |
| 已中断 | 用户主动中断或系统中断 |
| 已失败 | 执行失败 |
| 已完成 | 任务成功完成 |
| 已归档 | 任务已归档 |

## 4. 任务执行流程

### 4.1 总体流程

```mermaid
sequenceDiagram
    participant U as 用户
    participant UI as Flutter UI
    participant M as Manager
    participant W as Worker
    participant A as Agent(Codex/Claude)

    U->>UI: 创建任务
    UI->>M: GraphQL Mutation createTask
    M->>M: 创建 Task 聚合
    M->>M: 持久化任务和 TaskCreated 事件
    M-->>UI: 返回任务 ID

    U->>UI: 分配 Worker 或自动分配
    UI->>M: GraphQL Mutation assignWorker
    M->>M: 查询可用 Worker
    M->>M: 绑定任务与 Worker
    M-->>UI: 返回分配结果

    U->>UI: 启动任务
    UI->>M: GraphQL Mutation startTask
    M->>M: 校验状态并生成 TaskStartRequested
    M->>W: WebSocket 下发 TASK_START

    W->>W: 读取 Project Git URL 和 worktree 前缀
    W->>W: 创建 Git Worktree
    W->>W: 注入 Agent 运行时环境变量
    W->>W: 初始化环境
    W->>W: 执行前置命令
    W->>A: 启动 Codex / Claude
    A-->>W: 输出日志、对话和结果

    W->>M: WebSocket 上报日志和事件
    M->>M: 持久化日志、状态和领域事件
    M-->>UI: GraphQL Subscription 推送更新

    W->>M: 上报任务完成或失败
    M->>M: 更新最终状态
    M-->>UI: 展示最终结果
```

### 4.2 创建任务流程

1. 用户在 UI 中填写任务标题、描述、Project、Agent 类型和命令配置。
2. UI 调用 Manager 的 `createTask` Mutation。
3. Manager 创建 `Task` 聚合。
4. `Task` 聚合产生 `TaskCreated` 领域事件。
5. Application Service 在同一事务中保存任务和领域事件。
6. Manager 返回任务 ID。
7. UI 跳转到任务详情页或看板页。

### 4.3 分配 Worker 流程

Worker 分配支持两种模式：

1. 手动分配：用户选择具体 Worker。
2. 自动分配：Manager 根据调度策略选择 Worker。

自动分配策略：

1. 只选择 `ONLINE` Worker。
2. 排除 `DISABLED`、`OFFLINE`、`ERROR` Worker。
3. 排除不支持目标 Agent 类型的 Worker。
4. 排除未绑定目标 Project 的专用 Worker。
5. 如果 Worker 配置为所有 Project 共用，则可参与任意 Project 的任务调度。
6. 只选择 `currentTaskId` 为空的 Worker。
7. 可选按标签、机器能力、最近任务数和负载进行排序。

对应领域事件：

1. `WorkerAssignedToTask`
2. `TaskAssigned`
3. `WorkerTaskAssigned`

### 4.4 启动任务流程

1. 用户点击启动任务。
2. UI 调用 `startTask` Mutation。
3. Manager 校验任务状态必须是 `ASSIGNED`。
4. Manager 校验 Worker 在线且 `currentTaskId` 为空。
5. Manager 生成 `TaskStartRequested` 领域事件。
6. Manager 通过 WebSocket 向 Worker 发送 `TASK_START`。
7. Worker 返回 `TASK_ACCEPTED`。
8. Manager 将任务状态更新为 `STARTING`。
9. Worker 开始本地任务执行。

### 4.5 Worker 本地执行流程

```mermaid
flowchart TD
    A[Worker 接收 TASK_START] --> B[拉取任务详情]
    B --> C[创建 Git Worktree]
    C --> D[初始化环境]
    D --> E[执行前置命令]
    E --> F[启动 Agent]
    F --> G[读取 Agent 输出]
    G --> H[上报日志、对话、状态]
    H --> I{任务是否结束}
    I -- 否 --> G
    I -- 是 --> J[执行后置命令]
    J --> K[上报最终结果]
```

Worker 执行步骤：

1. 接收 `TASK_START`。
2. 解析任务参数。
3. 根据任务的 `projectId` 获取 Project 的 Git URL、默认分支和 worktree 名称前缀。
4. 创建 Git Worktree。
5. 在 worktree 中切换或创建目标分支。
6. 注入系统设置中的 Agent 运行时环境变量。
7. 执行 Project 初始化命令。
8. 执行任务前置命令。
9. 启动 Codex / Claude。
10. 读取 stdout、stderr、AI 对话、工具调用和状态变化。
11. 通过 WebSocket 上报执行事件。
12. Agent 完成后执行后置命令。
13. 上报 `TASK_COMPLETED` 或 `TASK_FAILED`。

### 4.6 中断任务流程

1. 用户点击中断。
2. UI 调用 `interruptTask` Mutation。
3. Manager 校验任务状态是否允许中断。
4. Manager 生成 `TaskInterruptRequested` 领域事件。
5. Manager 通过 WebSocket 向 Worker 发送 `TASK_INTERRUPT`。
6. Worker 停止 Agent 进程。
7. Worker 清理子进程和临时资源。
8. Worker 上报 `TASK_INTERRUPTED`。
9. Manager 更新任务状态为 `INTERRUPTED`。
10. UI 展示中断结果。

## 5. Manager 领域模型设计

### 5.1 DDD 分层

推荐目录结构：

```text
manager/
  cmd/
    manager/
      main.go
  internal/
    domain/
      task/
      worker/
      project/
      settings/
      board/
      event/
    application/
      command/
      query/
      service/
    infrastructure/
      persistence/
        sqlite/
        postgres/
      websocket/
      graphql/
      eventbus/
      clock/
      idgen/
    interfaces/
      graphql/
      websocket/
```

分层职责：

| 层 | 职责 |
| --- | --- |
| `domain` | 聚合、实体、值对象、领域服务、领域事件、Repository 接口 |
| `application` | 用例编排、事务边界、命令处理、查询处理 |
| `infrastructure` | 数据库、WebSocket、事件总线、ID、时钟等技术实现 |
| `interfaces` | GraphQL Resolver、WebSocket Handler、DTO 转换 |

### 5.2 Task 聚合

Task 是核心聚合根。

主要字段：

```text
Task
- id
- title
- description
- status
- projectId
- workerId
- agentType
- baseBranch
- targetBranch
- worktreePath
- preCommands
- postCommands
- result
- version
- createdAt
- updatedAt
```

核心行为：

1. `CreateTask`
2. `UpdateTask`
3. `AssignWorker`
4. `Start`
5. `MarkRunning`
6. `AppendLog`
7. `AppendConversation`
8. `RequestInterrupt`
9. `MarkInterrupted`
10. `MarkFailed`
11. `MarkCompleted`
12. `Archive`

状态机：

```mermaid
stateDiagram-v2
    [*] --> CREATED
    CREATED --> ASSIGNED
    ASSIGNED --> STARTING
    STARTING --> RUNNING
    RUNNING --> WAITING_INPUT
    WAITING_INPUT --> RUNNING
    RUNNING --> INTERRUPTING
    INTERRUPTING --> INTERRUPTED
    RUNNING --> COMPLETED
    RUNNING --> FAILED
    STARTING --> FAILED
    CREATED --> ARCHIVED
    COMPLETED --> ARCHIVED
    FAILED --> ARCHIVED
    INTERRUPTED --> ARCHIVED
```

### 5.3 Worker 聚合

Worker 表示执行节点。

主要字段：

```text
Worker
- id
- name
- status
- capabilities
- supportedAgents
- workDir
- startupCommand
- projectBindingMode
- boundProjectIds
- currentTaskId
- lastHeartbeatAt
- version
- createdAt
- updatedAt
```

核心行为：

1. `Register`
2. `Connect`
3. `Heartbeat`
4. `MarkOffline`
5. `Disable`
6. `Enable`
7. `BindProjects`
8. `ShareAcrossAllProjects`
9. `AssignTask`
10. `ReleaseTask`

### 5.4 Project 聚合

Project 表示一个可执行任务的 Git 项目，是任务获取代码和创建 worktree 的来源。

主要字段：

```text
Project
- id
- name
- gitUrl
- defaultBranch
- worktreeNamePrefix
- setupCommands
- archived
- createdAt
- updatedAt
```

核心行为：

1. `CreateProject`
2. `UpdateGitUrl`
3. `UpdateDefaultBranch`
4. `UpdateWorktreeNamePrefix`
5. `ConfigureSetupCommands`
6. `ArchiveProject`

### 5.5 Settings 聚合

Settings 表示系统级配置。Agent 运行时环境变量属于系统设置的一部分。

主要字段：

```text
Settings
- id
- version
- agentRuntimeEnvVars
- workerHeartbeatTimeout
- securityPolicy
- createdAt
- updatedAt
```

核心行为：

1. `UpdateAgentRuntimeEnvVars`
2. `EnableAgentRuntimeEnvVar`
3. `DisableAgentRuntimeEnvVar`
4. `UpdateWorkerHeartbeatTimeout`
5. `UpdateSecurityPolicy`

Agent 运行时环境变量值对象：

```text
AgentRuntimeEnvVar
- key
- value
- description
- enabled
- sensitive
```

### 5.6 Board 聚合

Board 表示任务展示视图。

主要字段：

```text
Board
- id
- name
- type
- columns
- filters
- createdAt
- updatedAt
```

Board 类型：

1. `KANBAN`
2. `CALENDAR`
3. `LIST`

## 6. 领域事件设计

领域事件是一等公民，系统中的关键业务行为都应产生领域事件。

领域事件能力要求：

1. 事件可持久化。
2. 事件可订阅。
3. 事件可审计。
4. 事件可作为 GraphQL Subscription 的数据源。
5. 事件可作为 Worker 通信消息的触发源。
6. 事件可在后续扩展为异步任务、通知、外部集成。

### 6.1 事件基础结构

```text
DomainEvent
- eventId
- eventType
- aggregateType
- aggregateId
- aggregateVersion
- payload
- occurredAt
- correlationId
- causationId
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `eventId` | 事件唯一 ID |
| `eventType` | 事件类型 |
| `aggregateType` | 聚合类型，例如 `Task`、`Worker` |
| `aggregateId` | 聚合 ID |
| `aggregateVersion` | 聚合版本 |
| `payload` | 事件内容 |
| `occurredAt` | 发生时间 |
| `correlationId` | 请求链路 ID |
| `causationId` | 引发当前事件的上游事件 ID |

### 6.2 推荐事件列表

| 事件 | 说明 |
| --- | --- |
| `TaskCreated` | 任务已创建 |
| `TaskUpdated` | 任务已更新 |
| `TaskAssigned` | 任务已分配 Worker |
| `WorkerAssignedToTask` | Worker 已分配给任务 |
| `TaskStartRequested` | 用户请求启动任务 |
| `TaskStarted` | Worker 已开始任务 |
| `TaskLogAppended` | 任务日志已追加 |
| `TaskConversationAppended` | AI 对话已追加 |
| `TaskWaitingInput` | 任务等待用户输入 |
| `TaskInterruptRequested` | 用户请求中断任务 |
| `TaskInterrupted` | 任务已中断 |
| `TaskCompleted` | 任务已完成 |
| `TaskFailed` | 任务失败 |
| `TaskArchived` | 任务已归档 |
| `WorkerRegistered` | Worker 已注册 |
| `WorkerConnected` | Worker 已连接 |
| `WorkerHeartbeatReceived` | 收到 Worker 心跳 |
| `WorkerDisconnected` | Worker 断开 |
| `WorkerTaskAssigned` | Worker 绑定到任务，`currentTaskId` 已设置 |
| `WorkerTaskReleased` | Worker 释放任务，`currentTaskId` 已清空 |
| `WorkerDisabled` | Worker 被禁用 |
| `WorkerProjectBindingUpdated` | Worker 可执行 Project 范围已更新 |
| `ProjectCreated` | Project 已创建 |
| `ProjectUpdated` | Project 已更新 |
| `ProjectArchived` | Project 已归档 |
| `AgentRuntimeEnvUpdated` | Agent 运行时环境变量已更新 |

## 7. 技术架构

### 7.1 总体架构

```mermaid
flowchart LR
    UI[Flutter Web/Desktop/App] -->|GraphQL Query/Mutation/Subscription| M[Manager Go + gqlgen]
    M -->|Repository| DB[(SQLite/PostgreSQL)]
    M <-->|WebSocket| W1[Worker 1]
    M <-->|WebSocket| W2[Worker 2]
    M <-->|WebSocket| WN[Worker N]
    W1 --> A1[Codex/Claude]
    W2 --> A2[Codex/Claude]
    WN --> AN[Codex/Claude]
    W1 --> FS1[Project Git Worktree]
    W2 --> FS2[Project Git Worktree]
```

### 7.2 Flutter 前端架构

Flutter 前端只与 Manager 交互。

通信方式：

1. GraphQL Query：查询任务、Project、Worker、看板、系统设置和事件。
2. GraphQL Mutation：创建任务、分配 Worker、启动任务、中断任务等。
3. GraphQL Subscription：订阅任务状态、日志、AI 对话和 Worker 状态变化。

推荐前端分层：

```text
ui/
  pages/
  widgets/
  state/
  graphql/
  models/
  routing/
```

核心视图组件：

1. `TaskListPage`
2. `TaskCreatePage`
3. `TaskDetailPage`
4. `BoardPage`
5. `WorkerListPage`
6. `WorkerDetailPage`
7. `ProjectListPage`
8. `ProjectDetailPage`
9. `SettingsPage`
10. `RealtimeLogPanel`
11. `AgentConversationPanel`

### 7.3 Manager 技术架构

Manager 使用 Go 实现，GraphQL 框架使用 gqlgen。

```mermaid
flowchart TD
    GQL[GraphQL Resolver] --> APP[Application Service]
    APP --> DOMAIN[Domain Model]
    APP --> REPO[Repository Interface]
    DOMAIN --> EVENTS[Domain Events]
    APP --> EVENTBUS[Event Bus]
    REPO --> DB[(SQLite/PostgreSQL)]
    EVENTBUS --> SUB[GraphQL Subscription]
    EVENTBUS --> WS[Worker WebSocket Gateway]
    WS --> WORKER[Worker]
```

关键设计：

1. GraphQL Resolver 不写业务逻辑，只做参数转换和调用 Application Service。
2. Application Service 负责事务、聚合加载、领域行为调用、仓储保存和事件发布。
3. Domain 层不依赖 GraphQL、数据库、WebSocket 或具体框架。
4. Repository 接口定义在 Domain 或 Application 边界，实现放在 Infrastructure。
5. SQLite 和 PostgreSQL 通过同一组 Repository 接口适配。
6. 领域事件在事务内落库，事务提交后再发布。

### 7.4 Worker 技术架构

推荐 Worker 目录结构：

```text
worker/
  cmd/
    worker/
      main.go
  internal/
    client/
      manager_ws_client.go
    executor/
      task_executor.go
      command_runner.go
      process_manager.go
    project/
      project_client.go
    runtime/
      git_worktree.go
      environment.go
    agent/
      agent.go
      codex.go
      claude.go
    event/
      reporter.go
    config/
      config.go
```

Worker 模块职责：

| 模块 | 职责 |
| --- | --- |
| Manager Client | 连接 Manager WebSocket，处理重连和消息收发 |
| Heartbeat | 定时上报 Worker 状态 |
| Task Executor | 编排任务执行流程 |
| Project Client | 获取任务所属 Project 的 Git URL、默认分支和 worktree 前缀 |
| Runtime Manager | 创建 Git Worktree、准备运行目录、注入环境变量 |
| Command Runner | 执行前置命令和后置命令 |
| Agent Adapter | 适配 Codex、Claude |
| Process Manager | 管理子进程、中断和退出码 |
| Reporter | 上报日志、状态、领域事件和结果 |

### 7.5 Agent 适配

Agent 通过统一接口接入。

```go
type Agent interface {
    Type() AgentType
    Start(ctx context.Context, input AgentInput) error
    Interrupt(ctx context.Context) error
    Events() <-chan AgentEvent
}
```

Agent 类型：

1. `codex`
2. `claude`

统一 Agent 事件：

```text
AgentEvent
- type
- taskId
- content
- metadata
- occurredAt
```

事件类型：

1. `stdout`
2. `stderr`
3. `conversation`
4. `tool_call`
5. `file_changed`
6. `waiting_input`
7. `completed`
8. `failed`

## 8. GraphQL API 设计

### 8.1 核心类型

```graphql
type Project {
  id: ID!
  name: String!
  gitUrl: String!
  defaultBranch: String!
  worktreeNamePrefix: String!
  setupCommands: [String!]!
  archived: Boolean!
  createdAt: Time!
  updatedAt: Time!
}

type Worker {
  id: ID!
  name: String!
  status: WorkerStatus!
  supportedAgents: [AgentType!]!
  workDir: String!
  projectBindingMode: WorkerProjectBindingMode!
  boundProjectIds: [ID!]!
  currentTaskId: ID
  lastHeartbeatAt: Time
}

type Settings {
  agentRuntimeEnvVars: [AgentRuntimeEnvVar!]!
}

type AgentRuntimeEnvVar {
  key: String!
  valueMasked: String!
  description: String
  enabled: Boolean!
  sensitive: Boolean!
}
```

### 8.2 Query

```graphql
type Query {
  task(id: ID!): Task
  tasks(filter: TaskFilter, page: PageInput): TaskConnection!

  worker(id: ID!): Worker
  workers(filter: WorkerFilter): [Worker!]!

  project(id: ID!): Project
  projects(filter: ProjectFilter): [Project!]!

  board(id: ID!): Board
  settings: Settings!
  taskEvents(taskId: ID!): [DomainEvent!]!
}
```

### 8.3 Mutation

```graphql
type Mutation {
  createTask(input: CreateTaskInput!): Task!
  updateTask(input: UpdateTaskInput!): Task!
  assignWorker(input: AssignWorkerInput!): Task!
  startTask(input: StartTaskInput!): Task!
  interruptTask(taskId: ID!): Task!
  archiveTask(taskId: ID!): Task!

  createWorker(input: CreateWorkerInput!): Worker!
  updateWorker(input: UpdateWorkerInput!): Worker!
  updateWorkerProjectBindings(input: UpdateWorkerProjectBindingsInput!): Worker!
  deleteWorker(id: ID!): Boolean!

  createProject(input: CreateProjectInput!): Project!
  updateProject(input: UpdateProjectInput!): Project!
  archiveProject(id: ID!): Project!

  updateAgentRuntimeEnvVars(input: UpdateAgentRuntimeEnvVarsInput!): Settings!
}
```

### 8.4 Subscription

```graphql
type Subscription {
  taskUpdated(taskId: ID!): TaskEvent!
  taskLogAppended(taskId: ID!): TaskLog!
  taskConversationAppended(taskId: ID!): ConversationMessage!
  workerUpdated(workerId: ID): WorkerEvent!
  domainEvents(filter: DomainEventFilter): DomainEvent!
}
```

## 9. WebSocket 协议设计

### 9.1 连接方向

推荐由 Worker 主动连接 Manager。

```text
Worker -> Manager
```

原因：

1. Worker 可能部署在内网、本地机器或开发机。
2. Manager 不需要主动访问 Worker。
3. 更容易穿透 NAT。
4. Worker 可自行实现断线重连。

### 9.2 基础消息结构

```json
{
  "messageId": "msg_001",
  "type": "TASK_START",
  "workerId": "worker_001",
  "taskId": "task_001",
  "timestamp": "2026-04-25T10:00:00Z",
  "payload": {}
}
```

### 9.3 Manager 下发 Worker 的消息

| 消息类型 | 说明 |
| --- | --- |
| `TASK_START` | 启动任务，payload 包含任务信息、Project Git URL、worktree 前缀、Agent 运行时环境变量 |
| `TASK_INTERRUPT` | 中断任务 |
| `TASK_CANCEL` | 取消任务 |
| `WORKER_CONFIG_UPDATE` | 更新 Worker 配置 |
| `PING` | 心跳检测 |

`TASK_START` payload 示例：

```json
{
  "task": {
    "id": "task_001",
    "title": "实现任务功能",
    "description": "根据需求完成代码修改",
    "agentType": "codex",
    "baseBranch": "main",
    "targetBranch": "task/task_001"
  },
  "project": {
    "id": "project_001",
    "gitUrl": "git@github.com:example/block-play-table.git",
    "defaultBranch": "main",
    "worktreeNamePrefix": "block-play-table",
    "setupCommands": ["git status"]
  },
  "agentRuntimeEnv": [
    {
      "key": "OPENAI_API_KEY",
      "value": "<runtime-value>",
      "sensitive": true
    }
  ]
}
```

说明：`agentRuntimeEnv.value` 在 Manager 存储和 UI 展示时需要加密或脱敏；下发给 Worker 执行任务时必须是可用明文值，因此生产环境必须使用 `wss` 并限制 Worker Token 权限。

### 9.4 Worker 上报 Manager 的消息

| 消息类型 | 说明 |
| --- | --- |
| `WORKER_REGISTER` | Worker 注册 |
| `WORKER_HEARTBEAT` | Worker 心跳 |
| `TASK_ACCEPTED` | Worker 接收任务 |
| `TASK_STARTED` | 任务已启动 |
| `TASK_LOG` | 任务日志 |
| `TASK_CONVERSATION` | AI 对话 |
| `TASK_WAITING_INPUT` | 等待用户输入 |
| `TASK_INTERRUPTED` | 任务已中断 |
| `TASK_COMPLETED` | 任务已完成 |
| `TASK_FAILED` | 任务失败 |
| `TASK_RESULT` | 任务结果 |

## 10. 数据架构

### 10.1 存储策略

Manager 支持两种数据库：

1. SQLite：适合本地、单机、个人使用。
2. PostgreSQL：适合团队、服务端、生产环境。

通过 Repository 接口隔离数据库差异。

```text
Domain Repository Interface
        ↑
SQLite Implementation
PostgreSQL Implementation
```

### 10.2 核心数据表

#### tasks

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 任务 ID |
| `title` | string | 标题 |
| `description` | text | 描述 |
| `status` | string | 状态 |
| `project_id` | string | Project ID |
| `worker_id` | string | Worker ID |
| `agent_type` | string | Agent 类型 |
| `base_branch` | string | 基础分支 |
| `target_branch` | string | 目标分支 |
| `worktree_path` | string | Worktree 路径 |
| `result` | json/text | 结果 |
| `version` | integer | 聚合版本 |
| `created_at` | datetime | 创建时间 |
| `updated_at` | datetime | 更新时间 |

#### workers

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | Worker ID |
| `name` | string | 名称 |
| `status` | string | 状态 |
| `capabilities` | json | 能力 |
| `supported_agents` | json | 支持的 Agent |
| `work_dir` | string | 工作目录 |
| `startup_command` | string | 启动命令 |
| `project_binding_mode` | string | `ALL_PROJECTS` 或 `SPECIFIC_PROJECTS` |
| `current_task_id` | string | 当前任务 |
| `last_heartbeat_at` | datetime | 最近心跳 |
| `version` | integer | 聚合版本 |
| `created_at` | datetime | 创建时间 |
| `updated_at` | datetime | 更新时间 |

#### projects

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | Project ID |
| `name` | string | Project 名称 |
| `git_url` | string | Git 仓库地址 |
| `default_branch` | string | 默认分支 |
| `worktree_name_prefix` | string | Git Worktree 名称前缀 |
| `setup_commands` | json | 初始化命令 |
| `archived` | boolean | 是否归档 |
| `created_at` | datetime | 创建时间 |
| `updated_at` | datetime | 更新时间 |

#### worker_project_bindings

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `worker_id` | string | Worker ID |
| `project_id` | string | Project ID |
| `created_at` | datetime | 创建时间 |

说明：

1. 当 Worker 的 `project_binding_mode = ALL_PROJECTS` 时，不需要写入绑定记录。
2. 当 Worker 的 `project_binding_mode = SPECIFIC_PROJECTS` 时，只能执行绑定表中的 Project 任务。

#### system_agent_env_vars

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `key` | string | 环境变量名 |
| `value` | text | 环境变量值，敏感值建议加密存储 |
| `description` | text | 说明 |
| `enabled` | boolean | 是否启用 |
| `sensitive` | boolean | 是否敏感 |
| `created_at` | datetime | 创建时间 |
| `updated_at` | datetime | 更新时间 |

#### domain_events

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 事件 ID |
| `event_type` | string | 事件类型 |
| `aggregate_type` | string | 聚合类型 |
| `aggregate_id` | string | 聚合 ID |
| `aggregate_version` | integer | 聚合版本 |
| `payload` | json | 事件内容 |
| `occurred_at` | datetime | 发生时间 |
| `correlation_id` | string | 链路 ID |
| `causation_id` | string | 来源事件 ID |

#### task_logs

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 日志 ID |
| `task_id` | string | 任务 ID |
| `stream` | string | `stdout` / `stderr` / `system` |
| `content` | text | 日志内容 |
| `created_at` | datetime | 创建时间 |

#### task_conversations

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 消息 ID |
| `task_id` | string | 任务 ID |
| `role` | string | `user` / `assistant` / `system` / `tool` |
| `content` | text | 内容 |
| `metadata` | json | 元数据 |
| `created_at` | datetime | 创建时间 |

## 11. 部署架构

### 11.1 单机部署

适用于个人使用、本地开发和轻量任务执行。

```mermaid
flowchart TD
    UI[Flutter Desktop/Web] --> M[Manager]
    M --> DB[(SQLite)]
    M <-->|WebSocket| W[Local Worker]
    W --> A[Codex/Claude]
    W --> FS[Project Git Worktree]
```

特点：

1. Manager 与 Worker 可运行在同一台机器。
2. 使用 SQLite 存储。
3. 部署简单。
4. 适合本地 Agent 任务执行。
5. 适合开发和个人使用。

### 11.2 服务端部署

适用于团队共享使用。

```mermaid
flowchart TD
    U1[User Web/Desktop/App] --> LB[Reverse Proxy]
    U2[User Web/Desktop/App] --> LB
    LB --> M[Manager Service]
    M --> PG[(PostgreSQL)]
    M <-->|WebSocket| W1[Worker Node 1]
    M <-->|WebSocket| W2[Worker Node 2]
    M <-->|WebSocket| W3[Worker Node N]
    W1 --> A1[Codex/Claude]
    W2 --> A2[Codex/Claude]
    W3 --> A3[Codex/Claude]
```

特点：

1. Manager 独立部署。
2. 使用 PostgreSQL 存储。
3. Worker 可横向扩展。
4. 用户通过 HTTP / HTTPS 访问 Manager。
5. Worker 通过 WebSocket 长连接 Manager。
6. 通过反向代理统一 TLS、路由和访问入口。

### 11.3 推荐端口与路径

| 服务 | 默认端口 | 说明 |
| --- | ---: | --- |
| Manager HTTP / GraphQL | `8080` | GraphQL API |
| Manager WebSocket | `8080` | 可与 HTTP 共用端口 |
| PostgreSQL | `5432` | 生产数据库 |
| Flutter Web Dev | `3000` 或 `5173` | 开发环境 |

推荐路径：

| 路径 | 说明 |
| --- | --- |
| `/graphql` | GraphQL Query / Mutation |
| `/subscriptions` | GraphQL Subscription |
| `/worker/ws` | Worker WebSocket |
| `/healthz` | 健康检查 |
| `/readyz` | 就绪检查 |

### 11.4 Docker 多阶段构建策略

项目整体构建和编译统一使用 Docker 多阶段构建。开发机和 CI 不直接依赖本地 Go、Flutter 或 Node 工具链版本，构建产物由 Docker builder 阶段生成，运行镜像只保留必要二进制、静态资源和运行依赖。

构建目标：

1. Manager：Go builder 阶段编译 Manager 二进制，runtime 阶段使用精简基础镜像运行。
2. Worker：Go builder 阶段编译 Worker 二进制，runtime 阶段保留 Git、Shell、Agent CLI 和必要系统依赖。
3. Flutter Web：Flutter builder 阶段执行 `flutter build web`，runtime 阶段由 Nginx 或 Manager 静态文件服务承载。
4. 桌面端和移动端：也通过 Docker builder 固化 Flutter SDK 和依赖版本，产物输出到构建目录。

Manager Dockerfile 结构建议：

```dockerfile
ARG GO_VERSION=1
FROM golang:${GO_VERSION} AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/manager ./manager/cmd/manager

FROM gcr.io/distroless/static-debian12 AS runtime
COPY --from=builder /out/manager /manager
ENTRYPOINT ["/manager"]
```

Worker Dockerfile 结构建议：

```dockerfile
ARG GO_VERSION=1
FROM golang:${GO_VERSION} AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/worker ./worker/cmd/worker

FROM debian:12-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates bash \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /out/worker /worker
ENTRYPOINT ["/worker"]
```

Flutter Web Dockerfile 结构建议：

```dockerfile
FROM ghcr.io/cirruslabs/flutter:stable AS builder
WORKDIR /src
COPY ui/pubspec.* ./
RUN flutter pub get
COPY ui/ .
RUN flutter build web --release

FROM nginx:1.27-alpine AS runtime
COPY --from=builder /src/build/web /usr/share/nginx/html
```

### 11.5 Docker Compose 示例

```yaml
services:
  manager:
    image: block-play-table-manager
    ports:
      - "8080:8080"
    environment:
      DB_DRIVER: postgres
      DB_DSN: postgres://manager:password@postgres:5432/block_play_table?sslmode=disable
    depends_on:
      - postgres

  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: manager
      POSTGRES_PASSWORD: password
      POSTGRES_DB: block_play_table
    volumes:
      - postgres_data:/var/lib/postgresql/data

  worker:
    image: block-play-table-worker
    environment:
      MANAGER_WS_URL: ws://manager:8080/worker/ws
      WORKER_ID: worker-001
      WORKER_WORK_DIR: /worker-data
    volumes:
      - ./worker-data:/worker-data

volumes:
  postgres_data:
```

## 12. 可靠性设计

### 12.1 心跳机制

Worker 定期向 Manager 上报心跳。

推荐心跳间隔：

```text
10s - 30s
```

Manager 判断 Worker 离线：

```text
last_heartbeat_at 超过 3 个心跳周期
```

处理策略：

1. Worker 心跳超时后标记为 `OFFLINE`。
2. 如果 Worker 正在执行任务，任务标记为 `WORKER_LOST` 或保留为异常状态。
3. 用户可选择重新分配任务、重试任务或人工确认任务结果。

### 12.2 事件可靠性

推荐使用 Outbox Pattern 保证状态变更和事件发布的一致性。

同一事务中写入：

1. 聚合状态。
2. 领域事件。
3. Outbox 事件。

事务提交后异步发布：

1. GraphQL Subscription。
2. Worker WebSocket 消息。
3. 内部异步处理器。

### 12.3 Worker 重连

Worker 断线后应自动重连。

重连成功后上报：

1. Worker ID。
2. 当前任务 ID。
3. 当前任务状态。
4. 最近执行进度。
5. 本地进程是否仍存在。

Manager 根据上报信息恢复状态。

### 12.4 幂等设计

以下操作必须幂等：

1. Worker 注册。
2. Worker 心跳。
3. Task Start 下发。
4. Task Log 上报。
5. Task Completed 上报。
6. Task Failed 上报。
7. Task Interrupted 上报。

每条 WebSocket 消息应包含 `messageId`，Manager 记录已处理消息，避免重复消费。

## 13. 安全设计

### 13.1 认证

推荐支持两种模式：

1. 本地模式：允许关闭认证或使用本地 Token。
2. 团队模式：启用用户登录和 Worker Token。

Worker 连接示例：

```text
ws://manager:8080/worker/ws?worker_id=worker-001&token=xxx
```

生产环境必须使用：

```text
wss://manager.example.com/worker/ws
```

### 13.2 权限

推荐权限级别：

| 权限级别 | 权限 |
| --- | --- |
| Admin | 系统设置、Worker 管理、任务管理 |
| Developer | 创建任务、启动任务、查看任务 |
| Viewer | 只读查看任务、日志和看板 |

### 13.3 命令执行安全

Worker 会执行本地命令，因此需要控制风险：

1. Worker 只执行 Manager 下发且经过权限校验的任务。
2. Worker 使用独立工作目录。
3. 敏感环境变量不直接展示给 UI。
4. 命令执行日志需要脱敏。
5. 生产环境建议每个 Worker 使用受限系统用户。
6. 可选使用容器隔离每个任务。

## 14. 配置设计

### 14.1 Manager 配置

SQLite 示例：

```yaml
server:
  http_addr: ":8080"

database:
  driver: "sqlite"
  dsn: "./data/manager.db"

worker:
  heartbeat_timeout: "90s"

event:
  outbox_enabled: true

agent_runtime_env:
  - key: "OPENAI_API_KEY"
    value: "${OPENAI_API_KEY}"
    enabled: true
    sensitive: true
    description: "Codex/OpenAI API key"
  - key: "ANTHROPIC_API_KEY"
    value: "${ANTHROPIC_API_KEY}"
    enabled: true
    sensitive: true
    description: "Claude API key"
```

PostgreSQL 示例：

```yaml
database:
  driver: "postgres"
  dsn: "postgres://manager:password@localhost:5432/block_play_table?sslmode=disable"
```

### 14.2 Project 配置

```yaml
project:
  id: "project-001"
  name: "block-play-table"
  git_url: "git@github.com:example/block-play-table.git"
  default_branch: "main"
  worktree_name_prefix: "block-play-table"
  setup_commands:
    - "git status"
```

### 14.3 Worker 配置

```yaml
worker:
  id: "worker-001"
  name: "local-worker"
  manager_ws_url: "ws://localhost:8080/worker/ws"
  token: "worker-token"
  work_dir: "/Users/example/block-play-table-worker"
  project_binding_mode: "SPECIFIC_PROJECTS"
  bound_project_ids:
    - "project-001"
  supported_agents:
    - "codex"
    - "claude"

commands:
  default_setup:
    - "git status"
```

## 15. 推荐开发里程碑

### 15.1 第一阶段：最小可用版本

目标是完成单机闭环。

1. Manager 启动。
2. SQLite 存储。
3. GraphQL 创建 Project。
4. GraphQL 创建任务。
5. Worker 连接 Manager。
6. Worker 根据 Project Git URL 创建 worktree。
7. Manager 下发任务。
8. Worker 执行简单命令。
9. Worker 上报日志。
10. UI 展示任务状态和日志。

### 15.2 第二阶段：Agent 执行能力

1. 支持 Codex。
2. 支持 Claude。
3. 支持 Git Worktree。
4. 支持任务中断。
5. 支持系统设置中的 Agent 运行时环境变量注入。
6. 支持 AI 对话展示。
7. 支持任务结果上报。

### 15.3 第三阶段：DDD 和领域事件完善

1. 完成 Task 聚合。
2. 完成 Worker 聚合。
3. 完成 Project 聚合。
4. 完成 Settings 聚合。
5. 完成领域事件表。
6. 完成 Outbox。
7. GraphQL Subscription 基于领域事件推送。
8. 事件审计页面。

### 15.4 第四阶段：团队部署能力

1. PostgreSQL 支持。
2. 多 Worker 支持。
3. Worker 自动重连。
4. 用户权限系统。
5. Docker 多阶段构建。
6. Docker Compose。
7. 生产配置。

## 16. 关键设计原则

1. Manager 是业务核心，Worker 是执行节点。
2. Domain 层保持纯粹，不依赖基础设施和外部协议。
3. 所有关键业务行为都应产生领域事件。
4. GraphQL 面向用户界面，WebSocket 面向 Worker。
5. Worker 主动连接 Manager。
6. SQLite 面向本地模式，PostgreSQL 面向生产模式。
7. Agent 执行通过统一接口抽象，便于扩展 Codex、Claude 或其他 Agent。
8. Project 是 Git 仓库配置的唯一业务来源，Task 不重复保存 Git URL。
9. Workspace 不作为独立业务模块，Worker 本地运行目录和任务 worktree 属于执行期资源。
10. 任务日志、AI 对话、状态变化都应实时上报并持久化。
11. 任务启动、中断、完成和失败都必须幂等。
12. 命令执行必须考虑安全隔离、权限控制和日志脱敏。
13. 构建和编译统一通过 Docker 多阶段构建完成。
