import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:graphql_flutter/graphql_flutter.dart';

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
                    WorkersPage(
                      apiClient: widget.apiClient,
                      data: data,
                      onChanged: _refresh,
                    ),
                    EventAuditPage(events: data.events),
                    SettingsPage(
                      apiClient: widget.apiClient,
                      settings: data.settings,
                      onChanged: _refresh,
                    ),
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
  final _description = TextEditingController();
  final _baseBranch = TextEditingController(text: 'main');
  final _targetBranch = TextEditingController();
  final _preCommands = TextEditingController();
  final _postCommands = TextEditingController();
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
            SizedBox(
              width: 360,
              child: TextField(
                controller: _description,
                decoration: const InputDecoration(
                  labelText: 'Description',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            SizedBox(
              width: 180,
              child: TextField(
                controller: _baseBranch,
                decoration: const InputDecoration(
                  labelText: 'Base branch',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            SizedBox(
              width: 220,
              child: TextField(
                controller: _targetBranch,
                decoration: const InputDecoration(
                  labelText: 'Target branch',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            SizedBox(
              width: 260,
              child: TextField(
                controller: _preCommands,
                decoration: const InputDecoration(
                  labelText: 'Pre commands',
                  border: OutlineInputBorder(),
                ),
                minLines: 1,
                maxLines: 3,
              ),
            ),
            SizedBox(
              width: 260,
              child: TextField(
                controller: _postCommands,
                decoration: const InputDecoration(
                  labelText: 'Post commands',
                  border: OutlineInputBorder(),
                ),
                minLines: 1,
                maxLines: 3,
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
                        tooltip: 'Assign first worker',
                        onPressed: task.id == null
                            ? null
                            : () => _assignTask(task.id!),
                        icon: const Icon(Icons.person_add_alt_1),
                      ),
                      IconButton(
                        tooltip: 'Interrupt',
                        onPressed: task.id == null
                            ? null
                            : () => _interruptTask(task.id!),
                        icon: const Icon(Icons.stop),
                      ),
                      IconButton(
                        tooltip: 'Archive',
                        onPressed: task.id == null
                            ? null
                            : () => _archiveTask(task.id!),
                        icon: const Icon(Icons.archive),
                      ),
                      IconButton(
                        tooltip: 'Retry',
                        onPressed: task.id == null
                            ? null
                            : () => _retryTask(task.id!),
                        icon: const Icon(Icons.replay),
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
      description: _description.text.trim(),
      projectId: projectId,
      agentType: _agent,
      baseBranch: _baseBranch.text.trim().isEmpty
          ? 'main'
          : _baseBranch.text.trim(),
      targetBranch: _targetBranch.text.trim(),
      preCommands: stringList(_preCommands.text),
      postCommands: stringList(_postCommands.text),
    );
    _title.clear();
    _description.clear();
    _targetBranch.clear();
    _preCommands.clear();
    _postCommands.clear();
    widget.onChanged();
  }

  Future<void> _assignTask(String taskId) async {
    final task = widget.data.tasks.firstWhere((item) => item.id == taskId);
    final candidates = widget.data.workers.where((worker) {
      final supports = worker.supportedAgents.contains(task.agentType);
      final projectMatches = worker.projectBindingMode == 'ALL_PROJECTS' ||
          worker.boundProjectIds.contains(task.projectId);
      return worker.status == 'ONLINE' &&
          (worker.currentTaskId ?? '').isEmpty &&
          supports &&
          projectMatches;
    }).toList();
    if (candidates.isEmpty) return;
    await widget.apiClient.assignWorker(taskId, candidates.first.id);
    _reloadSelectedTask();
    widget.onChanged();
  }

  Future<void> _startTask(String taskId) async {
    await widget.apiClient.startTask(taskId);
    _reloadSelectedTask();
    widget.onChanged();
  }

  Future<void> _archiveTask(String taskId) async {
    await widget.apiClient.archiveTask(taskId);
    _reloadSelectedTask();
    widget.onChanged();
  }

  Future<void> _retryTask(String taskId) async {
    await widget.apiClient.retryTask(taskId);
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

class BoardPage extends StatefulWidget {
  const BoardPage({super.key, required this.tasks});

  final List<TaskItem> tasks;

  @override
  State<BoardPage> createState() => _BoardPageState();
}

class _BoardPageState extends State<BoardPage> {
  String _view = 'KANBAN';

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
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(16),
          child: Align(
            alignment: Alignment.centerLeft,
            child: SegmentedButton<String>(
              segments: const [
                ButtonSegment(
                  value: 'KANBAN',
                  icon: Icon(Icons.view_kanban),
                  label: Text('Kanban'),
                ),
                ButtonSegment(
                  value: 'LIST',
                  icon: Icon(Icons.table_rows),
                  label: Text('List'),
                ),
                ButtonSegment(
                  value: 'CALENDAR',
                  icon: Icon(Icons.calendar_month),
                  label: Text('Calendar'),
                ),
              ],
              selected: {_view},
              onSelectionChanged: (values) =>
                  setState(() => _view = values.first),
            ),
          ),
        ),
        Expanded(child: _buildView(context)),
      ],
    );
  }

  Widget _buildView(BuildContext context) {
    if (_view == 'LIST') {
      return ListView(
        padding: const EdgeInsets.all(16),
        children: widget.tasks
            .map(
              (task) => ListTile(
                leading: const Icon(Icons.task_alt),
                title: Text(task.title),
                subtitle: Text('${task.status}  ${task.projectId}'),
                trailing: Text(task.updatedAt),
              ),
            )
            .toList(),
      );
    }
    if (_view == 'CALENDAR') {
      return ListView(
        padding: const EdgeInsets.all(16),
        children: widget.tasks
            .map(
              (task) => ListTile(
                leading: const Icon(Icons.event),
                title: Text(task.title),
                subtitle: Text(task.createdAt),
                trailing: StatusPill(value: task.status),
              ),
            )
            .toList(),
      );
    }
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.all(16),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: columns.entries.map((entry) {
          final columnTasks = widget.tasks
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
  final _defaultBranch = TextEditingController(text: 'main');
  final _prefix = TextEditingController(text: 'block-play-table');
  final _setupCommands = TextEditingController();

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
                controller: _defaultBranch,
                decoration: const InputDecoration(
                  labelText: 'Default branch',
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
            SizedBox(
              width: 300,
              child: TextField(
                controller: _setupCommands,
                decoration: const InputDecoration(
                  labelText: 'Setup commands',
                  border: OutlineInputBorder(),
                ),
                minLines: 1,
                maxLines: 3,
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
            subtitle: Text(
              '${project.gitUrl}\n${project.defaultBranch}  ${project.worktreeNamePrefix}',
            ),
            trailing: Wrap(
              spacing: 4,
              children: [
                if (project.archived) const StatusPill(value: 'ARCHIVED'),
                IconButton(
                  tooltip: 'Load for edit',
                  onPressed: () => _loadProject(project),
                  icon: const Icon(Icons.edit),
                ),
                IconButton(
                  tooltip: 'Save edits',
                  onPressed: () => _updateProject(project.id),
                  icon: const Icon(Icons.save),
                ),
                IconButton(
                  tooltip: 'Archive',
                  onPressed: project.archived
                      ? null
                      : () => _archiveProject(project.id),
                  icon: const Icon(Icons.archive),
                ),
              ],
            ),
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
      defaultBranch: _defaultBranch.text.trim().isEmpty
          ? 'main'
          : _defaultBranch.text.trim(),
      setupCommands: stringList(_setupCommands.text),
    );
    _name.clear();
    _gitUrl.clear();
    _setupCommands.clear();
    widget.onChanged();
  }

  void _loadProject(ProjectItem project) {
    _name.text = project.name;
    _gitUrl.text = project.gitUrl;
    _defaultBranch.text = project.defaultBranch;
    _prefix.text = project.worktreeNamePrefix;
    _setupCommands.text = project.setupCommands.join('\n');
  }

  Future<void> _updateProject(String projectId) async {
    if (_name.text.trim().isEmpty || _gitUrl.text.trim().isEmpty) return;
    await widget.apiClient.updateProject(
      ProjectItem(
        id: projectId,
        name: _name.text.trim(),
        gitUrl: _gitUrl.text.trim(),
        defaultBranch: _defaultBranch.text.trim().isEmpty
            ? 'main'
            : _defaultBranch.text.trim(),
        worktreeNamePrefix: _prefix.text.trim(),
        setupCommands: stringList(_setupCommands.text),
        archived: false,
      ),
    );
    widget.onChanged();
  }

  Future<void> _archiveProject(String projectId) async {
    await widget.apiClient.archiveProject(projectId);
    widget.onChanged();
  }
}

class WorkersPage extends StatefulWidget {
  const WorkersPage({
    super.key,
    required this.apiClient,
    required this.data,
    required this.onChanged,
  });

  final ApiClient apiClient;
  final DashboardData data;
  final VoidCallback onChanged;

  @override
  State<WorkersPage> createState() => _WorkersPageState();
}

class _WorkersPageState extends State<WorkersPage> {
  final _id = TextEditingController();
  final _name = TextEditingController(text: 'local-worker');
  final _workDir = TextEditingController(text: './worker-data');
  final _startupCommand = TextEditingController();
  String _bindingMode = 'ALL_PROJECTS';
  final Set<String> _agents = {'codex'};
  final Set<String> _boundProjectIds = {};

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
              width: 180,
              child: TextField(
                controller: _id,
                decoration: const InputDecoration(
                  labelText: 'Worker ID',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
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
              width: 260,
              child: TextField(
                controller: _workDir,
                decoration: const InputDecoration(
                  labelText: 'Work dir',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            SizedBox(
              width: 260,
              child: TextField(
                controller: _startupCommand,
                decoration: const InputDecoration(
                  labelText: 'Startup command',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(
                  value: 'codex',
                  icon: Icon(Icons.terminal),
                  label: Text('Codex'),
                ),
                ButtonSegment(
                  value: 'claude',
                  icon: Icon(Icons.chat),
                  label: Text('Claude'),
                ),
              ],
              selected: _agents,
              multiSelectionEnabled: true,
              onSelectionChanged: (values) =>
                  setState(() => _agents
                    ..clear()
                    ..addAll(values)),
            ),
            DropdownButton<String>(
              value: _bindingMode,
              items: const [
                DropdownMenuItem(
                  value: 'ALL_PROJECTS',
                  child: Text('All projects'),
                ),
                DropdownMenuItem(
                  value: 'SPECIFIC_PROJECTS',
                  child: Text('Specific projects'),
                ),
              ],
              onChanged: (value) =>
                  setState(() => _bindingMode = value ?? 'ALL_PROJECTS'),
            ),
            ...widget.data.projects.map(
              (project) => FilterChip(
                label: Text(project.name),
                selected: _boundProjectIds.contains(project.id),
                onSelected: _bindingMode == 'ALL_PROJECTS'
                    ? null
                    : (selected) => setState(() {
                        if (selected) {
                          _boundProjectIds.add(project.id);
                        } else {
                          _boundProjectIds.remove(project.id);
                        }
                      }),
              ),
            ),
            FilledButton.icon(
              onPressed: _createWorker,
              icon: const Icon(Icons.add),
              label: const Text('Create'),
            ),
            FilledButton.icon(
              onPressed: _id.text.trim().isEmpty ? null : _updateWorker,
              icon: const Icon(Icons.save),
              label: const Text('Save'),
            ),
          ],
        ),
        const SizedBox(height: 16),
        ...widget.data.workers.map(
          (worker) => ListTile(
            leading: const Icon(Icons.memory),
            title: Text(worker.name),
            subtitle: Text(
              '${worker.status}  ${worker.workDir}\n${worker.supportedAgents.join(', ')}  ${worker.projectBindingMode}',
            ),
            trailing: Wrap(
              spacing: 4,
              children: [
                Text(worker.currentTaskId ?? 'Available'),
                IconButton(
                  tooltip: 'Load for edit',
                  onPressed: () => _loadWorker(worker),
                  icon: const Icon(Icons.edit),
                ),
                IconButton(
                  tooltip: 'Enable',
                  onPressed: () => _enableWorker(worker.id),
                  icon: const Icon(Icons.toggle_on),
                ),
                IconButton(
                  tooltip: 'Disable',
                  onPressed: () => _disableWorker(worker.id),
                  icon: const Icon(Icons.toggle_off),
                ),
                IconButton(
                  tooltip: 'Delete',
                  onPressed: (worker.currentTaskId ?? '').isEmpty
                      ? () => _deleteWorker(worker.id)
                      : null,
                  icon: const Icon(Icons.delete),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }

  Future<void> _createWorker() async {
    if (_name.text.trim().isEmpty || _workDir.text.trim().isEmpty) return;
    await widget.apiClient.createWorker(
      id: _id.text.trim(),
      name: _name.text.trim(),
      supportedAgents: _agents.toList(),
      workDir: _workDir.text.trim(),
      startupCommand: _startupCommand.text.trim(),
      projectBindingMode: _bindingMode,
      boundProjectIds: _bindingMode == 'ALL_PROJECTS'
          ? const []
          : _boundProjectIds.toList(),
    );
    widget.onChanged();
  }

  Future<void> _updateWorker() async {
    if (_id.text.trim().isEmpty) return;
    await widget.apiClient.updateWorker(
      WorkerItem(
        id: _id.text.trim(),
        name: _name.text.trim(),
        status: '',
        supportedAgents: _agents.toList(),
        workDir: _workDir.text.trim(),
        startupCommand: _startupCommand.text.trim(),
        projectBindingMode: _bindingMode,
        boundProjectIds: _bindingMode == 'ALL_PROJECTS'
            ? const []
            : _boundProjectIds.toList(),
      ),
    );
    widget.onChanged();
  }

  void _loadWorker(WorkerItem worker) {
    _id.text = worker.id;
    _name.text = worker.name;
    _workDir.text = worker.workDir;
    _startupCommand.text = worker.startupCommand;
    setState(() {
      _bindingMode = worker.projectBindingMode;
      _agents
        ..clear()
        ..addAll(worker.supportedAgents);
      _boundProjectIds
        ..clear()
        ..addAll(worker.boundProjectIds);
    });
  }

  Future<void> _enableWorker(String id) async {
    await widget.apiClient.enableWorker(id);
    widget.onChanged();
  }

  Future<void> _disableWorker(String id) async {
    await widget.apiClient.disableWorker(id);
    widget.onChanged();
  }

  Future<void> _deleteWorker(String id) async {
    await widget.apiClient.deleteWorker(id);
    widget.onChanged();
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

class SettingsPage extends StatefulWidget {
  const SettingsPage({
    super.key,
    required this.apiClient,
    required this.settings,
    required this.onChanged,
  });

  final ApiClient apiClient;
  final SettingsData settings;
  final VoidCallback onChanged;

  @override
  State<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends State<SettingsPage> {
  late TextEditingController _heartbeat;
  final _key = TextEditingController();
  final _value = TextEditingController();
  final _description = TextEditingController();
  bool _enabled = true;
  bool _sensitive = true;
  late List<EnvVarItem> _vars;

  @override
  void initState() {
    super.initState();
    _heartbeat = TextEditingController(
      text: widget.settings.workerHeartbeatTimeout,
    );
    _vars = List<EnvVarItem>.from(widget.settings.agentRuntimeEnvVars);
  }

  @override
  void didUpdateWidget(covariant SettingsPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.settings != widget.settings) {
      _heartbeat.text = widget.settings.workerHeartbeatTimeout;
      _vars = List<EnvVarItem>.from(widget.settings.agentRuntimeEnvVars);
    }
  }

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
        const SizedBox(height: 16),
        SizedBox(
          width: 240,
          child: TextField(
            controller: _heartbeat,
            decoration: const InputDecoration(
              labelText: 'Worker heartbeat timeout',
              border: OutlineInputBorder(),
            ),
          ),
        ),
        const SizedBox(height: 24),
        Text(
          'Agent runtime environment',
          style: Theme.of(context).textTheme.titleMedium,
        ),
        const SizedBox(height: 12),
        Wrap(
          spacing: 12,
          runSpacing: 12,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            SizedBox(
              width: 220,
              child: TextField(
                controller: _key,
                decoration: const InputDecoration(
                  labelText: 'Key',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            SizedBox(
              width: 260,
              child: TextField(
                controller: _value,
                decoration: const InputDecoration(
                  labelText: 'Value',
                  border: OutlineInputBorder(),
                ),
                obscureText: _sensitive,
              ),
            ),
            SizedBox(
              width: 260,
              child: TextField(
                controller: _description,
                decoration: const InputDecoration(
                  labelText: 'Description',
                  border: OutlineInputBorder(),
                ),
              ),
            ),
            FilterChip(
              label: const Text('Enabled'),
              selected: _enabled,
              onSelected: (value) => setState(() => _enabled = value),
            ),
            FilterChip(
              label: const Text('Sensitive'),
              selected: _sensitive,
              onSelected: (value) => setState(() => _sensitive = value),
            ),
            FilledButton.icon(
              onPressed: _addEnvVar,
              icon: const Icon(Icons.add),
              label: const Text('Add'),
            ),
            FilledButton.icon(
              onPressed: _save,
              icon: const Icon(Icons.save),
              label: const Text('Save'),
            ),
          ],
        ),
        const SizedBox(height: 12),
        ..._vars.map(
          (item) => ListTile(
            leading: Icon(item.enabled ? Icons.toggle_on : Icons.toggle_off),
            title: Text(item.key),
            subtitle: Text('${item.valueMasked}  ${item.description}'),
            trailing: Wrap(
              spacing: 4,
              children: [
                if (item.sensitive) const Icon(Icons.visibility_off),
                IconButton(
                  tooltip: 'Remove',
                  onPressed: () => setState(
                    () => _vars.removeWhere((env) => env.key == item.key),
                  ),
                  icon: const Icon(Icons.delete),
                ),
              ],
            ),
          ),
        ),
      ],
    );
  }

  void _addEnvVar() {
    if (_key.text.trim().isEmpty) return;
    final item = EnvVarItem(
      key: _key.text.trim(),
      valueMasked: _sensitive ? '********' : _value.text,
      description: _description.text.trim(),
      enabled: _enabled,
      sensitive: _sensitive,
      valueInput: _value.text,
    );
    setState(() {
      _vars.removeWhere((env) => env.key == item.key);
      _vars.add(item);
      _key.clear();
      _value.clear();
      _description.clear();
      _enabled = true;
      _sensitive = true;
    });
  }

  Future<void> _save() async {
    await widget.apiClient.updateSettings(
      SettingsData(
        agentRuntimeEnvVars: _vars,
        workerHeartbeatTimeout: _heartbeat.text.trim().isEmpty
            ? '90s'
            : _heartbeat.text.trim(),
        securityPolicy: widget.settings.securityPolicy,
      ),
    );
    widget.onChanged();
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
  ApiClient(this.endpoint)
    : _client = GraphQLClient(
        link: HttpLink(endpoint),
        cache: GraphQLCache(store: InMemoryStore()),
      );

  factory ApiClient.fromEnvironment() => ApiClient(
    const String.fromEnvironment(
      'MANAGER_GRAPHQL_URL',
      defaultValue: 'http://localhost:8080/graphql',
    ),
  );

  final String endpoint;
  final GraphQLClient _client;

  Future<DashboardData> dashboard() async {
    final results = await Future.wait([
      graphQL(
        'query { tasks { nodes { id title description status projectId agentType baseBranch targetBranch workerId worktreePath preCommands postCommands result createdAt updatedAt } totalCount } }',
      ),
      graphQL(
        'query { projects(filter: { includeArchived: true }) { id name gitUrl defaultBranch worktreeNamePrefix setupCommands archived } }',
      ),
      graphQL(
        'query { workers(filter: { includeDisabled: true }) { id name status supportedAgents workDir startupCommand projectBindingMode boundProjectIds currentTaskId lastHeartbeatAt } }',
      ),
      graphQL(
        'query { settings { id workerHeartbeatTimeout securityPolicy agentRuntimeEnvVars { key valueMasked description enabled sensitive } } }',
      ),
      graphQL(
        'query { domainEvents { eventId eventType aggregateType aggregateId aggregateVersion payload occurredAt } }',
      ),
    ]);
    final taskConnection = results[0]['tasks'] as Map<String, dynamic>? ?? {};
    return DashboardData(
      tasks: (taskConnection['nodes'] as List<dynamic>? ?? [])
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
    String defaultBranch = 'main',
    List<String> setupCommands = const [],
  }) {
    return graphQL(
      'mutation CreateProject(\$input: CreateProjectInput!) { createProject(input: \$input) { id } }',
      variables: {
        'input': {
          'name': name,
          'gitUrl': gitUrl,
          'defaultBranch': defaultBranch,
          'worktreeNamePrefix': prefix,
          'setupCommands': setupCommands,
        },
      },
    ).then((_) {});
  }

  Future<void> updateProject(ProjectItem project) {
    return graphQL(
      'mutation UpdateProject(\$input: UpdateProjectInput!) { updateProject(input: \$input) { id } }',
      variables: {
        'input': {
          'id': project.id,
          'name': project.name,
          'gitUrl': project.gitUrl,
          'defaultBranch': project.defaultBranch,
          'worktreeNamePrefix': project.worktreeNamePrefix,
          'setupCommands': project.setupCommands,
        },
      },
    ).then((_) {});
  }

  Future<void> archiveProject(String projectId) => graphQL(
    'mutation ArchiveProject(\$id: ID!) { archiveProject(id: \$id) { id } }',
    variables: {'id': projectId},
  ).then((_) {});

  Future<void> createTask({
    required String title,
    String description = '',
    required String projectId,
    required String agentType,
    String baseBranch = 'main',
    String targetBranch = '',
    List<String> preCommands = const [],
    List<String> postCommands = const [],
  }) {
    return graphQL(
      'mutation CreateTask(\$input: CreateTaskInput!) { createTask(input: \$input) { id } }',
      variables: {
        'input': {
          'title': title,
          'description': description,
          'projectId': projectId,
          'agentType': agentType,
          'baseBranch': baseBranch,
          'targetBranch': targetBranch.isEmpty ? 'task/$title' : targetBranch,
          'preCommands': preCommands,
          'postCommands': postCommands,
        },
      },
    ).then((_) {});
  }

  Future<void> updateTask(TaskItem task) => graphQL(
    'mutation UpdateTask(\$input: UpdateTaskInput!) { updateTask(input: \$input) { id } }',
    variables: {
      'input': {
        'id': task.id,
        'title': task.title,
        'description': task.description,
        'projectId': task.projectId,
        'agentType': task.agentType,
        'baseBranch': task.baseBranch,
        'targetBranch': task.targetBranch,
        'preCommands': task.preCommands,
        'postCommands': task.postCommands,
      },
    },
  ).then((_) {});

  Future<void> assignWorker(String taskId, String workerId) => graphQL(
    'mutation AssignWorker(\$input: AssignWorkerInput!) { assignWorker(input: \$input) { id } }',
    variables: {
      'input': {'taskId': taskId, 'workerId': workerId},
    },
  ).then((_) {});

  Future<void> startTask(String taskId) => graphQL(
    'mutation StartTask(\$taskId: ID!) { startTask(taskId: \$taskId) { id } }',
    variables: {'taskId': taskId},
  ).then((_) {});

  Future<void> interruptTask(String taskId) => graphQL(
    'mutation InterruptTask(\$taskId: ID!) { interruptTask(taskId: \$taskId) { id } }',
    variables: {'taskId': taskId},
  ).then((_) {});

  Future<void> archiveTask(String taskId) => graphQL(
    'mutation ArchiveTask(\$taskId: ID!) { archiveTask(taskId: \$taskId) { id } }',
    variables: {'taskId': taskId},
  ).then((_) {});

  Future<void> retryTask(String taskId) => graphQL(
    'mutation RetryTask(\$taskId: ID!) { retryTask(taskId: \$taskId) { id } }',
    variables: {'taskId': taskId},
  ).then((_) {});

  Future<void> createWorker({
    String id = '',
    required String name,
    required List<String> supportedAgents,
    required String workDir,
    String startupCommand = '',
    String projectBindingMode = 'ALL_PROJECTS',
    List<String> boundProjectIds = const [],
  }) {
    return graphQL(
      'mutation CreateWorker(\$input: CreateWorkerInput!) { createWorker(input: \$input) { id } }',
      variables: {
        'input': {
          if (id.isNotEmpty) 'id': id,
          'name': name,
          'supportedAgents': supportedAgents,
          'workDir': workDir,
          'startupCommand': startupCommand,
          'projectBindingMode': projectBindingMode,
          'boundProjectIds': boundProjectIds,
        },
      },
    ).then((_) {});
  }

  Future<void> updateWorker(WorkerItem worker) => graphQL(
    'mutation UpdateWorker(\$input: UpdateWorkerInput!) { updateWorker(input: \$input) { id } }',
    variables: {
      'input': {
        'id': worker.id,
        'name': worker.name,
        'supportedAgents': worker.supportedAgents,
        'workDir': worker.workDir,
        'startupCommand': worker.startupCommand,
        'projectBindingMode': worker.projectBindingMode,
        'boundProjectIds': worker.boundProjectIds,
      },
    },
  ).then((_) {});

  Future<void> enableWorker(String workerId) => graphQL(
    'mutation EnableWorker(\$id: ID!) { enableWorker(id: \$id) { id } }',
    variables: {'id': workerId},
  ).then((_) {});

  Future<void> disableWorker(String workerId) => graphQL(
    'mutation DisableWorker(\$id: ID!) { disableWorker(id: \$id) { id } }',
    variables: {'id': workerId},
  ).then((_) {});

  Future<void> deleteWorker(String workerId) => graphQL(
    'mutation DeleteWorker(\$id: ID!) { deleteWorker(id: \$id) }',
    variables: {'id': workerId},
  ).then((_) {});

  Future<void> updateSettings(SettingsData settings) => graphQL(
    'mutation UpdateSettings(\$input: UpdateAgentRuntimeEnvVarsInput!, \$timeout: String!) { updateAgentRuntimeEnvVars(input: \$input) { id } updateWorkerHeartbeatTimeout(timeout: \$timeout) { id } }',
    variables: {
      'input': {
        'vars': settings.agentRuntimeEnvVars
            .map(
              (item) => {
                'key': item.key,
                if (item.valueInput.isNotEmpty) 'value': item.valueInput,
                'description': item.description,
                'enabled': item.enabled,
                'sensitive': item.sensitive,
              },
            )
            .toList(),
      },
      'timeout': settings.workerHeartbeatTimeout,
    },
  ).then((_) {});

  Future<TaskDetailData> taskDetail(String taskId) async {
    final results = await Future.wait([
      graphQL(
        'query Task(\$id: ID!) { task(id: \$id) { id title description status projectId agentType baseBranch targetBranch workerId worktreePath preCommands postCommands result createdAt updatedAt } }',
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
    final result = await _client.query(
      QueryOptions(
        document: gql(query),
        variables: variables ?? const {},
        fetchPolicy: FetchPolicy.networkOnly,
      ),
    );
    if (result.hasException) {
      throw StateError(result.exception.toString());
    }
    return result.data ?? <String, dynamic>{};
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
    required this.description,
    required this.status,
    required this.projectId,
    required this.agentType,
    required this.baseBranch,
    required this.targetBranch,
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
    targetBranch: json['targetBranch'] as String? ?? '',
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
  final String targetBranch;
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
  ProjectItem({
    required this.id,
    required this.name,
    required this.gitUrl,
    required this.defaultBranch,
    required this.worktreeNamePrefix,
    required this.setupCommands,
    required this.archived,
  });

  factory ProjectItem.fromJson(Map<String, dynamic> json) => ProjectItem(
    id: json['id'] as String,
    name: json['name'] as String? ?? '',
    gitUrl: json['gitUrl'] as String? ?? '',
    defaultBranch: json['defaultBranch'] as String? ?? '',
    worktreeNamePrefix: json['worktreeNamePrefix'] as String? ?? '',
    setupCommands: stringList(json['setupCommands']),
    archived: json['archived'] as bool? ?? false,
  );

  final String id;
  final String name;
  final String gitUrl;
  final String defaultBranch;
  final String worktreeNamePrefix;
  final List<String> setupCommands;
  final bool archived;
}

class WorkerItem {
  WorkerItem({
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
    id: json['id'] as String,
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
  SettingsData({
    required this.agentRuntimeEnvVars,
    this.workerHeartbeatTimeout = '90s',
    this.securityPolicy = 'TRUSTED',
  });

  factory SettingsData.fromJson(Map<String, dynamic> json) => SettingsData(
    agentRuntimeEnvVars: (json['agentRuntimeEnvVars'] as List<dynamic>? ?? [])
        .map((item) => EnvVarItem.fromJson(item as Map<String, dynamic>))
        .toList(),
    workerHeartbeatTimeout:
        json['workerHeartbeatTimeout'] as String? ?? '90s',
    securityPolicy: json['securityPolicy'] as String? ?? 'TRUSTED',
  );

  final List<EnvVarItem> agentRuntimeEnvVars;
  final String workerHeartbeatTimeout;
  final String securityPolicy;
}

class EnvVarItem {
  EnvVarItem({
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
    return value.split('\n').map((item) => item.trim()).where((item) => item.isNotEmpty).toList();
  }
  return const [];
}
