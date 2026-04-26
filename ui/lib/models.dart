import 'dart:convert';

class BoardData {
  BoardData({
    required this.id,
    required this.name,
    required this.type,
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
  }) => BoardData(
    id: json['id'] as String? ?? 'default',
    name: json['name'] as String? ?? 'Default Board',
    type: json['type'] as String? ?? 'KANBAN',
    columns: (json['columns'] as List<dynamic>? ?? [])
        .map((item) => BoardColumnData.fromJson(item as Map<String, dynamic>))
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
    columns: const [],
    calendarItems: const [],
    tasks: const [],
    projects: const [],
    workers: const [],
  );

  final String id;
  final String name;
  final String type;
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
    required this.baseBranch,
    required this.preCommands,
    required this.postCommands,
    required this.createdAt,
    required this.updatedAt,
    this.workerId,
    this.worktreePath,
    this.result,
  });

  factory TaskItem.fromJson(Map<String, dynamic> json) => TaskItem(
    id: json['id'] as String?,
    title: json['title'] as String? ?? '',
    description: json['description'] as String? ?? '',
    status: json['status'] as String? ?? '',
    projectId: json['projectId'] as String? ?? '',
    agentType: json['agentType'] as String? ?? '',
    baseBranch: json['baseBranch'] as String? ?? 'main',
    preCommands: stringList(json['preCommands']),
    postCommands: stringList(json['postCommands']),
    createdAt: json['createdAt'] as String? ?? '',
    updatedAt: json['updatedAt'] as String? ?? '',
    workerId: json['workerId'] as String?,
    worktreePath: json['worktreePath'] as String?,
    result: json['result'] as String?,
  );

  final String? id;
  final String title;
  final String description;
  final String status;
  final String projectId;
  final String agentType;
  final String baseBranch;
  final List<String> preCommands;
  final List<String> postCommands;
  final String createdAt;
  final String updatedAt;
  final String? workerId;
  final String? worktreePath;
  final String? result;
}

class TaskDetailData {
  TaskDetailData({
    required this.task,
    required this.logs,
    required this.conversations,
    required this.events,
  });

  final TaskItem task;
  final List<TaskLogItem> logs;
  final List<ConversationItem> conversations;
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
  const ConversationItem({required this.role, required this.content});

  factory ConversationItem.fromJson(Map<String, dynamic> json) =>
      ConversationItem(
        role: json['role'] as String? ?? 'assistant',
        content: json['content'] as String? ?? '',
      );

  final String role;
  final String content;
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
    this.lastHeartbeatAt,
    this.currentTaskId,
  });

  factory WorkerItem.fromJson(Map<String, dynamic> json) => WorkerItem(
    id: json['id'] as String? ?? '',
    name: json['name'] as String? ?? '',
    status: json['status'] as String? ?? '',
    supportedAgents: stringList(json['supportedAgents']),
    workDir: json['workDir'] as String? ?? '',
    startupCommand: json['startupCommand'] as String? ?? '',
    projectBindingMode: json['projectBindingMode'] as String? ?? 'ALL_PROJECTS',
    boundProjectIds: stringList(json['boundProjectIds']),
    lastHeartbeatAt: json['lastHeartbeatAt'] as String?,
    currentTaskId: json['currentTaskId'] as String?,
  );

  final String id;
  final String name;
  final String status;
  final List<String> supportedAgents;
  final String workDir;
  final String startupCommand;
  final String projectBindingMode;
  final List<String> boundProjectIds;
  final String? lastHeartbeatAt;
  final String? currentTaskId;
}

class SettingsData {
  const SettingsData({
    required this.agentRuntimeEnvVars,
    this.workerHeartbeatTimeout = '90s',
    this.securityPolicy = 'TRUSTED',
  });

  factory SettingsData.fromJson(Map<String, dynamic> json) => SettingsData(
    agentRuntimeEnvVars: (json['agentRuntimeEnvVars'] as List<dynamic>? ?? [])
        .map((item) => EnvVarItem.fromJson(item as Map<String, dynamic>))
        .toList(),
    workerHeartbeatTimeout: json['workerHeartbeatTimeout'] as String? ?? '90s',
    securityPolicy: json['securityPolicy'] as String? ?? 'TRUSTED',
  );

  final List<EnvVarItem> agentRuntimeEnvVars;
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

  factory EnvVarItem.fromJson(Map<String, dynamic> json) => EnvVarItem(
    key: json['key'] as String? ?? '',
    valueMasked: json['valueMasked'] as String? ?? '',
    description: json['description'] as String? ?? '',
    enabled: json['enabled'] as bool? ?? false,
    sensitive: json['sensitive'] as bool? ?? false,
  );

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
