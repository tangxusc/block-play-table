import 'dart:convert';

class PageRequest {
  const PageRequest({this.offset = 0, this.limit = defaultLimit});

  static const defaultLimit = 20;

  final int offset;
  final int limit;

  PageRequest copyWith({int? offset, int? limit}) => PageRequest(
        offset: offset ?? this.offset,
        limit: limit ?? this.limit,
      );

  PageRequest first() => copyWith(offset: 0);

  PageRequest withOffset(int value) => copyWith(offset: value < 0 ? 0 : value);

  int lastOffset(int totalCount) {
    if (totalCount <= 0) {
      return 0;
    }
    return ((totalCount - 1) ~/ limit) * limit;
  }

  List<T> slice<T>(List<T> items) {
    final start = offset < 0 ? 0 : offset;
    if (start >= items.length) {
      return const [];
    }
    final end = start + limit < items.length ? start + limit : items.length;
    return items.sublist(start, end);
  }

  Map<String, dynamic> toGraphQLInput() => {
        'offset': offset,
        'limit': limit,
      };
}

class PagedResult<T> {
  const PagedResult({required this.items, required this.totalCount});

  final List<T> items;
  final int totalCount;
}

class BoardData {
  BoardData({
    required this.id,
    required this.name,
    required this.type,
    required this.totalCount,
    required this.columns,
    required this.calendarItems,
    required this.tasks,
    required this.projects,
    required this.workers,
  });

  factory BoardData.fromJson(
    Map<String, dynamic> json, {
    required List<ProjectItem> projects,
    required List<WorkerItem> workers,
  }) =>
      BoardData(
        id: json['id'] as String? ?? 'default',
        name: json['name'] as String? ?? 'Default Board',
        type: json['type'] as String? ?? 'KANBAN',
        totalCount: json['totalCount'] as int? ?? 0,
        columns: (json['columns'] as List<dynamic>? ?? [])
            .map((item) =>
                BoardColumnData.fromJson(item as Map<String, dynamic>))
            .toList(),
        calendarItems: (json['calendarItems'] as List<dynamic>? ?? [])
            .map(
              (item) =>
                  BoardCalendarItemData.fromJson(item as Map<String, dynamic>),
            )
            .toList(),
        tasks: (json['tasks'] as List<dynamic>? ?? [])
            .map((item) => TaskItem.fromJson(item as Map<String, dynamic>))
            .toList(),
        projects: projects,
        workers: workers,
      );

  factory BoardData.empty() => BoardData(
        id: 'default',
        name: 'Default Board',
        type: 'KANBAN',
        totalCount: 0,
        columns: const [],
        calendarItems: const [],
        tasks: const [],
        projects: const [],
        workers: const [],
      );

  final String id;
  final String name;
  final String type;
  final int totalCount;
  final List<BoardColumnData> columns;
  final List<BoardCalendarItemData> calendarItems;
  final List<TaskItem> tasks;
  final List<ProjectItem> projects;
  final List<WorkerItem> workers;
}

class BoardColumnData {
  BoardColumnData({
    required this.id,
    required this.title,
    required this.status,
    required this.tasks,
  });

  factory BoardColumnData.fromJson(Map<String, dynamic> json) =>
      BoardColumnData(
        id: json['id'] as String? ?? '',
        title: json['title'] as String? ?? '',
        status: json['status'] as String? ?? '',
        tasks: (json['tasks'] as List<dynamic>? ?? [])
            .map((item) => TaskItem.fromJson(item as Map<String, dynamic>))
            .toList(),
      );

  final String id;
  final String title;
  final String status;
  final List<TaskItem> tasks;
}

class BoardCalendarItemData {
  BoardCalendarItemData({
    required this.id,
    required this.task,
    required this.date,
    required this.status,
  });

  factory BoardCalendarItemData.fromJson(Map<String, dynamic> json) =>
      BoardCalendarItemData(
        id: json['id'] as String? ?? '',
        task: TaskItem.fromJson(
          json['task'] as Map<String, dynamic>? ?? const {},
        ),
        date: json['date'] as String? ?? '',
        status: json['status'] as String? ?? '',
      );

