import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:http/http.dart' as http;

void main() {
  runApp(BlockPlayTableApp(apiClient: ApiClient.fromEnvironment()));
}

class BlockPlayTableApp extends StatelessWidget {
  const BlockPlayTableApp({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Block Play Table',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xff256f6b)),
        useMaterial3: true,
        visualDensity: VisualDensity.compact,
      ),
      home: HomePage(apiClient: apiClient),
    );
  }
}

class HomePage extends StatefulWidget {
  const HomePage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<HomePage> createState() => _HomePageState();
}

class _HomePageState extends State<HomePage> {
  late Future<DashboardData> _dashboard;
  int _selectedIndex = 0;

  @override
  void initState() {
    super.initState();
    _dashboard = widget.apiClient.dashboard();
  }

  void _refresh() {
    setState(() {
      _dashboard = widget.apiClient.dashboard();
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Block Play Table'),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            onPressed: _refresh,
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: Row(
        children: [
          NavigationRail(
            selectedIndex: _selectedIndex,
            onDestinationSelected: (index) =>
                setState(() => _selectedIndex = index),
            labelType: NavigationRailLabelType.all,
            destinations: const [
              NavigationRailDestination(
                icon: Icon(Icons.table_rows),
                label: Text('Tasks'),
              ),
              NavigationRailDestination(
                icon: Icon(Icons.view_kanban),
                label: Text('Board'),
              ),
              NavigationRailDestination(
                icon: Icon(Icons.folder_copy),
                label: Text('Projects'),
              ),
              NavigationRailDestination(
                icon: Icon(Icons.memory),
                label: Text('Workers'),
              ),
              NavigationRailDestination(
                icon: Icon(Icons.event_note),
                label: Text('Events'),
              ),
              NavigationRailDestination(
                icon: Icon(Icons.settings),
                label: Text('Settings'),
              ),
            ],
          ),
          const VerticalDivider(width: 1),
          Expanded(
            child: FutureBuilder<DashboardData>(
              future: _dashboard,
              builder: (context, snapshot) {
                if (snapshot.connectionState != ConnectionState.done) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snapshot.hasError) {
                  return ErrorView(
                    message: snapshot.error.toString(),
                    onRetry: _refresh,
                  );
                }
                final data = snapshot.data ?? DashboardData.empty();
                return IndexedStack(
                  index: _selectedIndex,
                  children: [
                    TasksPage(
                      apiClient: widget.apiClient,
                      data: data,
                      onChanged: _refresh,
                    ),
                    BoardPage(tasks: data.tasks),
                    ProjectsPage(
                      apiClient: widget.apiClient,
                      data: data,
                      onChanged: _refresh,
                    ),
                    WorkersPage(workers: data.workers),
                    EventAuditPage(events: data.events),
                    SettingsPage(settings: data.settings),
                  ],
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

class TasksPage extends StatefulWidget {
  const TasksPage({
    super.key,
    required this.apiClient,
    required this.data,
    required this.onChanged,
  });

  final ApiClient apiClient;
  final DashboardData data;
  final VoidCallback onChanged;

  @override
  State<TasksPage> createState() => _TasksPageState();
}

class _TasksPageState extends State<TasksPage> {
  final _title = TextEditingController();
  String? _projectId;
  String _agent = 'codex';
  String? _selectedTaskId;
  Future<TaskDetailData>? _detail;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Wrap(
          spacing: 12,
          runSpacing: 12,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            SizedBox(
              width: 280,
              child: TextField(
                controller: _title,
                decoration: const InputDecoration(
                  labelText: 'Task title',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            SizedBox(
              width: 260,
              child: DropdownButtonFormField<String>(
                value: _projectId,
                decoration: const InputDecoration(
                  labelText: 'Project',
                  border: OutlineInputBorder(),
                ),
                items: widget.data.projects
                    .map(
                      (project) => DropdownMenuItem(
                        value: project.id,
                        child: Text(project.name),
                      ),
                    )
                    .toList(),
                onChanged: (value) => setState(() => _projectId = value),
              ),
            ),
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(
                  value: 'codex',
                  label: Text('Codex'),
                  icon: Icon(Icons.terminal),
                ),
                ButtonSegment(
                  value: 'claude',
                  label: Text('Claude'),
                  icon: Icon(Icons.chat),
                ),
              ],
              selected: {_agent},
              onSelectionChanged: (values) =>
                  setState(() => _agent = values.first),
            ),
            FilledButton.icon(
              onPressed: _createTask,
              icon: const Icon(Icons.add),
              label: const Text('Create'),
            ),
          ],
        ),
        const SizedBox(height: 16),
        DataTable(
          columns: const [
            DataColumn(label: Text('Title')),
            DataColumn(label: Text('Status')),
            DataColumn(label: Text('Agent')),
            DataColumn(label: Text('Worker')),
            DataColumn(label: Text('Actions')),
          ],
          rows: widget.data.tasks.map((task) {
            return DataRow(
              selected: task.id == _selectedTaskId,
              onSelectChanged: task.id == null
                  ? null
                  : (_) => _selectTask(task.id!),
              cells: [
                DataCell(Text(task.title)),
                DataCell(StatusPill(value: task.status)),
                DataCell(Text(task.agentType)),
                DataCell(Text(task.workerId ?? '')),
                DataCell(
                  Wrap(
                    spacing: 8,
                    children: [
                      IconButton(
                        tooltip: 'Details',
                        onPressed: task.id == null
                            ? null
                            : () => _selectTask(task.id!),
                        icon: const Icon(Icons.subject),
                      ),
                      IconButton(
                        tooltip: 'Start',
                        onPressed: task.id == null
                            ? null
                            : () => _startTask(task.id!),
                        icon: const Icon(Icons.play_arrow),
                      ),
                      IconButton(
                        tooltip: 'Interrupt',
                        onPressed: task.id == null
                            ? null
                            : () => _interruptTask(task.id!),
                        icon: const Icon(Icons.stop),
                      ),
                    ],
                  ),
                ),
              ],
            );
          }).toList(),
        ),
        if (_detail != null) ...[
          const SizedBox(height: 16),
          FutureBuilder<TaskDetailData>(
            future: _detail,
            builder: (context, snapshot) {
              if (snapshot.connectionState != ConnectionState.done) {
                return const LinearProgressIndicator();
              }
              if (snapshot.hasError) {
                return ErrorView(
                  message: snapshot.error.toString(),
                  onRetry: _reloadSelectedTask,
                );
              }
              return TaskDetailPanel(detail: snapshot.data!);
            },
          ),
        ],
      ],
    );
  }

  Future<void> _createTask() async {
    final projectId =
        _projectId ??
        (widget.data.projects.isNotEmpty
            ? widget.data.projects.first.id
            : null);
    if (_title.text.trim().isEmpty || projectId == null) return;
    await widget.apiClient.createTask(
      title: _title.text.trim(),
      projectId: projectId,
      agentType: _agent,
    );
    _title.clear();
    widget.onChanged();
  }

  Future<void> _startTask(String taskId) async {
    await widget.apiClient.startTask(taskId);
    _reloadSelectedTask();
    widget.onChanged();
  }

  Future<void> _interruptTask(String taskId) async {
    await widget.apiClient.interruptTask(taskId);
    _reloadSelectedTask();
    widget.onChanged();
  }

  void _selectTask(String taskId) {
    setState(() {
      _selectedTaskId = taskId;
      _detail = widget.apiClient.taskDetail(taskId);
    });
  }

  void _reloadSelectedTask() {
    final taskId = _selectedTaskId;
    if (taskId == null) return;
    setState(() {
      _detail = widget.apiClient.taskDetail(taskId);
    });
  }
}

class TaskDetailPanel extends StatelessWidget {
  const TaskDetailPanel({super.key, required this.detail});

  final TaskDetailData detail;

  @override
  Widget build(BuildContext context) {
    final task = detail.task;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                task.title,
                style: Theme.of(context).textTheme.titleMedium,
              ),
            ),
            StatusPill(value: task.status),
          ],
        ),
        const SizedBox(height: 8),
        Wrap(
          spacing: 16,
          runSpacing: 8,
          children: [
            InfoText(icon: Icons.terminal, text: task.agentType),
            if ((task.workerId ?? '').isNotEmpty)
              InfoText(icon: Icons.memory, text: task.workerId!),
            if ((task.worktreePath ?? '').isNotEmpty)
              InfoText(icon: Icons.folder_open, text: task.worktreePath!),
            if ((task.result ?? '').isNotEmpty)
              InfoText(icon: Icons.flag, text: task.result!),
          ],
        ),
        const SizedBox(height: 16),
        LayoutBuilder(
          builder: (context, constraints) {
            final narrow = constraints.maxWidth < 760;
            final logs = RuntimeList(
              title: 'Logs',
              icon: Icons.article,
              children: detail.logs
                  .map((log) => '[${log.stream}] ${log.content}')
                  .toList(),
            );
            final conversation = RuntimeList(
              title: 'Conversation',
              icon: Icons.chat_bubble_outline,
              children: detail.conversations
                  .map((message) => '${message.role}: ${message.content}')
                  .toList(),
            );
            final events = RuntimeList(
              title: 'Domain events',
              icon: Icons.event_note,
              children: detail.events
                  .map(
                    (event) =>
                        '${event.eventType} ${event.aggregateVersion}: ${event.payload}',
                  )
                  .toList(),
            );
            if (narrow) {
              return Column(
                children: [
                  logs,
                  const SizedBox(height: 12),
                  conversation,
                  const SizedBox(height: 12),
                  events,
                ],
              );
            }
            return Column(
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(child: logs),
                    const SizedBox(width: 12),
                    Expanded(child: conversation),
                  ],
                ),
                const SizedBox(height: 12),
                events,
              ],
            );
          },
        ),
      ],
    );
  }
}