  final String id;
  final TaskItem task;
  final String date;
  final String status;
}

class TaskItem {
  TaskItem({
    required this.id,
    required this.title,
    required this.description,
    required this.status,
    required this.projectId,
    required this.agentType,
    required this.agentConfig,
    required this.baseBranch,
    required this.preCommands,
    required this.postCommands,
    required this.startDate,
    required this.endDate,
    required this.createdAt,
    required this.updatedAt,
    this.workerId,
    this.worktreePath,
    this.agentSessionId,
    this.result,
  });

  factory TaskItem.fromJson(Map<String, dynamic> json) => TaskItem(
        id: json['id'] as String?,
        title: json['title'] as String? ?? '',
        description: json['description'] as String? ?? '',
        status: json['status'] as String? ?? '',
        projectId: json['projectId'] as String? ?? '',
        agentType: json['agentType'] as String? ?? '',
        agentConfig: AgentExecutionConfigItem.fromJson(
          json['agentConfig'] as Map<String, dynamic>? ?? const {},
        ),
        baseBranch: json['baseBranch'] as String? ?? 'main',
        preCommands: stringList(json['preCommands']),
        postCommands: stringList(json['postCommands']),
        startDate: (json['startDate'] as String?) ??
            (json['createdAt'] as String?) ??
            '',
        endDate: (json['endDate'] as String?) ??
            (json['startDate'] as String?) ??
            (json['createdAt'] as String?) ??
            '',
        createdAt: json['createdAt'] as String? ?? '',
        updatedAt: json['updatedAt'] as String? ?? '',
        workerId: json['workerId'] as String?,
        worktreePath: json['worktreePath'] as String?,
        agentSessionId: json['agentSessionId'] as String?,
        result: json['result'] as String?,
      );

  final String? id;
  final String title;
  final String description;
  final String status;
  final String projectId;
  final String agentType;
  final AgentExecutionConfigItem agentConfig;
  final String baseBranch;
  final List<String> preCommands;
  final List<String> postCommands;
  final String startDate;
  final String endDate;
  final String createdAt;
  final String updatedAt;
  final String? workerId;
  final String? worktreePath;
  final String? agentSessionId;
  final String? result;
}

class AgentExecutionConfigItem {
  const AgentExecutionConfigItem({
    this.workMode = '',
    this.codex = const CodexExecutionConfigItem(),
    this.claude = const ClaudeExecutionConfigItem(),
  });

  factory AgentExecutionConfigItem.fromJson(Map<String, dynamic> json) =>
      AgentExecutionConfigItem(
        workMode: json['workMode'] as String? ?? '',
        codex: CodexExecutionConfigItem.fromJson(
          json['codex'] as Map<String, dynamic>? ?? const {},
        ),
        claude: ClaudeExecutionConfigItem.fromJson(
          json['claude'] as Map<String, dynamic>? ?? const {},
        ),
      );

  final String workMode;
  final CodexExecutionConfigItem codex;
  final ClaudeExecutionConfigItem claude;

  bool get isEmpty => workMode.isEmpty && codex.isEmpty && claude.isEmpty;

  Map<String, dynamic> toGraphQLInput(String agentType) {
    final input = <String, dynamic>{};
    if (workMode.isNotEmpty) {
      input['workMode'] = workMode;
    }
    if (agentType == 'codex') {
      input['codex'] = codex.toGraphQLInput();
    } else if (agentType == 'claude') {
      input['claude'] = claude.toGraphQLInput();
    }
    return input;
  }
}

class CodexExecutionConfigItem {
  const CodexExecutionConfigItem({
    this.model = '',
    this.reasoningEffort = '',
    this.sandboxMode = '',
    this.approvalPolicy = '',
    this.fullAuto = false,
    this.bypassApprovalsAndSandbox = false,
  });

  factory CodexExecutionConfigItem.fromJson(Map<String, dynamic> json) =>
      CodexExecutionConfigItem(
        model: json['model'] as String? ?? '',
        reasoningEffort: json['reasoningEffort'] as String? ?? '',
        sandboxMode: json['sandboxMode'] as String? ?? '',
        approvalPolicy: json['approvalPolicy'] as String? ?? '',
        fullAuto: json['fullAuto'] as bool? ?? false,
        bypassApprovalsAndSandbox:
            json['bypassApprovalsAndSandbox'] as bool? ?? false,
      );

  final String model;
  final String reasoningEffort;
  final String sandboxMode;
  final String approvalPolicy;
  final bool fullAuto;
  final bool bypassApprovalsAndSandbox;

  bool get isEmpty =>
      model.isEmpty &&
      reasoningEffort.isEmpty &&
      sandboxMode.isEmpty &&
      approvalPolicy.isEmpty &&
      !fullAuto &&
      !bypassApprovalsAndSandbox;

  Map<String, dynamic> toGraphQLInput() => {
        if (model.trim().isNotEmpty) 'model': model.trim(),
        if (reasoningEffort.isNotEmpty) 'reasoningEffort': reasoningEffort,
        if (sandboxMode.isNotEmpty) 'sandboxMode': sandboxMode,
        if (approvalPolicy.isNotEmpty) 'approvalPolicy': approvalPolicy,
        'fullAuto': fullAuto,
        'bypassApprovalsAndSandbox': bypassApprovalsAndSandbox,
      };
}

class ClaudeExecutionConfigItem {
  const ClaudeExecutionConfigItem({
    this.model = '',
    this.effort = '',
    this.permissionMode = '',
  });

  factory ClaudeExecutionConfigItem.fromJson(Map<String, dynamic> json) =>
      ClaudeExecutionConfigItem(
        model: json['model'] as String? ?? '',
        effort: json['effort'] as String? ?? '',
        permissionMode: json['permissionMode'] as String? ?? '',
      );

  final String model;
  final String effort;
  final String permissionMode;

  bool get isEmpty => model.isEmpty && effort.isEmpty && permissionMode.isEmpty;

  Map<String, dynamic> toGraphQLInput() => {
        if (model.trim().isNotEmpty) 'model': model.trim(),
        if (effort.isNotEmpty) 'effort': effort,
        if (permissionMode.isNotEmpty) 'permissionMode': permissionMode,
      };
}

class TaskDetailData {
  TaskDetailData({
    required this.task,
    required this.logs,
    required this.conversations,
    required this.interactions,
    required this.events,
  });

  final TaskItem task;
  final List<TaskLogItem> logs;
  final List<ConversationItem> conversations;
  final List<TaskInteractionItem> interactions;
  final List<DomainEventItem> events;
}

class TaskLogItem {
  const TaskLogItem({required this.stream, required this.content});

  factory TaskLogItem.fromJson(Map<String, dynamic> json) => TaskLogItem(
        stream: json['stream'] as String? ?? '',
        content: json['content'] as String? ?? '',
      );

  final String stream;
  final String content;
}

class ConversationItem {
  const ConversationItem({
    required this.role,
    required this.content,
    required this.metadata,
  });

  factory ConversationItem.fromJson(Map<String, dynamic> json) =>
      ConversationItem(
        role: json['role'] as String? ?? 'assistant',
        content: json['content'] as String? ?? '',
        metadata: _keyValuesToMap(json['metadata'] as List<dynamic>? ?? []),
      );

  final String role;
  final String content;
  final Map<String, String> metadata;
}

class TaskInteractionItem {
  const TaskInteractionItem({
    required this.id,
    required this.taskId,
    required this.kind,
    required this.status,
    required this.title,
    required this.body,
    required this.rawPayload,
    required this.createdAt,
    required this.updatedAt,
    this.agentSessionId,
    this.responseDecision,
    this.responseMessage,
    this.responsePayload,
  });

  factory TaskInteractionItem.fromJson(Map<String, dynamic> json) =>
      TaskInteractionItem(
        id: json['id'] as String? ?? '',
        taskId: json['taskId'] as String? ?? '',
        kind: json['kind'] as String? ?? '',
        status: json['status'] as String? ?? '',
        title: json['title'] as String? ?? '',
        body: json['body'] as String? ?? '',
        rawPayload: json['rawPayload'] as String? ?? '',
        agentSessionId: json['agentSessionId'] as String?,
        responseDecision: json['responseDecision'] as String?,
        responseMessage: json['responseMessage'] as String?,
        responsePayload: json['responsePayload'] as String?,
        createdAt: json['createdAt'] as String? ?? '',
        updatedAt: json['updatedAt'] as String? ?? '',
      );