class InfoText extends StatelessWidget {
  const InfoText({super.key, required this.icon, required this.text});

  final IconData icon;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 16),
        const SizedBox(width: 6),
        ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 420),
          child: Text(text, overflow: TextOverflow.ellipsis),
        ),
      ],
    );
  }
}

class RuntimeList extends StatelessWidget {
  const RuntimeList({
    super.key,
    required this.title,
    required this.icon,
    required this.children,
  });

  final String title;
  final IconData icon;
  final List<String> children;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: double.infinity,
      child: DecoratedBox(
        decoration: BoxDecoration(
          border: Border.all(color: Theme.of(context).dividerColor),
          borderRadius: BorderRadius.circular(8),
        ),
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(icon, size: 18),
                  const SizedBox(width: 8),
                  Text(title, style: Theme.of(context).textTheme.titleSmall),
                ],
              ),
              const SizedBox(height: 8),
              if (children.isEmpty) const Text('No entries'),
              ...children.map(
                (item) => Padding(
                  padding: const EdgeInsets.only(bottom: 6),
                  child: SelectableText(
                    item,
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class BoardPage extends StatelessWidget {
  const BoardPage({super.key, required this.tasks});

  final List<TaskItem> tasks;

  static const columns = {
    'CREATED': 'Pending',
    'ASSIGNED': 'Assigned',
    'STARTING': 'Starting',
    'RUNNING': 'Running',
    'WAITING_INPUT': 'Waiting',
    'INTERRUPTING': 'Interrupting',
    'INTERRUPTED': 'Interrupted',
    'FAILED': 'Failed',
    'COMPLETED': 'Completed',
    'ARCHIVED': 'Archived',
  };

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.all(16),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: columns.entries.map((entry) {
          final columnTasks = tasks
              .where((task) => task.status == entry.key)
              .toList();
          return SizedBox(
            width: 220,
            child: Padding(
              padding: const EdgeInsets.only(right: 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '${entry.value} ${columnTasks.length}',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const SizedBox(height: 8),
                  ...columnTasks.map(
                    (task) => ListTile(
                      dense: true,
                      title: Text(task.title),
                      subtitle: Text(task.agentType),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(8),
                        side: BorderSide(color: Theme.of(context).dividerColor),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          );
        }).toList(),
      ),
    );
  }
}

class ProjectsPage extends StatefulWidget {
  const ProjectsPage({
    super.key,
    required this.apiClient,
    required this.data,
    required this.onChanged,
  });

  final ApiClient apiClient;
  final DashboardData data;
  final VoidCallback onChanged;

  @override
  State<ProjectsPage> createState() => _ProjectsPageState();
}

class _ProjectsPageState extends State<ProjectsPage> {
  final _name = TextEditingController();
  final _gitUrl = TextEditingController();
  final _prefix = TextEditingController(text: 'block-play-table');

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Wrap(
          spacing: 12,
          runSpacing: 12,
          children: [
            SizedBox(
              width: 220,
              child: TextField(
                controller: _name,
                decoration: const InputDecoration(
                  labelText: 'Name',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            SizedBox(
              width: 360,
              child: TextField(
                controller: _gitUrl,
                decoration: const InputDecoration(
                  labelText: 'Git URL',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            SizedBox(
              width: 220,
              child: TextField(
                controller: _prefix,
                decoration: const InputDecoration(
                  labelText: 'Worktree prefix',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            FilledButton.icon(
              onPressed: _createProject,
              icon: const Icon(Icons.add),
              label: const Text('Create'),
            ),
          ],
        ),
        const SizedBox(height: 16),
        ...widget.data.projects.map(
          (project) => ListTile(
            leading: const Icon(Icons.folder_copy),
            title: Text(project.name),
            subtitle: Text(project.gitUrl),
            trailing: Text(project.defaultBranch),
          ),
        ),
      ],
    );
  }

  Future<void> _createProject() async {
    if (_name.text.trim().isEmpty || _gitUrl.text.trim().isEmpty) return;
    await widget.apiClient.createProject(
      name: _name.text.trim(),
      gitUrl: _gitUrl.text.trim(),
      prefix: _prefix.text.trim(),
    );
    _name.clear();
    _gitUrl.clear();
    widget.onChanged();
  }
}

class WorkersPage extends StatelessWidget {
  const WorkersPage({super.key, required this.workers});

  final List<WorkerItem> workers;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: workers
          .map(
            (worker) => ListTile(
              leading: const Icon(Icons.memory),
              title: Text(worker.name),
              subtitle: Text('${worker.status}  ${worker.workDir}'),
              trailing: Text(worker.currentTaskId ?? 'Available'),
            ),
          )
          .toList(),
    );
  }
}

class EventAuditPage extends StatelessWidget {
  const EventAuditPage({super.key, required this.events});

  final List<DomainEventItem> events;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Text('Event audit', style: Theme.of(context).textTheme.titleLarge),
        const SizedBox(height: 12),
        if (events.isEmpty) const Text('No domain events'),
        ...events.reversed.map(
          (event) => ListTile(
            leading: const Icon(Icons.event_note),
            title: Text(event.eventType),
            subtitle: Text(
              '${event.aggregateType} ${event.aggregateId}\n${event.payload}',
              maxLines: 3,
              overflow: TextOverflow.ellipsis,
            ),
            trailing: SizedBox(
              width: 180,
              child: Text(
                event.occurredAt,
                textAlign: TextAlign.end,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ),
        ),
      ],
    );
  }
}

class SettingsPage extends StatelessWidget {
  const SettingsPage({super.key, required this.settings});

  final SettingsData settings;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Text('Trusted mode', style: Theme.of(context).textTheme.titleLarge),
        const SizedBox(height: 8),
        const Text(
          'Authentication and authorization are intentionally disabled in this build. Deploy Manager, UI, and Workers only on a trusted network.',
        ),
        const SizedBox(height: 24),
        Text(
          'Agent runtime environment',
          style: Theme.of(context).textTheme.titleMedium,
        ),
        ...settings.agentRuntimeEnvVars.map(
          (item) => ListTile(
            leading: Icon(item.enabled ? Icons.toggle_on : Icons.toggle_off),
            title: Text(item.key),
            subtitle: Text(item.valueMasked),
            trailing: item.sensitive ? const Icon(Icons.visibility_off) : null,
          ),
        ),
      ],
    );
  }
}

class StatusPill extends StatelessWidget {
  const StatusPill({super.key, required this.value});

  final String value;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.secondaryContainer,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text(value, style: Theme.of(context).textTheme.labelSmall),
    );
  }
}

class ErrorView extends StatelessWidget {
  const ErrorView({super.key, required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(message, textAlign: TextAlign.center),
          const SizedBox(height: 12),
          FilledButton.icon(
            onPressed: onRetry,
            icon: const Icon(Icons.refresh),
            label: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}

class ApiClient {
  ApiClient(this.endpoint);

  factory ApiClient.fromEnvironment() => ApiClient(
    const String.fromEnvironment(
      'MANAGER_GRAPHQL_URL',
      defaultValue: 'http://localhost:8080/graphql',
    ),
  );

  final String endpoint;

  Future<DashboardData> dashboard() async {
    final results = await Future.wait([
      graphQL(
        'query { tasks { id title status agentType workerId worktreePath result } }',
      ),
      graphQL(
        'query { projects { id name gitUrl defaultBranch worktreeNamePrefix } }',
      ),
      graphQL('query { workers { id name status workDir currentTaskId } }'),
      graphQL(
        'query { settings { agentRuntimeEnvVars { key valueMasked enabled sensitive } } }',
      ),
      graphQL(
        'query { domainEvents { eventId eventType aggregateType aggregateId aggregateVersion payload occurredAt } }',
      ),
    ]);
    return DashboardData(
      tasks: (results[0]['tasks'] as List<dynamic>? ?? [])
          .map((item) => TaskItem.fromJson(item as Map<String, dynamic>))
          .toList(),
      projects: (results[1]['projects'] as List<dynamic>? ?? [])
          .map((item) => ProjectItem.fromJson(item as Map<String, dynamic>))
          .toList(),
      workers: (results[2]['workers'] as List<dynamic>? ?? [])
          .map((item) => WorkerItem.fromJson(item as Map<String, dynamic>))
          .toList(),
      settings: SettingsData.fromJson(
        results[3]['settings'] as Map<String, dynamic>? ?? const {},
      ),
      events: (results[4]['domainEvents'] as List<dynamic>? ?? [])
          .map((item) => DomainEventItem.fromJson(item as Map<String, dynamic>))
          .toList(),
    );
  }

  Future<void> createProject({
    required String name,
    required String gitUrl,
    required String prefix,
  }) {
    return graphQL(
      'mutation CreateProject(\$input: CreateProjectInput!) { createProject(input: \$input) { id } }',
      variables: {
        'input': {
          'name': name,
          'gitUrl': gitUrl,
          'defaultBranch': 'main',
          'worktreeNamePrefix': prefix,
        },
      },
    ).then((_) {});
  }

  Future<void> createTask({
    required String title,
    required String projectId,
    required String agentType,
  }) {
    return graphQL(
      'mutation CreateTask(\$input: CreateTaskInput!) { createTask(input: \$input) { id } }',
      variables: {
        'input': {
          'title': title,
          'projectId': projectId,
          'agentType': agentType,
          'baseBranch': 'main',
          'targetBranch': 'task/$title',
        },
      },
    ).then((_) {});
  }

  Future<void> startTask(String taskId) => graphQL(
    'mutation StartTask(\$taskId: ID!) { startTask(taskId: \$taskId) { id } }',
    variables: {'taskId': taskId},
  ).then((_) {});

  Future<void> interruptTask(String taskId) => graphQL(
    'mutation InterruptTask(\$taskId: ID!) { interruptTask(taskId: \$taskId) { id } }',
    variables: {'taskId': taskId},
  ).then((_) {});

  Future<TaskDetailData> taskDetail(String taskId) async {
    final results = await Future.wait([
      graphQL(
        'query Task(\$id: ID!) { task(id: \$id) { id title status agentType workerId worktreePath result } }',
        variables: {'id': taskId},
      ),
      graphQL(
        'query TaskLogs(\$taskId: ID!) { taskLogs(taskId: \$taskId) { id stream content createdAt } }',
        variables: {'taskId': taskId},
      ),
      graphQL(
        'query TaskConversations(\$taskId: ID!) { taskConversations(taskId: \$taskId) { id role content createdAt } }',
        variables: {'taskId': taskId},
      ),
      graphQL(
        'query TaskEvents(\$taskId: ID!) { taskEvents(taskId: \$taskId) { eventId eventType aggregateType aggregateId aggregateVersion payload occurredAt } }',
        variables: {'taskId': taskId},
      ),
    ]);
    return TaskDetailData(
      task: TaskItem.fromJson(results[0]['task'] as Map<String, dynamic>),
      logs: (results[1]['taskLogs'] as List<dynamic>? ?? [])
          .map((item) => TaskLogItem.fromJson(item as Map<String, dynamic>))
          .toList(),
      conversations: (results[2]['taskConversations'] as List<dynamic>? ?? [])
          .map(
            (item) => ConversationItem.fromJson(item as Map<String, dynamic>),
          )
          .toList(),
      events: (results[3]['taskEvents'] as List<dynamic>? ?? [])
          .map((item) => DomainEventItem.fromJson(item as Map<String, dynamic>))
          .toList(),
    );
  }

  Future<Map<String, dynamic>> graphQL(
    String query, {
    Map<String, dynamic>? variables,
  }) async {
    final response = await http.post(
      Uri.parse(endpoint),
      headers: {'content-type': 'application/json'},
      body: jsonEncode({'query': query, 'variables': variables ?? {}}),
    );
    final body = jsonDecode(response.body) as Map<String, dynamic>;
    if (response.statusCode >= 400 || body['errors'] != null) {
      throw StateError(body['errors']?.toString() ?? response.body);
    }
    return body['data'] as Map<String, dynamic>;
  }
}

class DashboardData {
  DashboardData({
    required this.tasks,
    required this.projects,
    required this.workers,
    required this.settings,
    required this.events,
  });

  factory DashboardData.empty() => DashboardData(
    tasks: const [],
    projects: const [],
    workers: const [],
    settings: SettingsData(agentRuntimeEnvVars: const []),
    events: const [],
  );

  final List<TaskItem> tasks;
  final List<ProjectItem> projects;
  final List<WorkerItem> workers;
  final SettingsData settings;
  final List<DomainEventItem> events;
}

class TaskItem {
  TaskItem({
    required this.id,
    required this.title,
    required this.status,
    required this.agentType,
    this.workerId,
    this.worktreePath,
    this.result,
  });

  factory TaskItem.fromJson(Map<String, dynamic> json) => TaskItem(
    id: json['id'] as String?,
    title: json['title'] as String? ?? '',
    status: json['status'] as String? ?? '',
    agentType: json['agentType'] as String? ?? '',
    workerId: json['workerId'] as String?,
    worktreePath: json['worktreePath'] as String?,
    result: json['result'] as String?,
  );

  final String? id;
  final String title;
  final String status;
  final String agentType;
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
  TaskLogItem({required this.stream, required this.content});

  factory TaskLogItem.fromJson(Map<String, dynamic> json) => TaskLogItem(
    stream: json['stream'] as String? ?? '',
    content: json['content'] as String? ?? '',
  );

  final String stream;
  final String content;
}

class ConversationItem {
  ConversationItem({required this.role, required this.content});

  factory ConversationItem.fromJson(Map<String, dynamic> json) =>
      ConversationItem(
        role: json['role'] as String? ?? 'assistant',
        content: json['content'] as String? ?? '',
      );

  final String role;
  final String content;
}

class DomainEventItem {
  DomainEventItem({
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
      payload: payload == null ? 'null' : jsonEncode(payload),
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
  ProjectItem({
    required this.id,
    required this.name,
    required this.gitUrl,
    required this.defaultBranch,
  });

  factory ProjectItem.fromJson(Map<String, dynamic> json) => ProjectItem(
    id: json['id'] as String,
    name: json['name'] as String? ?? '',
    gitUrl: json['gitUrl'] as String? ?? '',
    defaultBranch: json['defaultBranch'] as String? ?? '',
  );

  final String id;
  final String name;
  final String gitUrl;
  final String defaultBranch;
}

class WorkerItem {
  WorkerItem({
    required this.id,
    required this.name,
    required this.status,
    required this.workDir,
    this.currentTaskId,
  });

  factory WorkerItem.fromJson(Map<String, dynamic> json) => WorkerItem(
    id: json['id'] as String,
    name: json['name'] as String? ?? '',
    status: json['status'] as String? ?? '',
    workDir: json['workDir'] as String? ?? '',
    currentTaskId: json['currentTaskId'] as String?,
  );

  final String id;
  final String name;
  final String status;
  final String workDir;
  final String? currentTaskId;
}

class SettingsData {
  SettingsData({required this.agentRuntimeEnvVars});

  factory SettingsData.fromJson(Map<String, dynamic> json) => SettingsData(
    agentRuntimeEnvVars: (json['agentRuntimeEnvVars'] as List<dynamic>? ?? [])
        .map((item) => EnvVarItem.fromJson(item as Map<String, dynamic>))
        .toList(),
  );

  final List<EnvVarItem> agentRuntimeEnvVars;
}

class EnvVarItem {
  EnvVarItem({
    required this.key,
    required this.valueMasked,
    required this.enabled,
    required this.sensitive,
  });

  factory EnvVarItem.fromJson(Map<String, dynamic> json) => EnvVarItem(
    key: json['key'] as String? ?? '',
    valueMasked: json['valueMasked'] as String? ?? '',
    enabled: json['enabled'] as bool? ?? false,
    sensitive: json['sensitive'] as bool? ?? false,
  );

  final String key;
  final String valueMasked;
  final bool enabled;
  final bool sensitive;
}