  final String id;
  final String taskId;
  final String kind;
  final String status;
  final String title;
  final String body;
  final String rawPayload;
  final String? agentSessionId;
  final String? responseDecision;
  final String? responseMessage;
  final String? responsePayload;
  final String createdAt;
  final String updatedAt;

  Map<String, dynamic> get rawJson {
    if (rawPayload.trim().isEmpty) {
      return const {};
    }
    try {
      final decoded = jsonDecode(rawPayload);
      if (decoded is Map<String, dynamic>) {
        return decoded;
      }
    } catch (_) {
      return const {};
    }
    return const {};
  }
}

Map<String, String> _keyValuesToMap(List<dynamic> items) {
  final values = <String, String>{};
  for (final item in items) {
    if (item is! Map<String, dynamic>) {
      continue;
    }
    final key = item['key'] as String? ?? '';
    if (key.isEmpty) {
      continue;
    }
    values[key] = item['value'] as String? ?? '';
  }
  return values;
}

class DomainEventItem {
  const DomainEventItem({
    required this.eventId,
    required this.eventType,
    required this.aggregateType,
    required this.aggregateId,
    required this.aggregateVersion,
    required this.payload,
    required this.occurredAt,
  });

  factory DomainEventItem.fromJson(Map<String, dynamic> json) {
    final payload = json['payload'];
    return DomainEventItem(
      eventId: json['eventId'] as String? ?? '',
      eventType: json['eventType'] as String? ?? '',
      aggregateType: json['aggregateType'] as String? ?? '',
      aggregateId: json['aggregateId'] as String? ?? '',
      aggregateVersion: json['aggregateVersion'] as int? ?? 0,
      payload: payload == null
          ? 'null'
          : payload is String
              ? payload
              : jsonEncode(payload),
      occurredAt: json['occurredAt'] as String? ?? '',
    );
  }

  final String eventId;
  final String eventType;
  final String aggregateType;
  final String aggregateId;
  final int aggregateVersion;
  final String payload;
  final String occurredAt;
}

class ProjectItem {
  const ProjectItem({
    required this.id,
    required this.name,
    required this.gitUrl,
    required this.defaultBranch,
    required this.worktreeNamePrefix,
    required this.archived,
  });

  factory ProjectItem.fromJson(Map<String, dynamic> json) => ProjectItem(
        id: json['id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        gitUrl: json['gitUrl'] as String? ?? '',
        defaultBranch: json['defaultBranch'] as String? ?? 'main',
        worktreeNamePrefix: json['worktreeNamePrefix'] as String? ?? '',
        archived: json['archived'] as bool? ?? false,
      );

  final String id;
  final String name;
  final String gitUrl;
  final String defaultBranch;
  final String worktreeNamePrefix;
  final bool archived;
}

class WorkerItem {
  const WorkerItem({
    required this.id,
    required this.name,
    required this.status,
    required this.supportedAgents,
    required this.workDir,
    required this.startupCommand,
    required this.projectBindingMode,
    required this.boundProjectIds,
    required this.currentTaskIds,
    this.agentRuntimeEnv = const [],
    this.lastHeartbeatAt,
  });

  factory WorkerItem.fromJson(Map<String, dynamic> json) => WorkerItem(
        id: json['id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        status: json['status'] as String? ?? '',
        supportedAgents: stringList(json['supportedAgents']),
        workDir: json['workDir'] as String? ?? '',
        startupCommand: json['startupCommand'] as String? ?? '',
        projectBindingMode:
            json['projectBindingMode'] as String? ?? 'ALL_PROJECTS',
        boundProjectIds: stringList(json['boundProjectIds']),
        currentTaskIds: stringList(json['currentTaskIds']),
        agentRuntimeEnv: (json['agentRuntimeEnv'] as List<dynamic>? ?? [])
            .map(
              (item) => WorkerAgentRuntimeEnvItem.fromJson(
                  item as Map<String, dynamic>),
            )
            .toList(),
        lastHeartbeatAt: json['lastHeartbeatAt'] as String?,
      );

  final String id;
  final String name;
  final String status;
  final List<String> supportedAgents;
  final String workDir;
  final String startupCommand;
  final String projectBindingMode;
  final List<String> boundProjectIds;
  final List<String> currentTaskIds;
  final List<WorkerAgentRuntimeEnvItem> agentRuntimeEnv;
  final String? lastHeartbeatAt;

  WorkerItem copyWith({
    String? id,
    String? name,
    String? status,
    List<String>? supportedAgents,
    String? workDir,
    String? startupCommand,
    String? projectBindingMode,
    List<String>? boundProjectIds,
    List<String>? currentTaskIds,
    List<WorkerAgentRuntimeEnvItem>? agentRuntimeEnv,
    String? lastHeartbeatAt,
  }) =>
      WorkerItem(
        id: id ?? this.id,
        name: name ?? this.name,
        status: status ?? this.status,
        supportedAgents: supportedAgents ?? this.supportedAgents,
        workDir: workDir ?? this.workDir,
        startupCommand: startupCommand ?? this.startupCommand,
        projectBindingMode: projectBindingMode ?? this.projectBindingMode,
        boundProjectIds: boundProjectIds ?? this.boundProjectIds,
        currentTaskIds: currentTaskIds ?? this.currentTaskIds,
        agentRuntimeEnv: agentRuntimeEnv ?? this.agentRuntimeEnv,
        lastHeartbeatAt: lastHeartbeatAt ?? this.lastHeartbeatAt,
      );
}

class WorkerAgentRuntimeEnvItem {
  const WorkerAgentRuntimeEnvItem({
    required this.agentType,
    required this.vars,
  });

  factory WorkerAgentRuntimeEnvItem.fromJson(Map<String, dynamic> json) =>
      WorkerAgentRuntimeEnvItem(
        agentType: json['agentType'] as String? ?? '',
        vars: (json['vars'] as List<dynamic>? ?? [])
            .map((item) => EnvVarItem.fromJson(item as Map<String, dynamic>))
            .toList(),
      );

  final String agentType;
  final List<EnvVarItem> vars;
}

class SettingsData {
  const SettingsData({
    this.workerHeartbeatTimeout = '90s',
    this.securityPolicy = 'TRUSTED',
  });

  factory SettingsData.fromJson(Map<String, dynamic> json) => SettingsData(
        workerHeartbeatTimeout:
            json['workerHeartbeatTimeout'] as String? ?? '90s',
        securityPolicy: json['securityPolicy'] as String? ?? 'TRUSTED',
      );

  final String workerHeartbeatTimeout;
  final String securityPolicy;
}

class EnvVarItem {
  const EnvVarItem({
    required this.key,
    required this.valueMasked,
    required this.description,
    required this.enabled,
    required this.sensitive,
    this.valueInput = '',
  });

  factory EnvVarItem.fromJson(Map<String, dynamic> json) {
    final sensitive = json['sensitive'] as bool? ?? false;
    final valueMasked = json['valueMasked'] as String? ?? '';
    return EnvVarItem(
      key: json['key'] as String? ?? '',
      valueMasked: valueMasked,
      description: json['description'] as String? ?? '',
      enabled: json['enabled'] as bool? ?? false,
      sensitive: sensitive,
      valueInput: sensitive ? '' : valueMasked,
    );
  }

  final String key;
  final String valueMasked;
  final String description;
  final bool enabled;
  final bool sensitive;
  final String valueInput;
}

List<String> stringList(dynamic value) {
  if (value is List) {
    return value.map((item) => item.toString()).toList();
  }
  if (value is String && value.trim().isNotEmpty) {
    return value
        .split('\n')
        .map((item) => item.trim())
        .where((item) => item.isNotEmpty)
        .toList();
  }
  return const [];
}
