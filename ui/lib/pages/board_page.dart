import 'dart:convert';
import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:xterm/xterm.dart';

import '../api_client.dart';
import '../board_status_groups.dart';
import '../models.dart';
import '../realtime_refresh.dart';
import '../widgets.dart';
import '../worker_terminal_socket.dart';
import '../worker_web_preview.dart';

class BoardPage extends StatefulWidget {
  const BoardPage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<BoardPage> createState() => _BoardPageState();
}

class _BoardPageState extends State<BoardPage> {
  String _view = 'KANBAN';
  String _selectedProjectId = '';
  PageRequest _page = const PageRequest();
  final TextEditingController _searchController = TextEditingController();
  SortRequest _sort = const SortRequest(field: 'CREATED_AT');
  late Future<BoardData> _future;
  BoardData? _lastData;
  RealtimeRefreshController? _realtime;

  @override
  void initState() {
    super.initState();
    _future = _load();
    _subscribe();
  }

  @override
  void didUpdateWidget(covariant BoardPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.apiClient != widget.apiClient) {
      _realtime?.dispose();
      _future = _load();
      _subscribe();
    }
  }

  @override
  void dispose() {
    _realtime?.dispose();
    _searchController.dispose();
    super.dispose();
  }

  Future<BoardData> _load() async {
    var data = await widget.apiClient.fetchBoardData(
      _view,
      page: _page,
      search: _searchController.text,
      sort: _sort,
      projectId: _selectedProjectId,
    );
    if (data.tasks.isEmpty && data.totalCount > 0 && _page.offset > 0) {
      final corrected = _page.withOffset(_page.lastOffset(data.totalCount));
      if (corrected.offset != _page.offset) {
        _page = corrected;
        data = await widget.apiClient.fetchBoardData(
          _view,
          page: _page,
          search: _searchController.text,
          sort: _sort,
          projectId: _selectedProjectId,
        );
      }
    }
    _rememberData(data);
    return data;
  }

  void _rememberData(BoardData data) {
    final oldProjectIds = (_lastData?.projects ?? const <ProjectItem>[])
        .map((project) => project.id)
        .join('\n');
    final newProjectIds = data.projects.map((project) => project.id).join('\n');
    _lastData = data;
    if (mounted && oldProjectIds != newProjectIds) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) {
          setState(() {});
        }
      });
    }
  }

  void _reload({bool firstPage = false}) {
    if (!mounted) {
      return;
    }
    setState(() {
      if (firstPage) {
        _page = _page.first();
      }
      _future = _load();
    });
  }

  void _goToPage(PageRequest page) {
    if (!mounted) {
      return;
    }
    setState(() {
      _page = page;
      _future = _load();
    });
  }

  void _setSearch(String value) {
    setState(() {
      _page = _page.first();
      _future = _load();
    });
  }

  void _setSort(SortRequest sort) {
    setState(() {
      _sort = sort;
      _page = _page.first();
      _future = _load();
    });
  }

  void _setProject(String projectId) {
    setState(() {
      _selectedProjectId = projectId;
      _page = _page.first();
      _future = _load();
    });
  }

  void _subscribe() {
    _realtime = RealtimeRefreshController(
      events: widget.apiClient.subscribeDomainEvents(),
      reload: () => _reload(),
      shouldReload: (event) =>
          event.aggregateType == 'Task' ||
          event.aggregateType == 'Worker' ||
          event.aggregateType == 'Project',
    );
  }

  @override
  Widget build(BuildContext context) {
    return PageScaffold(
      title: 'Board',
      icon: Icons.view_kanban,
      actions: [
        SegmentedButton<String>(
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
            ButtonSegment(
              value: 'ARCHIVED',
              icon: Icon(Icons.archive_outlined),
              label: Text('Archived'),
            ),
          ],
          selected: {_view},
          onSelectionChanged: (values) {
            setState(() {
              _view = values.first;
              _page = _page.first();
              _future = _load();
            });
          },
        ),
        IconButton(
          tooltip: 'Refresh board',
          onPressed: () => _reload(),
          icon: const Icon(Icons.refresh),
        ),
        FilledButton.icon(
          onPressed: () => _openTaskDialog(),
          icon: const Icon(Icons.add),
          label: const Text('New task'),
        ),
      ],
      child: Column(
        children: [
          SearchSortToolbar(
            keyPrefix: 'board',
            searchController: _searchController,
            sort: _sort,
            sortOptions: const [
              SortOption(field: 'CREATED_AT', label: 'Created'),
              SortOption(field: 'UPDATED_AT', label: 'Updated'),
              SortOption(field: 'TITLE', label: 'Title'),
              SortOption(field: 'STATUS', label: 'Status'),
              SortOption(field: 'START_DATE', label: 'Start date'),
              SortOption(field: 'END_DATE', label: 'End date'),
            ],
            onSearchChanged: _setSearch,
            onSortChanged: _setSort,
            extraControls: [
              _ProjectFilterDropdown(
                projects: _lastData?.projects ?? const <ProjectItem>[],
                selectedProjectId: _selectedProjectId,
                onChanged: _setProject,
              ),
            ],
          ),
          Expanded(
            child: FutureBuilder<BoardData>(
              future: _future,
              builder: (context, snapshot) {
                final data = snapshot.data ?? _lastData;
                if (snapshot.connectionState != ConnectionState.done &&
                    data == null) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snapshot.hasError && data == null) {
                  return ErrorView(
                    message: snapshot.error.toString(),
                    onRetry: () => _reload(),
                  );
                }
                final board = data ?? BoardData.empty();
                return Column(
                  children: [
                    Expanded(
                      child: Stack(
                        children: [
                          _BoardContent(
                            view: _view,
                            data: board,
                            onTaskSelected: _openTaskDetail,
                            onTaskEdit: _openTaskDialog,
                            onTaskDelete: _deleteTask,
                          ),
                          if (snapshot.connectionState != ConnectionState.done)
                            const Positioned(
                              left: 0,
                              right: 0,
                              top: 0,
                              child: LinearProgressIndicator(minHeight: 2),
                            ),
                        ],
                      ),
                    ),
                    PaginationBar(
                      page: _page,
                      totalCount: board.totalCount,
                      onPageChanged: _goToPage,
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

  Future<void> _openTaskDialog([TaskItem? task]) async {
    final data = _lastData;
    if (data == null) {
      return;
    }
    final saved = await showTaskFormDialog(
      context,
      apiClient: widget.apiClient,
      data: data,
      task: task,
    );
    if (saved == true) {
      _reload(firstPage: task == null);
    }
  }

  Future<void> _openTaskDetail(TaskItem task) async {
    if (task.id == null) {
      return;
    }
    final changed = await showTaskDetailDialog(
      context,
      apiClient: widget.apiClient,
      task: task,
      boardData: _lastData ?? BoardData.empty(),
    );
    if (changed == true) {
      _reload();
    }
  }

  Future<void> _deleteTask(TaskItem task) async {
    final taskId = task.id;
    if (taskId == null || taskId.isEmpty) {
      return;
    }
    final confirmed = await confirmAction(
      context,
      title: 'Delete task',
      message: 'Delete "${task.title}"?',
      confirmLabel: 'Delete',
    );
    if (!confirmed) {
      return;
    }
    await widget.apiClient.deleteTask(taskId);
    _reload();
  }
}

class _ProjectFilterDropdown extends StatelessWidget {
  const _ProjectFilterDropdown({
    required this.projects,
    required this.selectedProjectId,
    required this.onChanged,
  });

  final List<ProjectItem> projects;
  final String selectedProjectId;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    final value = projects.any((project) => project.id == selectedProjectId)
        ? selectedProjectId
        : '';
    return SizedBox(
      width: 240,
      height: 40,
      child: DropdownButtonFormField<String>(
        key: const ValueKey('board-project-filter'),
        isExpanded: true,
        value: value,
        decoration: const InputDecoration(labelText: 'Project'),
        items: [
          const DropdownMenuItem(
            value: '',
            child: Text('All projects', overflow: TextOverflow.ellipsis),
          ),
          ...projects.map(
            (project) => DropdownMenuItem(
              value: project.id,
              child: Text(project.name, overflow: TextOverflow.ellipsis),
            ),
          ),
        ],
        onChanged: (value) => onChanged(value ?? ''),
      ),
    );
  }
}

class _BoardContent extends StatelessWidget {
  const _BoardContent({
    required this.view,
    required this.data,
    required this.onTaskSelected,
    required this.onTaskEdit,
    required this.onTaskDelete,
  });

  final String view;
  final BoardData data;
  final ValueChanged<TaskItem> onTaskSelected;
  final ValueChanged<TaskItem> onTaskEdit;
  final ValueChanged<TaskItem> onTaskDelete;

  @override
  Widget build(BuildContext context) {
    if (data.tasks.isEmpty) {
      return const EmptyState(
        icon: Icons.view_kanban_outlined,
        title: 'No tasks yet',
        message: 'Create a task to start filling the board.',
      );
    }
    return switch (view) {
      'LIST' => _TaskListView(
        tasks: data.tasks,
        projects: data.projects,
        workers: data.workers,
        onTaskSelected: onTaskSelected,
        onTaskEdit: onTaskEdit,
      ),
      'ARCHIVED' => _TaskListView(
        tasks: data.tasks,
        projects: data.projects,
        workers: data.workers,
        onTaskSelected: onTaskSelected,
        onTaskEdit: onTaskEdit,
        onTaskDelete: onTaskDelete,
        showEdit: false,
        showDelete: true,
      ),
      'CALENDAR' => _CalendarView(
        tasks: data.tasks,
        onTaskSelected: onTaskSelected,
      ),
      _ => _KanbanView(
        columns: buildBoardStatusColumns(data.tasks),
        onTaskSelected: onTaskSelected,
        onTaskEdit: onTaskEdit,
      ),
    };
  }
}

class _KanbanView extends StatefulWidget {
  const _KanbanView({
    required this.columns,
    required this.onTaskSelected,
    required this.onTaskEdit,
  });

  final List<BoardColumnData> columns;
  final ValueChanged<TaskItem> onTaskSelected;
  final ValueChanged<TaskItem> onTaskEdit;

  @override
  State<_KanbanView> createState() => _KanbanViewState();
}

class _KanbanViewState extends State<_KanbanView> {
  final ScrollController _horizontalController = ScrollController();

  @override
  void dispose() {
    _horizontalController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    const pagePadding = 16.0;
    const columnGap = 12.0;
    const minColumnWidth = 260.0;

    return LayoutBuilder(
      builder: (context, constraints) {
        final columnCount = widget.columns.length;
        final totalGap = columnGap * (columnCount - 1);
        final viewportWidth = constraints.maxWidth.isFinite
            ? constraints.maxWidth
            : 0.0;
        final viewportHeight = constraints.maxHeight.isFinite
            ? constraints.maxHeight
            : 0.0;
        final availableRowWidth = viewportWidth - (pagePadding * 2);
        final availableRowHeight = viewportHeight - (pagePadding * 2);
        final expandedColumnWidth =
            (availableRowWidth - totalGap) / columnCount;
        final columnWidth = expandedColumnWidth < minColumnWidth
            ? minColumnWidth
            : expandedColumnWidth;
        final rowWidth = (columnWidth * columnCount) + totalGap;
        final rowHeight = availableRowHeight > 0 ? availableRowHeight : 0.0;

        return Scrollbar(
          controller: _horizontalController,
          thumbVisibility: true,
          child: SingleChildScrollView(
            controller: _horizontalController,
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.all(pagePadding),
            child: SizedBox(
              width: rowWidth,
              height: rowHeight,
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  for (
                    var index = 0;
                    index < widget.columns.length;
                    index++
                  ) ...[
                    SizedBox(
                      key: ValueKey(
                        'kanban-column-${widget.columns[index].id}',
                      ),
                      width: columnWidth,
                      child: _KanbanColumn(
                        column: widget.columns[index],
                        onTaskSelected: widget.onTaskSelected,
                        onTaskEdit: widget.onTaskEdit,
                      ),
                    ),
                    if (index < widget.columns.length - 1)
                      const SizedBox(width: columnGap),
                  ],
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}

class _KanbanColumn extends StatefulWidget {
  const _KanbanColumn({
    required this.column,
    required this.onTaskSelected,
    required this.onTaskEdit,
  });

  final BoardColumnData column;
  final ValueChanged<TaskItem> onTaskSelected;
  final ValueChanged<TaskItem> onTaskEdit;

  @override
  State<_KanbanColumn> createState() => _KanbanColumnState();
}

class _KanbanColumnState extends State<_KanbanColumn> {
  final ScrollController _verticalController = ScrollController();

  @override
  void dispose() {
    _verticalController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerLowest,
        border: Border.all(color: Theme.of(context).dividerColor),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Padding(
        padding: const EdgeInsets.all(10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    widget.column.title,
                    style: Theme.of(context).textTheme.titleSmall,
                  ),
                ),
                StatusPill(value: widget.column.tasks.length.toString()),
              ],
            ),
            const SizedBox(height: 10),
            if (widget.column.tasks.isEmpty)
              Expanded(
                child: Text(
                  'No tasks',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              )
            else
              Expanded(
                child: Scrollbar(
                  controller: _verticalController,
                  thumbVisibility: true,
                  child: ListView.separated(
                    controller: _verticalController,
                    primary: false,
                    padding: const EdgeInsets.only(right: 10),
                    itemBuilder: (context, index) {
                      final task = widget.column.tasks[index];
                      return _TaskCard(
                        task: task,
                        onTap: () => widget.onTaskSelected(task),
                        onEdit: () => widget.onTaskEdit(task),
                      );
                    },
                    separatorBuilder: (context, index) =>
                        const SizedBox(height: 8),
                    itemCount: widget.column.tasks.length,
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _TaskCard extends StatelessWidget {
  const _TaskCard({
    required this.task,
    required this.onTap,
    required this.onEdit,
  });

  final TaskItem task;
  final VoidCallback onTap;
  final VoidCallback onEdit;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Theme.of(context).colorScheme.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(8),
        side: BorderSide(color: Theme.of(context).dividerColor),
      ),
      child: InkWell(
        borderRadius: BorderRadius.circular(8),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(10),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(
                    child: Text(
                      task.title,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.titleSmall,
                    ),
                  ),
                  IconButton(
                    tooltip: 'Edit task',
                    iconSize: 18,
                    visualDensity: VisualDensity.compact,
                    onPressed: onEdit,
                    icon: const Icon(Icons.edit_outlined),
                  ),
                ],
              ),
              const SizedBox(height: 8),
              Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  StatusPill(value: task.status),
                  if (task.agentType.isNotEmpty)
                    StatusPill(value: task.agentType),
                ],
              ),
              const SizedBox(height: 8),
              _TaskCardDetail(
                icon: Icons.date_range,
                text: _taskDateRangeLabel(task),
              ),
              if ((task.workerId ?? '').isNotEmpty) ...[
                const SizedBox(height: 8),
                _TaskCardDetail(icon: Icons.memory, text: task.workerId!),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class _TaskCardDetail extends StatelessWidget {
  const _TaskCardDetail({required this.icon, required this.text});

  final IconData icon;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(icon, size: 16, color: Theme.of(context).colorScheme.outline),
        const SizedBox(width: 6),
        Expanded(
          child: Text(text, maxLines: 1, overflow: TextOverflow.ellipsis),
        ),
      ],
    );
  }
}

class _TaskListView extends StatelessWidget {
  const _TaskListView({
    required this.tasks,
    required this.projects,
    required this.workers,
    required this.onTaskSelected,
    required this.onTaskEdit,
    this.onTaskDelete,
    this.showEdit = true,
    this.showDelete = false,
  });

  final List<TaskItem> tasks;
  final List<ProjectItem> projects;
  final List<WorkerItem> workers;
  final ValueChanged<TaskItem> onTaskSelected;
  final ValueChanged<TaskItem> onTaskEdit;
  final ValueChanged<TaskItem>? onTaskDelete;
  final bool showEdit;
  final bool showDelete;

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: tasks.map((task) {
        final project = _projectName(projects, task.projectId);
        final worker = _workerName(workers, task.workerId);
        return Card(
          child: ListTile(
            leading: const Icon(Icons.task_alt),
            title: Text(task.title),
            subtitle: Text(
              '${project ?? task.projectId}  ${worker ?? task.workerId ?? 'Unassigned'}  ${_taskDateRangeLabel(task)}',
            ),
            trailing: Wrap(
              spacing: 8,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                StatusPill(value: task.status),
                if (showEdit)
                  IconButton(
                    tooltip: 'Edit task',
                    onPressed: () => onTaskEdit(task),
                    icon: const Icon(Icons.edit_outlined),
                  ),
                if (showDelete)
                  IconButton(
                    tooltip: 'Delete task',
                    onPressed: onTaskDelete == null
                        ? null
                        : () => onTaskDelete!(task),
                    icon: const Icon(Icons.delete_outline),
                  ),
              ],
            ),
            onTap: () => onTaskSelected(task),
          ),
        );
      }).toList(),
    );
  }
}

String? _projectName(List<ProjectItem> projects, String projectId) {
  for (final project in projects) {
    if (project.id == projectId) {
      return project.name;
    }
  }
  return null;
}

String? _workerName(List<WorkerItem> workers, String? workerId) {
  if (workerId == null) {
    return null;
  }
  for (final worker in workers) {
    if (worker.id == workerId) {
      return worker.name;
    }
  }
  return null;
}

WorkerItem? _workerById(List<WorkerItem> workers, String workerId) {
  for (final worker in workers) {
    if (worker.id == workerId) {
      return worker;
    }
  }
  return null;
}

List<Widget> _agentConfigDetailWidgets(TaskItem task) {
  final config = task.agentConfig;
  if (config.isEmpty) {
    return const [];
  }
  final widgets = <Widget>[];
  if (config.workMode.isNotEmpty) {
    widgets.add(
      DetailText(
        icon: Icons.rule_folder_outlined,
        text: _enumLabel(config.workMode),
      ),
    );
  }
  if (task.agentType == 'codex') {
    final codex = config.codex;
    if (codex.model.isNotEmpty) {
      widgets.add(
        DetailText(icon: Icons.smart_toy_outlined, text: codex.model),
      );
    }
    if (codex.reasoningEffort.isNotEmpty) {
      widgets.add(
        DetailText(
          icon: Icons.psychology_alt_outlined,
          text: _enumLabel(codex.reasoningEffort),
        ),
      );
    }
    if (codex.sandboxMode.isNotEmpty) {
      widgets.add(
        DetailText(
          icon: Icons.inventory_2_outlined,
          text: _enumLabel(codex.sandboxMode),
        ),
      );
    }
    if (codex.approvalPolicy.isNotEmpty) {
      widgets.add(
        DetailText(
          icon: Icons.verified_user_outlined,
          text: _enumLabel(codex.approvalPolicy),
        ),
      );
    }
    if (codex.fullAuto) {
      widgets.add(const DetailText(icon: Icons.auto_mode, text: 'Full auto'));
    }
    if (codex.bypassApprovalsAndSandbox) {
      widgets.add(
        const DetailText(
          icon: Icons.warning_amber_outlined,
          text: 'Bypass approvals and sandbox',
        ),
      );
    }
  } else if (task.agentType == 'claude') {
    final claude = config.claude;
    if (claude.model.isNotEmpty) {
      widgets.add(
        DetailText(icon: Icons.smart_toy_outlined, text: claude.model),
      );
    }
    if (claude.effort.isNotEmpty) {
      widgets.add(
        DetailText(
          icon: Icons.psychology_alt_outlined,
          text: _enumLabel(claude.effort),
        ),
      );
    }
    if (claude.permissionMode.isNotEmpty) {
      widgets.add(
        DetailText(
          icon: Icons.verified_user_outlined,
          text: _enumLabel(claude.permissionMode),
        ),
      );
    }
  }
  return widgets;
}

bool _workerAllowsProject(WorkerItem worker, String? projectId) {
  if (projectId == null) {
    return false;
  }
  return worker.projectBindingMode == 'ALL_PROJECTS' ||
      worker.boundProjectIds.contains(projectId);
}

bool _workerAvailableForProject(WorkerItem worker, String? projectId) {
  return worker.status == 'ONLINE' &&
      worker.supportedAgents.isNotEmpty &&
      _workerAllowsProject(worker, projectId);
}

String _agentLabel(String agent) => switch (agent) {
  'codex' => 'Codex',
  'claude' => 'Claude',
  _ => agent,
};

String _enumLabel(String value) {
  if (value.isEmpty) {
    return 'Default';
  }
  return value
      .split('_')
      .where((part) => part.isNotEmpty)
      .map(
        (part) =>
            part.substring(0, 1).toUpperCase() +
            part.substring(1).toLowerCase(),
      )
      .join(' ');
}

class _AgentConfigDraft {
  _AgentConfigDraft({required AgentExecutionConfigItem config})
    : workMode = config.workMode,
      codexModel = TextEditingController(text: config.codex.model),
      codexReasoningEffort = config.codex.reasoningEffort,
      codexSandboxMode = config.codex.sandboxMode,
      codexApprovalPolicy = config.codex.approvalPolicy,
      codexFullAuto = config.codex.fullAuto,
      codexBypassApprovalsAndSandbox = config.codex.bypassApprovalsAndSandbox,
      claudeModel = TextEditingController(text: config.claude.model),
      claudeEffort = config.claude.effort,
      claudePermissionMode = config.claude.permissionMode;

  factory _AgentConfigDraft.empty() =>
      _AgentConfigDraft(config: const AgentExecutionConfigItem());

  factory _AgentConfigDraft.fromTask(TaskItem? task) => _AgentConfigDraft(
    config: task?.agentConfig ?? const AgentExecutionConfigItem(),
  );

  String workMode;
  final TextEditingController codexModel;
  String codexReasoningEffort;
  String codexSandboxMode;
  String codexApprovalPolicy;
  bool codexFullAuto;
  bool codexBypassApprovalsAndSandbox;
  final TextEditingController claudeModel;
  String claudeEffort;
  String claudePermissionMode;

  AgentExecutionConfigItem toConfig(String agentType) =>
      AgentExecutionConfigItem(
        workMode: workMode,
        codex: agentType == 'codex'
            ? CodexExecutionConfigItem(
                model: codexModel.text.trim(),
                reasoningEffort: codexReasoningEffort,
                sandboxMode: codexSandboxMode,
                approvalPolicy: codexApprovalPolicy,
                fullAuto: codexFullAuto,
                bypassApprovalsAndSandbox: codexBypassApprovalsAndSandbox,
              )
            : const CodexExecutionConfigItem(),
        claude: agentType == 'claude'
            ? ClaudeExecutionConfigItem(
                model: claudeModel.text.trim(),
                effort: claudeEffort,
                permissionMode: claudePermissionMode,
              )
            : const ClaudeExecutionConfigItem(),
      );

  void dispose() {
    codexModel.dispose();
    claudeModel.dispose();
  }
}

class _AgentConfigFields extends StatelessWidget {
  const _AgentConfigFields({
    required this.draft,
    required this.agentType,
    required this.onChanged,
  });

  final _AgentConfigDraft draft;
  final String agentType;
  final VoidCallback onChanged;

  @override
  Widget build(BuildContext context) {
    final children = <Widget>[
      _AgentConfigDropdown(
        label: 'Work mode',
        value: draft.workMode,
        options: const {
          '': 'Default',
          'PLAN': 'Plan',
          'IMPLEMENT': 'Implement',
          'REVIEW': 'Review',
        },
        onChanged: (value) {
          draft.workMode = value;
          onChanged();
        },
      ),
      if (agentType == 'codex') ...[
        TextField(
          controller: draft.codexModel,
          decoration: const InputDecoration(labelText: 'Codex model'),
          onChanged: (_) => onChanged(),
        ),
        _AgentConfigDropdown(
          label: 'Reasoning effort',
          value: draft.codexReasoningEffort,
          options: const {
            '': 'Default',
            'MINIMAL': 'Minimal',
            'LOW': 'Low',
            'MEDIUM': 'Medium',
            'HIGH': 'High',
            'XHIGH': 'XHigh',
          },
          onChanged: (value) {
            draft.codexReasoningEffort = value;
            onChanged();
          },
        ),
        _AgentConfigDropdown(
          label: 'Sandbox',
          value: draft.codexSandboxMode,
          options: const {
            '': 'Default',
            'READ_ONLY': 'Read only',
            'WORKSPACE_WRITE': 'Workspace write',
            'DANGER_FULL_ACCESS': 'Danger full access',
          },
          onChanged: (value) {
            draft.codexSandboxMode = value;
            onChanged();
          },
        ),
        _AgentConfigDropdown(
          label: 'Approval',
          value: draft.codexApprovalPolicy,
          options: const {
            '': 'Default',
            'UNTRUSTED': 'Untrusted',
            'ON_FAILURE': 'On failure',
            'ON_REQUEST': 'On request',
            'NEVER': 'Never',
          },
          onChanged: (value) {
            draft.codexApprovalPolicy = value;
            onChanged();
          },
        ),
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          title: const Text('Full auto'),
          value: draft.codexFullAuto,
          onChanged: (value) {
            draft.codexFullAuto = value;
            onChanged();
          },
        ),
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          title: const Text('Bypass approvals and sandbox'),
          value: draft.codexBypassApprovalsAndSandbox,
          onChanged: (value) {
            draft.codexBypassApprovalsAndSandbox = value;
            onChanged();
          },
        ),
      ] else if (agentType == 'claude') ...[
        TextField(
          controller: draft.claudeModel,
          decoration: const InputDecoration(labelText: 'Claude model'),
          onChanged: (_) => onChanged(),
        ),
        _AgentConfigDropdown(
          label: 'Effort',
          value: draft.claudeEffort,
          options: const {
            '': 'Default',
            'LOW': 'Low',
            'MEDIUM': 'Medium',
            'HIGH': 'High',
            'XHIGH': 'XHigh',
            'MAX': 'Max',
          },
          onChanged: (value) {
            draft.claudeEffort = value;
            onChanged();
          },
        ),
        _AgentConfigDropdown(
          label: 'Permission mode',
          value: draft.claudePermissionMode,
          options: const {
            '': 'Default',
            'ACCEPT_EDITS': 'Accept edits',
            'AUTO': 'Auto',
            'BYPASS_PERMISSIONS': 'Bypass permissions',
            'DEFAULT': 'Default',
            'DONT_ASK': "Don't ask",
            'PLAN': 'Plan',
          },
          onChanged: (value) {
            draft.claudePermissionMode = value;
            onChanged();
          },
        ),
      ],
    ];

    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border.all(color: Theme.of(context).dividerColor),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'Agent CLI settings',
              style: Theme.of(context).textTheme.titleSmall,
            ),
            const SizedBox(height: 12),
            Wrap(
              spacing: 12,
              runSpacing: 12,
              children: children
                  .map(
                    (child) => SizedBox(
                      width: child is SwitchListTile ? 300 : 210,
                      child: child,
                    ),
                  )
                  .toList(),
            ),
          ],
        ),
      ),
    );
  }
}

class _AgentConfigDropdown extends StatelessWidget {
  const _AgentConfigDropdown({
    required this.label,
    required this.value,
    required this.options,
    required this.onChanged,
  });

  final String label;
  final String value;
  final Map<String, String> options;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    return DropdownButtonFormField<String>(
      isExpanded: true,
      value: options.containsKey(value) ? value : '',
      decoration: InputDecoration(labelText: label),
      items: options.entries
          .map(
            (entry) => DropdownMenuItem(
              value: entry.key,
              child: Text(entry.value, overflow: TextOverflow.ellipsis),
            ),
          )
          .toList(),
      onChanged: (value) => onChanged(value ?? ''),
    );
  }
}

class _AssignWorkerSelection {
  const _AssignWorkerSelection({
    required this.workerId,
    required this.agentType,
    required this.agentConfig,
  });

  final String workerId;
  final String agentType;
  final AgentExecutionConfigItem agentConfig;
}

DateTime _todayTaskDate() {
  final now = DateTime.now();
  return DateTime(now.year, now.month, now.day);
}

DateTime _taskDateFromIso(String value, DateTime fallback) {
  final parsed = DateTime.tryParse(value);
  if (parsed == null) {
    return fallback;
  }
  final utc = parsed.toUtc();
  return DateTime(utc.year, utc.month, utc.day);
}

String _taskDateLabel(DateTime date) {
  final year = date.year.toString().padLeft(4, '0');
  final month = date.month.toString().padLeft(2, '0');
  final day = date.day.toString().padLeft(2, '0');
  return '$year-$month-$day';
}

String _taskDateIso(DateTime date) =>
    DateTime.utc(date.year, date.month, date.day).toIso8601String();

String _taskDateRangeLabel(TaskItem task) {
  final fallback = _taskDateFromIso(task.createdAt, _todayTaskDate());
  final startDate = _taskDateFromIso(task.startDate, fallback);
  var endDate = _taskDateFromIso(task.endDate, startDate);
  if (endDate.isBefore(startDate)) {
    endDate = startDate;
  }
  if (endDate == startDate) {
    return _taskDateLabel(startDate);
  }
  return '${_taskDateLabel(startDate)} - ${_taskDateLabel(endDate)}';
}

enum _CalendarMode {
  day('Day', Icons.view_day),
  week('Week', Icons.view_week),
  month('Month', Icons.calendar_month),
  year('Year', Icons.calendar_today);

  const _CalendarMode(this.label, this.icon);

  final String label;
  final IconData icon;
}

class _CalendarTaskRange {
  const _CalendarTaskRange({
    required this.task,
    required this.start,
    required this.end,
  });

  final TaskItem task;
  final DateTime start;
  final DateTime end;

  bool contains(DateTime date) {
    final day = _dateOnly(date);
    return !day.isBefore(start) && !day.isAfter(end);
  }

  bool overlaps(DateTime rangeStart, DateTime rangeEnd) {
    return !end.isBefore(rangeStart) && !start.isAfter(rangeEnd);
  }
}

class _CalendarView extends StatefulWidget {
  const _CalendarView({required this.tasks, required this.onTaskSelected});

  final List<TaskItem> tasks;
  final ValueChanged<TaskItem> onTaskSelected;

  @override
  State<_CalendarView> createState() => _CalendarViewState();
}

class _CalendarViewState extends State<_CalendarView> {
  _CalendarMode _mode = _CalendarMode.month;
  late DateTime _focusedDate;

  @override
  void initState() {
    super.initState();
    final ranges = _taskRanges(widget.tasks);
    _focusedDate = ranges.isEmpty ? _todayTaskDate() : ranges.first.start;
  }

  @override
  void didUpdateWidget(covariant _CalendarView oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.tasks != widget.tasks && widget.tasks.isNotEmpty) {
      final ranges = _taskRanges(widget.tasks);
      if (ranges.isNotEmpty) {
        _focusedDate = ranges.first.start;
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final ranges = _visibleRanges();
    return Column(
      children: [
        _CalendarToolbar(
          mode: _mode,
          title: _calendarTitle(),
          onModeChanged: (mode) => setState(() => _mode = mode),
          onPrevious: () => _moveFocus(-1),
          onToday: () => setState(() => _focusedDate = _todayTaskDate()),
          onNext: () => _moveFocus(1),
        ),
        Expanded(
          child: ranges.isEmpty
              ? EmptyState(
                  icon: Icons.search_off,
                  title: 'No scheduled tasks',
                  message: 'Create a task with dates to fill the calendar.',
                )
              : switch (_mode) {
                  _CalendarMode.day => _CalendarDayList(
                    date: _focusedDate,
                    ranges: ranges,
                    onTaskSelected: widget.onTaskSelected,
                  ),
                  _CalendarMode.week => _CalendarWeekGrid(
                    weekStart: _startOfWeek(_focusedDate),
                    ranges: ranges,
                    onTaskSelected: widget.onTaskSelected,
                  ),
                  _CalendarMode.month => _CalendarMonthGrid(
                    month: _focusedDate,
                    ranges: ranges,
                    onTaskSelected: widget.onTaskSelected,
                  ),
                  _CalendarMode.year => _CalendarYearGrid(
                    year: _focusedDate.year,
                    ranges: ranges,
                    onTaskSelected: widget.onTaskSelected,
                    onMonthSelected: (month) => setState(() {
                      _mode = _CalendarMode.month;
                      _focusedDate = month;
                    }),
                  ),
                },
        ),
      ],
    );
  }

  List<_CalendarTaskRange> _visibleRanges() {
    return _taskRanges(widget.tasks);
  }

  void _moveFocus(int delta) {
    setState(() {
      _focusedDate = switch (_mode) {
        _CalendarMode.day => _dateOnly(_focusedDate.add(Duration(days: delta))),
        _CalendarMode.week => _dateOnly(
          _focusedDate.add(Duration(days: delta * 7)),
        ),
        _CalendarMode.month => _addMonths(_focusedDate, delta),
        _CalendarMode.year => DateTime(
          _focusedDate.year + delta,
          _focusedDate.month,
          _focusedDate.day,
        ),
      };
    });
  }

  String _calendarTitle() {
    return switch (_mode) {
      _CalendarMode.day => _formatDayTitle(_focusedDate),
      _CalendarMode.week => _formatWeekTitle(_focusedDate),
      _CalendarMode.month =>
        '${_monthName(_focusedDate.month)} ${_focusedDate.year}',
      _CalendarMode.year => _focusedDate.year.toString(),
    };
  }
}

class _CalendarToolbar extends StatelessWidget {
  const _CalendarToolbar({
    required this.mode,
    required this.title,
    required this.onModeChanged,
    required this.onPrevious,
    required this.onToday,
    required this.onNext,
  });

  final _CalendarMode mode;
  final String title;
  final ValueChanged<_CalendarMode> onModeChanged;
  final VoidCallback onPrevious;
  final VoidCallback onToday;
  final VoidCallback onNext;

  @override
  Widget build(BuildContext context) {
    final controls = <Widget>[
      SegmentedButton<_CalendarMode>(
        key: const ValueKey('calendar-mode-switcher'),
        showSelectedIcon: false,
        segments: _CalendarMode.values
            .map(
              (mode) => ButtonSegment<_CalendarMode>(
                value: mode,
                icon: Icon(mode.icon, size: 18),
                label: Text(mode.label),
              ),
            )
            .toList(),
        selected: {mode},
        onSelectionChanged: (values) => onModeChanged(values.first),
      ),
      Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            tooltip: 'Previous period',
            onPressed: onPrevious,
            icon: const Icon(Icons.chevron_left),
          ),
          OutlinedButton(onPressed: onToday, child: const Text('Today')),
          IconButton(
            tooltip: 'Next period',
            onPressed: onNext,
            icon: const Icon(Icons.chevron_right),
          ),
        ],
      ),
    ];

    return Container(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surface,
        border: Border(
          bottom: BorderSide(color: Theme.of(context).dividerColor),
        ),
      ),
      child: LayoutBuilder(
        builder: (context, constraints) {
          final titleWidget = Text(
            title,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(
              context,
            ).textTheme.headlineSmall?.copyWith(fontWeight: FontWeight.w700),
          );
          if (constraints.maxWidth < 900) {
            return Wrap(
              spacing: 12,
              runSpacing: 10,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                SizedBox(width: constraints.maxWidth, child: titleWidget),
                ...controls,
              ],
            );
          }
          return Row(
            children: [
              Expanded(child: titleWidget),
              ..._spacedCalendarControls(controls),
            ],
          );
        },
      ),
    );
  }
}

class _CalendarMonthGrid extends StatelessWidget {
  const _CalendarMonthGrid({
    required this.month,
    required this.ranges,
    required this.onTaskSelected,
  });

  final DateTime month;
  final List<_CalendarTaskRange> ranges;
  final ValueChanged<TaskItem> onTaskSelected;

  @override
  Widget build(BuildContext context) {
    final firstDay = DateTime(month.year, month.month);
    final lastDay = DateTime(month.year, month.month + 1, 0);
    final gridStart = _startOfWeek(firstDay);
    final gridEnd = _startOfWeek(lastDay).add(const Duration(days: 6));
    final weekStarts = <DateTime>[];
    for (
      var date = gridStart;
      !date.isAfter(gridEnd);
      date = date.add(const Duration(days: 7))
    ) {
      weekStarts.add(date);
    }

    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        children: [
          const _CalendarWeekdayHeader(),
          Expanded(
            child: Column(
              children: [
                for (final weekStart in weekStarts)
                  Expanded(
                    child: _CalendarWeekRow(
                      weekStart: weekStart,
                      primaryMonth: month.month,
                      ranges: ranges,
                      onTaskSelected: onTaskSelected,
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _CalendarWeekGrid extends StatelessWidget {
  const _CalendarWeekGrid({
    required this.weekStart,
    required this.ranges,
    required this.onTaskSelected,
  });

  final DateTime weekStart;
  final List<_CalendarTaskRange> ranges;
  final ValueChanged<TaskItem> onTaskSelected;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        children: [
          const _CalendarWeekdayHeader(),
          Expanded(
            child: _CalendarWeekRow(
              weekStart: weekStart,
              primaryMonth: 0,
              ranges: ranges,
              onTaskSelected: onTaskSelected,
              showFullDate: true,
            ),
          ),
        ],
      ),
    );
  }
}

class _CalendarWeekdayHeader extends StatelessWidget {
  const _CalendarWeekdayHeader();

  @override
  Widget build(BuildContext context) {
    const labels = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
    return Container(
      height: 32,
      decoration: BoxDecoration(
        border: Border(
          bottom: BorderSide(color: Theme.of(context).dividerColor),
        ),
      ),
      child: Row(
        children: [
          for (final label in labels)
            Expanded(
              child: Center(
                child: Text(
                  label,
                  style: Theme.of(context).textTheme.labelLarge?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _CalendarWeekRow extends StatelessWidget {
  const _CalendarWeekRow({
    required this.weekStart,
    required this.primaryMonth,
    required this.ranges,
    required this.onTaskSelected,
    this.showFullDate = false,
  });

  final DateTime weekStart;
  final int primaryMonth;
  final List<_CalendarTaskRange> ranges;
  final ValueChanged<TaskItem> onTaskSelected;
  final bool showFullDate;

  @override
  Widget build(BuildContext context) {
    final weekEnd = weekStart.add(const Duration(days: 6));
    final weekRanges = ranges
        .where((range) => range.overlaps(weekStart, weekEnd))
        .toList();

    return LayoutBuilder(
      builder: (context, constraints) {
        final width = constraints.maxWidth.isFinite
            ? constraints.maxWidth
            : 0.0;
        final height = constraints.maxHeight.isFinite
            ? constraints.maxHeight
            : 150.0;
        final cellWidth = width / 7;
        final availableSlots = math.max(1, ((height - 38) / 22).floor());
        final visibleRanges = weekRanges.take(availableSlots).toList();
        final overflowCount = weekRanges.length - visibleRanges.length;

        return DecoratedBox(
          decoration: BoxDecoration(
            color: Theme.of(context).colorScheme.surface,
            border: Border(
              bottom: BorderSide(color: Theme.of(context).dividerColor),
            ),
          ),
          child: Stack(
            clipBehavior: Clip.hardEdge,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  for (var index = 0; index < 7; index++)
                    Expanded(
                      child: _CalendarDateCell(
                        date: weekStart.add(Duration(days: index)),
                        inPrimaryMonth:
                            primaryMonth == 0 ||
                            weekStart.add(Duration(days: index)).month ==
                                primaryMonth,
                        showFullDate: showFullDate,
                      ),
                    ),
                ],
              ),
              for (var index = 0; index < visibleRanges.length; index++)
                _positionedTaskBar(
                  context,
                  range: visibleRanges[index],
                  index: index,
                  cellWidth: cellWidth,
                ),
              if (overflowCount > 0)
                Positioned(
                  right: 8,
                  bottom: 6,
                  child: Text(
                    '+$overflowCount more',
                    style: Theme.of(context).textTheme.labelSmall?.copyWith(
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                  ),
                ),
            ],
          ),
        );
      },
    );
  }

  Widget _positionedTaskBar(
    BuildContext context, {
    required _CalendarTaskRange range,
    required int index,
    required double cellWidth,
  }) {
    final segmentStart = range.start.isBefore(weekStart)
        ? weekStart
        : range.start;
    final weekEnd = weekStart.add(const Duration(days: 6));
    final segmentEnd = range.end.isAfter(weekEnd) ? weekEnd : range.end;
    final dayOffset = segmentStart.difference(weekStart).inDays;
    final spanDays = segmentEnd.difference(segmentStart).inDays + 1;
    return Positioned(
      left: (dayOffset * cellWidth) + 4,
      top: 34 + (index * 22),
      width: math.max(40, (spanDays * cellWidth) - 8),
      height: 18,
      child: _CalendarTaskBar(
        task: range.task,
        color: _calendarStatusColor(context, range.task.status),
        onTap: () => onTaskSelected(range.task),
      ),
    );
  }
}

class _CalendarDateCell extends StatelessWidget {
  const _CalendarDateCell({
    required this.date,
    required this.inPrimaryMonth,
    required this.showFullDate,
  });

  final DateTime date;
  final bool inPrimaryMonth;
  final bool showFullDate;

  @override
  Widget build(BuildContext context) {
    final isToday = _sameDate(date, _todayTaskDate());
    final scheme = Theme.of(context).colorScheme;
    final labelColor = inPrimaryMonth
        ? scheme.onSurface
        : scheme.onSurfaceVariant.withOpacity(0.55);
    return Container(
      decoration: BoxDecoration(
        color: inPrimaryMonth
            ? scheme.surface
            : scheme.surfaceContainerHighest.withOpacity(0.42),
        border: Border(
          right: BorderSide(color: Theme.of(context).dividerColor),
        ),
      ),
      padding: const EdgeInsets.fromLTRB(8, 6, 8, 4),
      child: Align(
        alignment: Alignment.topRight,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 120),
          width: isToday ? 28 : null,
          height: isToday ? 28 : null,
          alignment: Alignment.center,
          decoration: isToday
              ? BoxDecoration(color: scheme.error, shape: BoxShape.circle)
              : null,
          child: Text(
            showFullDate ? _shortDateLabel(date) : date.day.toString(),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(context).textTheme.labelMedium?.copyWith(
              color: isToday ? scheme.onError : labelColor,
              fontWeight: isToday ? FontWeight.w700 : FontWeight.w500,
            ),
          ),
        ),
      ),
    );
  }
}

class _CalendarTaskBar extends StatelessWidget {
  const _CalendarTaskBar({
    required this.task,
    required this.color,
    required this.onTap,
  });

  final TaskItem task;
  final Color color;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      label: task.title,
      child: Material(
        color: color.withOpacity(0.18),
        borderRadius: BorderRadius.circular(4),
        child: InkWell(
          borderRadius: BorderRadius.circular(4),
          onTap: onTap,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            alignment: Alignment.centerLeft,
            decoration: BoxDecoration(
              border: Border(left: BorderSide(color: color, width: 3)),
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              task.title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: Theme.of(context).textTheme.labelSmall?.copyWith(
                color: color,
                fontWeight: FontWeight.w700,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _CalendarDayList extends StatelessWidget {
  const _CalendarDayList({
    required this.date,
    required this.ranges,
    required this.onTaskSelected,
  });

  final DateTime date;
  final List<_CalendarTaskRange> ranges;
  final ValueChanged<TaskItem> onTaskSelected;

  @override
  Widget build(BuildContext context) {
    final dayRanges = ranges.where((range) => range.contains(date)).toList();
    if (dayRanges.isEmpty) {
      return EmptyState(
        icon: Icons.event_available,
        title: 'No tasks on ${_taskDateLabel(date)}',
      );
    }
    return ListView.separated(
      padding: const EdgeInsets.all(16),
      itemBuilder: (context, index) {
        final range = dayRanges[index];
        final color = _calendarStatusColor(context, range.task.status);
        return Card(
          child: ListTile(
            leading: Icon(Icons.event, color: color),
            title: Text(range.task.title),
            subtitle: Text(_taskDateRangeLabel(range.task)),
            trailing: StatusPill(value: range.task.status),
            onTap: () => onTaskSelected(range.task),
          ),
        );
      },
      separatorBuilder: (context, index) => const SizedBox(height: 8),
      itemCount: dayRanges.length,
    );
  }
}

class _CalendarYearGrid extends StatelessWidget {
  const _CalendarYearGrid({
    required this.year,
    required this.ranges,
    required this.onTaskSelected,
    required this.onMonthSelected,
  });

  final int year;
  final List<_CalendarTaskRange> ranges;
  final ValueChanged<TaskItem> onTaskSelected;
  final ValueChanged<DateTime> onMonthSelected;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final width = constraints.maxWidth;
        final columns = width >= 1120
            ? 4
            : width >= 820
            ? 3
            : width >= 560
            ? 2
            : 1;
        return GridView.builder(
          padding: const EdgeInsets.all(16),
          gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
            crossAxisCount: columns,
            mainAxisExtent: 248,
            crossAxisSpacing: 12,
            mainAxisSpacing: 12,
          ),
          itemCount: 12,
          itemBuilder: (context, index) {
            final month = DateTime(year, index + 1);
            return _YearMonthCard(
              month: month,
              ranges: ranges,
              onTap: () => onMonthSelected(month),
              onTaskSelected: onTaskSelected,
            );
          },
        );
      },
    );
  }
}

class _YearMonthCard extends StatelessWidget {
  const _YearMonthCard({
    required this.month,
    required this.ranges,
    required this.onTap,
    required this.onTaskSelected,
  });

  final DateTime month;
  final List<_CalendarTaskRange> ranges;
  final VoidCallback onTap;
  final ValueChanged<TaskItem> onTaskSelected;

  @override
  Widget build(BuildContext context) {
    final monthStart = DateTime(month.year, month.month);
    final monthEnd = DateTime(month.year, month.month + 1, 0);
    final monthRanges = ranges
        .where((range) => range.overlaps(monthStart, monthEnd))
        .toList();
    final busyDays = <int>{};
    for (final range in monthRanges) {
      final start = range.start.isBefore(monthStart) ? monthStart : range.start;
      final end = range.end.isAfter(monthEnd) ? monthEnd : range.end;
      for (
        var date = start;
        !date.isAfter(end);
        date = date.add(const Duration(days: 1))
      ) {
        busyDays.add(date.day);
      }
    }
    final firstVisible = _startOfWeek(monthStart);
    final visibleDates = List.generate(
      42,
      (index) => firstVisible.add(Duration(days: index)),
    );
    final color = monthRanges.isEmpty
        ? Theme.of(context).colorScheme.outline
        : _calendarStatusColor(context, monthRanges.first.task.status);

    return Material(
      color: Theme.of(context).colorScheme.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(8),
        side: BorderSide(color: Theme.of(context).dividerColor),
      ),
      child: InkWell(
        borderRadius: BorderRadius.circular(8),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      _monthName(month.month),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.titleSmall,
                    ),
                  ),
                  StatusPill(
                    value:
                        '${monthRanges.length} ${monthRanges.length == 1 ? 'task' : 'tasks'}',
                  ),
                ],
              ),
              const SizedBox(height: 10),
              const _MiniWeekdayHeader(),
              const SizedBox(height: 6),
              Expanded(
                child: GridView.builder(
                  physics: const NeverScrollableScrollPhysics(),
                  gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                    crossAxisCount: 7,
                    mainAxisSpacing: 2,
                    crossAxisSpacing: 2,
                  ),
                  itemCount: visibleDates.length,
                  itemBuilder: (context, index) {
                    final date = visibleDates[index];
                    final inMonth = date.month == month.month;
                    final busy = inMonth && busyDays.contains(date.day);
                    final today = _sameDate(date, _todayTaskDate());
                    return Container(
                      alignment: Alignment.center,
                      decoration: BoxDecoration(
                        color: busy
                            ? color.withOpacity(0.14)
                            : today
                            ? Theme.of(
                                context,
                              ).colorScheme.error.withOpacity(0.12)
                            : null,
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: Text(
                        inMonth ? date.day.toString() : '',
                        style: Theme.of(context).textTheme.labelSmall?.copyWith(
                          color: busy
                              ? color
                              : Theme.of(context).colorScheme.onSurfaceVariant,
                          fontWeight: busy || today
                              ? FontWeight.w700
                              : FontWeight.w400,
                        ),
                      ),
                    );
                  },
                ),
              ),
              if (monthRanges.isNotEmpty) ...[
                const SizedBox(height: 8),
                _CalendarTaskBar(
                  task: monthRanges.first.task,
                  color: _calendarStatusColor(
                    context,
                    monthRanges.first.task.status,
                  ),
                  onTap: () => onTaskSelected(monthRanges.first.task),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class _MiniWeekdayHeader extends StatelessWidget {
  const _MiniWeekdayHeader();

  @override
  Widget build(BuildContext context) {
    const labels = ['M', 'T', 'W', 'T', 'F', 'S', 'S'];
    return Row(
      children: [
        for (final label in labels)
          Expanded(
            child: Center(
              child: Text(
                label,
                style: Theme.of(context).textTheme.labelSmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          ),
      ],
    );
  }
}

List<_CalendarTaskRange> _taskRanges(List<TaskItem> tasks) {
  final out = <_CalendarTaskRange>[];
  for (final task in tasks) {
    final fallback = _taskDateFromIso(task.createdAt, _todayTaskDate());
    final start = _taskDateFromIso(task.startDate, fallback);
    var end = _taskDateFromIso(task.endDate, start);
    if (end.isBefore(start)) {
      end = start;
    }
    out.add(_CalendarTaskRange(task: task, start: start, end: end));
  }
  out.sort((a, b) {
    final startCompare = a.start.compareTo(b.start);
    if (startCompare != 0) {
      return startCompare;
    }
    final durationCompare = b.end.compareTo(a.end);
    if (durationCompare != 0) {
      return durationCompare;
    }
    return a.task.title.compareTo(b.task.title);
  });
  return out;
}

List<Widget> _spacedCalendarControls(List<Widget> controls) {
  final out = <Widget>[];
  for (var index = 0; index < controls.length; index++) {
    if (index > 0) {
      out.add(const SizedBox(width: 12));
    }
    out.add(controls[index]);
  }
  return out;
}

DateTime _dateOnly(DateTime date) => DateTime(date.year, date.month, date.day);

DateTime _startOfWeek(DateTime date) {
  final day = _dateOnly(date);
  return day.subtract(Duration(days: day.weekday - DateTime.monday));
}

DateTime _addMonths(DateTime date, int months) {
  final target = DateTime(date.year, date.month + months);
  final lastDay = DateTime(target.year, target.month + 1, 0).day;
  return DateTime(target.year, target.month, math.min(date.day, lastDay));
}

bool _sameDate(DateTime a, DateTime b) =>
    a.year == b.year && a.month == b.month && a.day == b.day;

String _calendarDateLabel(DateTime date) {
  return '${_shortMonthName(date.month)} ${date.day}, ${date.year}';
}

String _formatDayTitle(DateTime date) => _calendarDateLabel(date);

String _formatWeekTitle(DateTime date) {
  final start = _startOfWeek(date);
  final end = start.add(const Duration(days: 6));
  if (start.year == end.year && start.month == end.month) {
    return '${_shortMonthName(start.month)} ${start.day} - ${end.day}, ${start.year}';
  }
  return '${_calendarDateLabel(start)} - ${_calendarDateLabel(end)}';
}

String _shortDateLabel(DateTime date) =>
    '${_shortMonthName(date.month)} ${date.day}';

String _monthName(int month) => const [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
][month - 1];

String _shortMonthName(int month) => const [
  'Jan',
  'Feb',
  'Mar',
  'Apr',
  'May',
  'Jun',
  'Jul',
  'Aug',
  'Sep',
  'Oct',
  'Nov',
  'Dec',
][month - 1];

Color _calendarStatusColor(BuildContext context, String status) {
  final scheme = Theme.of(context).colorScheme;
  return switch (status) {
    'ONLINE' || 'COMPLETED' || 'RUNNING' => const Color(0xff13795b),
    'CREATED' || 'REGISTERED' || 'ASSIGNED' => const Color(0xff476a99),
    'STARTING' || 'WAITING_INPUT' || 'INTERRUPTING' => const Color(0xff9a6700),
    'FAILED' || 'ERROR' => scheme.error,
    'ARCHIVED' ||
    'DISABLED' ||
    'OFFLINE' ||
    'INTERRUPTED' => scheme.onSurfaceVariant,
    _ => scheme.primary,
  };
}

Future<bool?> showTaskFormDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required BoardData data,
  TaskItem? task,
}) {
  final title = TextEditingController(text: task?.title ?? '');
  final description = TextEditingController(text: task?.description ?? '');
  final baseBranch = TextEditingController(text: task?.baseBranch ?? 'main');
  final preCommands = TextEditingController(
    text: task?.preCommands.join('\n') ?? '',
  );
  final postCommands = TextEditingController(
    text: task?.postCommands.join('\n') ?? '',
  );
  DateTime startDate = _taskDateFromIso(
    task?.startDate ?? '',
    _todayTaskDate(),
  );
  DateTime endDate = _taskDateFromIso(task?.endDate ?? '', startDate);
  if (endDate.isBefore(startDate)) {
    endDate = startDate;
  }
  String? projectId =
      task?.projectId ??
      (data.projects.isNotEmpty ? data.projects.first.id : null);
  String selectedWorkerId = task?.workerId ?? '';
  String? selectedAgent = (task?.agentType ?? '').isEmpty
      ? null
      : task!.agentType;
  final configDraft = _AgentConfigDraft.fromTask(task);

  List<WorkerItem> availableWorkers() => data.workers
      .where((worker) => _workerAvailableForProject(worker, projectId))
      .toList();

  void reconcileCreateSelection() {
    if (task != null) {
      return;
    }
    final workers = availableWorkers();
    if (selectedWorkerId.isNotEmpty &&
        !workers.any((worker) => worker.id == selectedWorkerId)) {
      selectedWorkerId = '';
      selectedAgent = null;
    }
    final worker = selectedWorkerId.isEmpty
        ? null
        : _workerById(workers, selectedWorkerId);
    if (worker == null) {
      selectedAgent = null;
      return;
    }
    if (selectedAgent == null ||
        !worker.supportedAgents.contains(selectedAgent)) {
      selectedAgent = worker.supportedAgents.first;
    }
  }

  reconcileCreateSelection();
  return showDialog<bool>(
    context: context,
    builder: (context) => StatefulBuilder(
      builder: (context, setState) {
        reconcileCreateSelection();
        final workers = availableWorkers();
        final selectedWorker = selectedWorkerId.isEmpty
            ? null
            : _workerById(workers, selectedWorkerId);
        final supportedAgents =
            selectedWorker?.supportedAgents ?? const <String>[];
        final canSave =
            title.text.trim().isNotEmpty &&
            projectId != null &&
            !endDate.isBefore(startDate) &&
            (selectedWorkerId.isEmpty || selectedAgent != null);

        return AlertDialog(
          title: Text(task == null ? 'Create task' : 'Edit task'),
          content: SizedBox(
            width: 680,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextField(
                    controller: title,
                    decoration: const InputDecoration(labelText: 'Title'),
                    onChanged: (_) => setState(() {}),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: description,
                    decoration: const InputDecoration(labelText: 'Description'),
                    minLines: 2,
                    maxLines: 4,
                  ),
                  const SizedBox(height: 12),
                  DropdownButtonFormField<String>(
                    value: projectId,
                    decoration: const InputDecoration(labelText: 'Project'),
                    items: data.projects
                        .map(
                          (project) => DropdownMenuItem(
                            value: project.id,
                            child: Text(project.name),
                          ),
                        )
                        .toList(),
                    onChanged: (value) => setState(() {
                      projectId = value;
                      reconcileCreateSelection();
                    }),
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      Expanded(
                        child: _TaskDateField(
                          label: 'Start date',
                          value: startDate,
                          onChanged: (value) => setState(() {
                            startDate = value;
                            if (endDate.isBefore(startDate)) {
                              endDate = startDate;
                            }
                          }),
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: _TaskDateField(
                          label: 'End date',
                          value: endDate,
                          onChanged: (value) => setState(() {
                            endDate = value;
                          }),
                        ),
                      ),
                    ],
                  ),
                  if (endDate.isBefore(startDate)) ...[
                    const SizedBox(height: 8),
                    Align(
                      alignment: Alignment.centerLeft,
                      child: Text(
                        'End date must be on or after start date.',
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                    ),
                  ],
                  const SizedBox(height: 12),
                  TextField(
                    controller: baseBranch,
                    decoration: const InputDecoration(labelText: 'Base branch'),
                  ),
                  if (task == null) ...[
                    const SizedBox(height: 12),
                    DropdownButtonFormField<String>(
                      value: selectedWorkerId,
                      decoration: const InputDecoration(labelText: 'Worker'),
                      items: [
                        const DropdownMenuItem(
                          value: '',
                          child: Text('Unassigned'),
                        ),
                        ...workers.map(
                          (worker) => DropdownMenuItem(
                            value: worker.id,
                            child: Text(worker.name),
                          ),
                        ),
                      ],
                      onChanged: (value) => setState(() {
                        selectedWorkerId = value ?? '';
                        selectedAgent = null;
                        reconcileCreateSelection();
                      }),
                    ),
                  ],
                  if (selectedWorker != null) ...[
                    const SizedBox(height: 12),
                    SegmentedButton<String>(
                      segments: supportedAgents
                          .map(
                            (agent) => ButtonSegment(
                              value: agent,
                              icon: Icon(
                                agent == 'claude'
                                    ? Icons.chat_bubble_outline
                                    : Icons.terminal,
                              ),
                              label: Text(_agentLabel(agent)),
                            ),
                          )
                          .toList(),
                      selected: {selectedAgent ?? supportedAgents.first},
                      onSelectionChanged: (values) =>
                          setState(() => selectedAgent = values.first),
                    ),
                    if (selectedAgent != null) ...[
                      const SizedBox(height: 12),
                      _AgentConfigFields(
                        draft: configDraft,
                        agentType: selectedAgent!,
                        onChanged: () => setState(() {}),
                      ),
                    ],
                  ],
                  const SizedBox(height: 12),
                  TextField(
                    controller: preCommands,
                    decoration: const InputDecoration(
                      labelText: 'Pre commands',
                    ),
                    minLines: 2,
                    maxLines: 5,
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: postCommands,
                    decoration: const InputDecoration(
                      labelText: 'Post commands',
                    ),
                    minLines: 2,
                    maxLines: 5,
                  ),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: canSave
                  ? () async {
                      if (task == null) {
                        await apiClient.createTask(
                          title: title.text.trim(),
                          description: description.text.trim(),
                          projectId: projectId!,
                          workerId: selectedWorkerId.isEmpty
                              ? null
                              : selectedWorkerId,
                          agentType: selectedAgent,
                          agentConfig: selectedAgent == null
                              ? null
                              : configDraft.toConfig(selectedAgent!),
                          baseBranch: baseBranch.text.trim().isEmpty
                              ? 'main'
                              : baseBranch.text.trim(),
                          preCommands: stringList(preCommands.text),
                          postCommands: stringList(postCommands.text),
                          startDate: _taskDateIso(startDate),
                          endDate: _taskDateIso(endDate),
                        );
                      } else {
                        await apiClient.updateTask(
                          TaskItem(
                            id: task.id,
                            title: title.text.trim(),
                            description: description.text.trim(),
                            status: task.status,
                            projectId: projectId!,
                            agentType: selectedAgent ?? '',
                            agentConfig: task.agentConfig,
                            baseBranch: baseBranch.text.trim().isEmpty
                                ? 'main'
                                : baseBranch.text.trim(),
                            preCommands: stringList(preCommands.text),
                            postCommands: stringList(postCommands.text),
                            startDate: _taskDateIso(startDate),
                            endDate: _taskDateIso(endDate),
                            createdAt: task.createdAt,
                            updatedAt: task.updatedAt,
                            workerId: task.workerId,
                            worktreePath: task.worktreePath,
                            agentSessionId: task.agentSessionId,
                            result: task.result,
                          ),
                        );
                      }
                      if (context.mounted) {
                        Navigator.of(context).pop(true);
                      }
                    }
                  : null,
              child: const Text('Save'),
            ),
          ],
        );
      },
    ),
  );
}

class _TaskDateField extends StatelessWidget {
  const _TaskDateField({
    required this.label,
    required this.value,
    required this.onChanged,
  });

  final String label;
  final DateTime value;
  final ValueChanged<DateTime> onChanged;

  @override
  Widget build(BuildContext context) {
    return OutlinedButton.icon(
      icon: const Icon(Icons.event_outlined),
      label: Text('$label: ${_taskDateLabel(value)}'),
      onPressed: () async {
        final picked = await showDatePicker(
          context: context,
          initialDate: value,
          firstDate: DateTime(2000),
          lastDate: DateTime(2100),
        );
        if (picked != null) {
          onChanged(DateTime(picked.year, picked.month, picked.day));
        }
      },
    );
  }
}

Future<bool?> showTaskDetailDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required TaskItem task,
  required BoardData boardData,
}) {
  return showDialog<bool>(
    context: context,
    builder: (context) => _TaskDetailDialog(
      apiClient: apiClient,
      task: task,
      boardData: boardData,
    ),
  );
}

typedef _TaskInteractionResponder =
    Future<void> Function(
      TaskInteractionItem interaction, {
      String? decision,
      String? message,
      String? payload,
    });

class _TaskDetailDialog extends StatefulWidget {
  const _TaskDetailDialog({
    required this.apiClient,
    required this.task,
    required this.boardData,
  });

  final ApiClient apiClient;
  final TaskItem task;
  final BoardData boardData;

  @override
  State<_TaskDetailDialog> createState() => _TaskDetailDialogState();
}

class _TaskDetailDialogState extends State<_TaskDetailDialog> {
  late Future<TaskDetailData> _future;
  TaskDetailData? _lastDetail;
  RealtimeRefreshController? _realtime;

  @override
  void initState() {
    super.initState();
    _future = _load();
    _realtime = RealtimeRefreshController(
      events: widget.apiClient.subscribeDomainEvents(
        aggregateId: widget.task.id!,
        aggregateType: 'Task',
      ),
      reload: _reload,
      shouldReload: (_) => true,
    );
  }

  @override
  void dispose() {
    _realtime?.dispose();
    super.dispose();
  }

  Future<TaskDetailData> _load() async {
    final detail = await widget.apiClient.fetchTaskDetail(widget.task.id!);
    _lastDetail = detail;
    return detail;
  }

  void _reload() {
    if (!mounted) {
      return;
    }
    setState(() {
      _future = _load();
    });
  }

  @override
  Widget build(BuildContext context) {
    final currentTask = _lastDetail?.task ?? widget.task;
    final waitingForInput = currentTask.status == 'WAITING_INPUT';
    final archived = currentTask.status == 'ARCHIVED';
    final taskId = currentTask.id;
    final closeAction = _TaskDetailAction(
      key: const ValueKey('task-detail-action-close'),
      label: 'Close',
      icon: Icons.close,
      onPressed: () => Navigator.of(context).pop(false),
    );
    final actions = archived
        ? [
            closeAction,
            _TaskDetailAction(
              key: const ValueKey('task-detail-action-delete'),
              label: 'Delete',
              icon: Icons.delete_outline,
              emphasis: _TaskDetailActionEmphasis.filled,
              onPressed: () async {
                final confirmed = await confirmAction(
                  context,
                  title: 'Delete task',
                  message: 'Delete "${currentTask.title}"?',
                  confirmLabel: 'Delete',
                );
                if (!confirmed) {
                  return;
                }
                await _run(() => widget.apiClient.deleteTask(currentTask.id!));
              },
            ),
          ]
        : [
            closeAction,
            _TaskDetailAction(
              key: const ValueKey('task-detail-action-assign'),
              label: 'Assign',
              icon: Icons.person_add_alt_1,
              onPressed: _assign,
            ),
            _TaskDetailAction(
              key: const ValueKey('task-detail-action-start'),
              label: 'Start',
              icon: Icons.play_arrow,
              onPressed: waitingForInput
                  ? null
                  : () => _run(
                      () => widget.apiClient.startTask(currentTask.id!),
                    ),
            ),
            _TaskDetailAction(
              key: const ValueKey('task-detail-action-interrupt'),
              label: 'Interrupt',
              icon: Icons.stop,
              onPressed: () =>
                  _run(() => widget.apiClient.interruptTask(currentTask.id!)),
            ),
            _TaskDetailAction(
              key: const ValueKey('task-detail-action-retry'),
              label: 'Retry',
              icon: Icons.replay,
              onPressed: waitingForInput
                  ? null
                  : () => _run(
                      () => widget.apiClient.retryTask(currentTask.id!),
                    ),
            ),
            _TaskDetailAction(
              key: const ValueKey('task-detail-action-archive'),
              label: 'Archive',
              icon: Icons.archive_outlined,
              emphasis: _TaskDetailActionEmphasis.filled,
              onPressed: () async {
                final confirmed = await confirmAction(
                  context,
                  title: 'Archive task',
                  message: 'Archive "${currentTask.title}"?',
                  confirmLabel: 'Archive',
                );
                if (!confirmed) {
                  return;
                }
                await _run(() => widget.apiClient.archiveTask(currentTask.id!));
              },
            ),
          ];
    return AlertDialog(
      title: Row(
        children: [
          Expanded(
            child: Text(currentTask.title, overflow: TextOverflow.ellipsis),
          ),
          IconButton(
            tooltip: 'Copy task ID',
            onPressed: taskId == null || taskId.isEmpty
                ? null
                : () => _copyTaskId(taskId),
            icon: const Icon(Icons.copy_outlined),
          ),
        ],
      ),
      content: SizedBox(
        width: math.min(MediaQuery.sizeOf(context).width * 0.88, 1280),
        height: math.min(MediaQuery.sizeOf(context).height * 0.82, 820),
        child: FutureBuilder<TaskDetailData>(
          future: _future,
          builder: (context, snapshot) {
            final detail = snapshot.data ?? _lastDetail;
            if (snapshot.connectionState != ConnectionState.done &&
                detail == null) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snapshot.hasError && detail == null) {
              return ErrorView(
                message: snapshot.error.toString(),
                onRetry: () {
                  _reload();
                },
              );
            }
            return Stack(
              children: [
                _TaskDetailBody(
                  apiClient: widget.apiClient,
                  detail: detail!,
                  projects: widget.boardData.projects,
                  workers: widget.boardData.workers,
                  actions: actions,
                  onContinue: _continueTask,
                  onRefresh: _reload,
                  onRespondInteraction: _respondInteraction,
                ),
                if (snapshot.connectionState != ConnectionState.done)
                  const Positioned(
                    left: 0,
                    right: 0,
                    top: 0,
                    child: LinearProgressIndicator(minHeight: 2),
                  ),
              ],
            );
          },
        ),
      ),
    );
  }

  Future<void> _run(Future<void> Function() action) async {
    await action();
    if (mounted) {
      Navigator.of(context).pop(true);
    }
  }

  Future<void> _copyTaskId(String taskId) async {
    await Clipboard.setData(ClipboardData(text: taskId));
    if (!mounted) {
      return;
    }
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(const SnackBar(content: Text('Task ID copied')));
  }

  Future<void> _continueTask(String message) async {
    await widget.apiClient.continueTask(widget.task.id!, message);
    _reload();
  }

  Future<void> _respondInteraction(
    TaskInteractionItem interaction, {
    String? decision,
    String? message,
    String? payload,
  }) async {
    await widget.apiClient.respondTaskInteraction(
      interactionId: interaction.id,
      decision: decision,
      message: message ?? '',
      payload: payload ?? '',
    );
    _reload();
  }

  Future<void> _assign() async {
    final task = _lastDetail?.task ?? widget.task;
    final currentWorkerId = task.workerId ?? '';
    final candidates = widget.boardData.workers.where((worker) {
      final supports =
          task.agentType.isEmpty ||
          worker.supportedAgents.contains(task.agentType);
      final projectMatches =
          worker.projectBindingMode == 'ALL_PROJECTS' ||
          worker.boundProjectIds.contains(task.projectId);
      return worker.status == 'ONLINE' &&
          supports &&
          worker.supportedAgents.isNotEmpty &&
          projectMatches;
    }).toList();
    if (candidates.isEmpty) {
      return;
    }
    String selected =
        currentWorkerId.isNotEmpty &&
            candidates.any((worker) => worker.id == currentWorkerId)
        ? currentWorkerId
        : candidates.first.id;
    String selectedAgent = task.agentType.isEmpty
        ? candidates.first.supportedAgents.first
        : task.agentType;
    final configDraft = _AgentConfigDraft.fromTask(task);
    final selection = await showDialog<_AssignWorkerSelection>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setState) {
          final worker = _workerById(candidates, selected) ?? candidates.first;
          if (!worker.supportedAgents.contains(selectedAgent)) {
            selectedAgent = worker.supportedAgents.first;
          }
          return AlertDialog(
            title: const Text('Assign worker'),
            content: SizedBox(
              width: 620,
              child: SingleChildScrollView(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    DropdownButtonFormField<String>(
                      value: selected,
                      decoration: const InputDecoration(labelText: 'Worker'),
                      items: candidates
                          .map(
                            (worker) => DropdownMenuItem(
                              value: worker.id,
                              child: Text(worker.name),
                            ),
                          )
                          .toList(),
                      onChanged: (value) => setState(() {
                        selected = value ?? selected;
                        final worker =
                            _workerById(candidates, selected) ??
                            candidates.first;
                        final agentOptions = task.agentType.isEmpty
                            ? worker.supportedAgents
                            : worker.supportedAgents
                                  .where((agent) => agent == task.agentType)
                                  .toList();
                        selectedAgent = agentOptions.isEmpty
                            ? task.agentType
                            : agentOptions.first;
                      }),
                    ),
                    const SizedBox(height: 12),
                    Builder(
                      builder: (context) {
                        final agentOptions = task.agentType.isEmpty
                            ? worker.supportedAgents
                            : worker.supportedAgents
                                  .where((agent) => agent == task.agentType)
                                  .toList();
                        final visibleAgents = agentOptions.isEmpty
                            ? <String>[selectedAgent]
                            : agentOptions;
                        return SegmentedButton<String>(
                          segments: visibleAgents
                              .map(
                                (agent) => ButtonSegment(
                                  value: agent,
                                  icon: Icon(
                                    agent == 'claude'
                                        ? Icons.chat_bubble_outline
                                        : Icons.terminal,
                                  ),
                                  label: Text(_agentLabel(agent)),
                                ),
                              )
                              .toList(),
                          selected: {selectedAgent},
                          onSelectionChanged: task.agentType.isEmpty
                              ? (values) =>
                                    setState(() => selectedAgent = values.first)
                              : null,
                        );
                      },
                    ),
                    const SizedBox(height: 12),
                    _AgentConfigFields(
                      draft: configDraft,
                      agentType: selectedAgent,
                      onChanged: () => setState(() {}),
                    ),
                  ],
                ),
              ),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('Cancel'),
              ),
              FilledButton(
                onPressed: () => Navigator.of(context).pop(
                  _AssignWorkerSelection(
                    workerId: selected,
                    agentType: selectedAgent,
                    agentConfig: configDraft.toConfig(selectedAgent),
                  ),
                ),
                child: const Text('Assign'),
              ),
            ],
          );
        },
      ),
    );
    if (selection == null) {
      return;
    }
    await _run(
      () => widget.apiClient.assignWorker(
        task.id!,
        selection.workerId,
        agentType: selection.agentType,
        agentConfig: selection.agentConfig,
      ),
    );
  }
}

enum _TaskDetailSection { conversation, review, web, terminal, logs, events }

IconData _taskDetailSectionIcon(_TaskDetailSection section) =>
    switch (section) {
      _TaskDetailSection.conversation => Icons.chat_bubble_outline,
      _TaskDetailSection.review => Icons.rate_review_outlined,
      _TaskDetailSection.web => Icons.web_asset,
      _TaskDetailSection.terminal => Icons.terminal,
      _TaskDetailSection.logs => Icons.article_outlined,
      _TaskDetailSection.events => Icons.event_note_outlined,
    };

String _taskDetailSectionLabel(_TaskDetailSection section) => switch (section) {
  _TaskDetailSection.conversation => 'Conversation',
  _TaskDetailSection.review => 'Review',
  _TaskDetailSection.web => 'Web preview',
  _TaskDetailSection.terminal => 'Terminal',
  _TaskDetailSection.logs => 'Logs',
  _TaskDetailSection.events => 'Domain events',
};

Key _taskDetailSectionKey(_TaskDetailSection section) => switch (section) {
  _TaskDetailSection.conversation => const ValueKey(
    'task-detail-section-conversation',
  ),
  _TaskDetailSection.review => const ValueKey('task-detail-section-review'),
  _TaskDetailSection.web => const ValueKey('task-detail-section-web'),
  _TaskDetailSection.terminal => const ValueKey('task-detail-section-terminal'),
  _TaskDetailSection.logs => const ValueKey('task-detail-section-logs'),
  _TaskDetailSection.events => const ValueKey(
    'task-detail-section-domain-events',
  ),
};

enum _TaskDetailActionEmphasis { normal, filled }

class _TaskDetailAction {
  const _TaskDetailAction({
    required this.key,
    required this.label,
    required this.icon,
    required this.onPressed,
    this.emphasis = _TaskDetailActionEmphasis.normal,
  });

  final Key key;
  final String label;
  final IconData icon;
  final VoidCallback? onPressed;
  final _TaskDetailActionEmphasis emphasis;
}

class _TaskDetailBody extends StatefulWidget {
  const _TaskDetailBody({
    required this.apiClient,
    required this.detail,
    required this.projects,
    required this.workers,
    required this.actions,
    required this.onContinue,
    required this.onRefresh,
    required this.onRespondInteraction,
  });

  final ApiClient apiClient;
  final TaskDetailData detail;
  final List<ProjectItem> projects;
  final List<WorkerItem> workers;
  final List<_TaskDetailAction> actions;
  final Future<void> Function(String message) onContinue;
  final VoidCallback onRefresh;
  final _TaskInteractionResponder onRespondInteraction;

  @override
  State<_TaskDetailBody> createState() => _TaskDetailBodyState();
}

class _TaskDetailBodyState extends State<_TaskDetailBody> {
  _TaskDetailSection _selectedSection = _TaskDetailSection.conversation;

  @override
  Widget build(BuildContext context) {
    final task = widget.detail.task;
    final projectName = _projectName(widget.projects, task.projectId);
    return LayoutBuilder(
      builder: (context, constraints) {
        final compact = constraints.maxWidth < 680;
        final railWidth = compact ? 124.0 : 168.0;
        return Stack(
          children: [
            Padding(
              padding: EdgeInsets.only(right: railWidth + 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Wrap(
                    spacing: 12,
                    runSpacing: 8,
                    children: [
                      StatusPill(value: task.status),
                      if (task.agentType.isNotEmpty)
                        DetailText(icon: Icons.terminal, text: task.agentType),
                      DetailText(
                        icon: Icons.folder_copy,
                        text: projectName ?? task.projectId,
                      ),
                      DetailText(
                        icon: Icons.date_range,
                        text: _taskDateRangeLabel(task),
                      ),
                      if ((task.workerId ?? '').isNotEmpty)
                        DetailText(icon: Icons.memory, text: task.workerId!),
                      ..._agentConfigDetailWidgets(task),
                    ],
                  ),
                  const SizedBox(height: 16),
                  Expanded(child: _selectedPanel(task)),
                ],
              ),
            ),
            Positioned(
              top: 0,
              right: 0,
              bottom: 0,
              child: _FloatingCommandRail(
                compact: compact,
                selected: _selectedSection,
                actions: widget.actions,
                onSelected: (section) =>
                    setState(() => _selectedSection = section),
              ),
            ),
          ],
        );
      },
    );
  }

  Widget _selectedPanel(TaskItem task) => switch (_selectedSection) {
    _TaskDetailSection.conversation => _ConversationTab(
      conversations: widget.detail.conversations,
      interactions: widget.detail.interactions,
      onRespondInteraction: widget.onRespondInteraction,
      footer: _ContinuationComposer(task: task, onContinue: widget.onContinue),
    ),
    _TaskDetailSection.review => _ReviewTab(
      apiClient: widget.apiClient,
      detail: widget.detail,
      onRefresh: widget.onRefresh,
    ),
    _TaskDetailSection.web => _WorkerWebTab(
      apiClient: widget.apiClient,
      task: task,
      workers: widget.workers,
    ),
    _TaskDetailSection.terminal => _WorkerTerminalTab(
      apiClient: widget.apiClient,
      task: task,
      worker: _workerById(widget.workers, task.workerId ?? ''),
    ),
    _TaskDetailSection.logs => _RuntimeTab(
      children: widget.detail.logs
          .map((item) => '[${item.stream}] ${item.content}')
          .toList(),
    ),
    _TaskDetailSection.events => _RuntimeTab(
      children: widget.detail.events
          .map(
            (item) =>
                '${item.eventType} v${item.aggregateVersion}: ${item.payload}',
          )
          .toList(),
    ),
  };
}

class _FloatingCommandRail extends StatelessWidget {
  const _FloatingCommandRail({
    required this.compact,
    required this.selected,
    required this.actions,
    required this.onSelected,
  });

  final bool compact;
  final _TaskDetailSection selected;
  final List<_TaskDetailAction> actions;
  final ValueChanged<_TaskDetailSection> onSelected;

  @override
  Widget build(BuildContext context) {
    return _FloatingRailFrame(
      key: const ValueKey('task-detail-floating-command-rail'),
      width: compact ? 124 : 168,
      child: compact
          ? Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: _ActionRailColumn(actions: actions, compact: compact),
                ),
                const SizedBox(width: 4),
                Expanded(
                  child: _SectionRailColumn(
                    selected: selected,
                    compact: compact,
                    onSelected: onSelected,
                  ),
                ),
              ],
            )
          : Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                _SectionRailColumn(
                  selected: selected,
                  compact: compact,
                  onSelected: onSelected,
                ),
                Divider(height: 16, color: Theme.of(context).dividerColor),
                _ActionRailColumn(actions: actions, compact: compact),
              ],
            ),
    );
  }
}

class _SectionRailColumn extends StatelessWidget {
  const _SectionRailColumn({
    required this.selected,
    required this.compact,
    required this.onSelected,
  });

  final _TaskDetailSection selected;
  final bool compact;
  final ValueChanged<_TaskDetailSection> onSelected;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final section in _TaskDetailSection.values)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: _SectionRailButton(
              section: section,
              selected: selected == section,
              compact: compact,
              onPressed: () => onSelected(section),
            ),
          ),
      ],
    );
  }
}

class _ActionRailColumn extends StatelessWidget {
  const _ActionRailColumn({required this.actions, required this.compact});

  final List<_TaskDetailAction> actions;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final action in actions)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: _ActionRailButton(action: action, compact: compact),
          ),
      ],
    );
  }
}

class _SectionRailButton extends StatelessWidget {
  const _SectionRailButton({
    required this.section,
    required this.selected,
    required this.compact,
    required this.onPressed,
  });

  final _TaskDetailSection section;
  final bool selected;
  final bool compact;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final label = _taskDetailSectionLabel(section);
    final icon = Icon(_taskDetailSectionIcon(section));
    if (compact) {
      if (selected) {
        return IconButton.filledTonal(
          key: _taskDetailSectionKey(section),
          tooltip: label,
          onPressed: onPressed,
          icon: icon,
        );
      }
      return IconButton(
        key: _taskDetailSectionKey(section),
        tooltip: label,
        onPressed: onPressed,
        icon: icon,
      );
    }
    final text = Text(label, overflow: TextOverflow.ellipsis);
    if (selected) {
      return FilledButton.tonalIcon(
        key: _taskDetailSectionKey(section),
        onPressed: onPressed,
        icon: icon,
        label: text,
      );
    }
    return TextButton.icon(
      key: _taskDetailSectionKey(section),
      onPressed: onPressed,
      icon: icon,
      label: text,
    );
  }
}

class _ActionRailButton extends StatelessWidget {
  const _ActionRailButton({required this.action, required this.compact});

  final _TaskDetailAction action;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final icon = Icon(action.icon);
    if (compact) {
      if (action.emphasis == _TaskDetailActionEmphasis.filled) {
        return IconButton.filled(
          key: action.key,
          tooltip: action.label,
          onPressed: action.onPressed,
          icon: icon,
        );
      }
      return IconButton(
        key: action.key,
        tooltip: action.label,
        onPressed: action.onPressed,
        icon: icon,
      );
    }
    final label = Text(action.label, overflow: TextOverflow.ellipsis);
    if (action.emphasis == _TaskDetailActionEmphasis.filled) {
      return FilledButton.icon(
        key: action.key,
        onPressed: action.onPressed,
        icon: icon,
        label: label,
      );
    }
    return OutlinedButton.icon(
      key: action.key,
      onPressed: action.onPressed,
      icon: icon,
      label: label,
    );
  }
}

class _FloatingRailFrame extends StatelessWidget {
  const _FloatingRailFrame({
    super.key,
    required this.width,
    required this.child,
  });

  final double width;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return SizedBox(
      width: width,
      child: LayoutBuilder(
        builder: (context, constraints) => Align(
          alignment: Alignment.topCenter,
          child: ConstrainedBox(
            constraints: BoxConstraints(maxHeight: constraints.maxHeight),
            child: Material(
              elevation: 3,
              color: scheme.surface,
              shadowColor: scheme.shadow.withOpacity(0.22),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(8),
                side: BorderSide(color: Theme.of(context).dividerColor),
              ),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(8, 8, 8, 0),
                child: SingleChildScrollView(child: child),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _ConversationTab extends StatelessWidget {
  const _ConversationTab({
    required this.conversations,
    required this.interactions,
    required this.onRespondInteraction,
    required this.footer,
  });

  final List<ConversationItem> conversations;
  final List<TaskInteractionItem> interactions;
  final _TaskInteractionResponder onRespondInteraction;
  final Widget footer;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: const EdgeInsets.only(top: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (interactions.isNotEmpty) ...[
            _PendingInteractions(
              interactions: interactions,
              onRespondInteraction: onRespondInteraction,
            ),
            const SizedBox(height: 12),
          ],
          _ConversationList(conversations: conversations),
          const SizedBox(height: 12),
          footer,
        ],
      ),
    );
  }
}

class _ReviewTab extends StatefulWidget {
  const _ReviewTab({
    required this.apiClient,
    required this.detail,
    required this.onRefresh,
  });

  final ApiClient apiClient;
  final TaskDetailData detail;
  final VoidCallback onRefresh;

  @override
  State<_ReviewTab> createState() => _ReviewTabState();
}

class _ReviewTabState extends State<_ReviewTab> {
  String _scope = 'UNCOMMITTED';
  String _changeSet = 'ALL';
  TaskGitDiffData? _diff;
  int _selectedFile = 0;
  bool _busy = false;
  String? _message;
  final TextEditingController _commentController = TextEditingController();

  List<TaskReviewFindingData> get _findings => widget.detail.reviewRuns
      .expand((run) => run.findings)
      .where((finding) => finding.status != 'DISMISSED')
      .toList();

  TaskGitDiffFileData? get _file {
    final files = _diff?.files ?? const <TaskGitDiffFileData>[];
    if (files.isEmpty) {
      return null;
    }
    final index = math.min(_selectedFile, files.length - 1);
    return files[index];
  }

  @override
  void initState() {
    super.initState();
    _diff = widget.detail.reviewDiff;
    _message = widget.detail.reviewError;
  }

  @override
  void didUpdateWidget(covariant _ReviewTab oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.detail != widget.detail) {
      _diff = widget.detail.reviewDiff;
      _message = widget.detail.reviewError;
      _scope = widget.detail.reviewDiff?.scope ?? 'UNCOMMITTED';
      _changeSet = 'ALL';
      _selectedFile = 0;
    }
  }

  @override
  void dispose() {
    _commentController.dispose();
    super.dispose();
  }

  Future<void> _run(Future<void> Function() action) async {
    if (_busy) {
      return;
    }
    setState(() {
      _busy = true;
      _message = null;
    });
    try {
      await action();
      widget.onRefresh();
    } catch (error) {
      if (mounted) {
        setState(() => _message = error.toString());
      }
    } finally {
      if (mounted) {
        setState(() => _busy = false);
      }
    }
  }

  Future<void> _loadDiff({String? scope, String? changeSet}) async {
    if (_busy) {
      return;
    }
    final nextScope = scope ?? _scope;
    final nextChangeSet = changeSet ?? _changeSet;
    setState(() {
      _busy = true;
      _message = null;
    });
    try {
      final diff = await widget.apiClient.fetchTaskGitDiff(
        widget.detail.task.id!,
        nextScope,
        staged: _stagedFilter(nextScope, nextChangeSet),
      );
      if (mounted) {
        setState(() {
          _scope = nextScope;
          _changeSet = nextScope == 'UNCOMMITTED' ? nextChangeSet : 'ALL';
          _diff = diff;
          _selectedFile = 0;
        });
      }
    } catch (error) {
      if (mounted) {
        setState(() => _message = error.toString());
      }
    } finally {
      if (mounted) {
        setState(() => _busy = false);
      }
    }
  }

  bool? _stagedFilter(String scope, String changeSet) {
    if (scope != 'UNCOMMITTED') {
      return null;
    }
    return switch (changeSet) {
      'STAGED' => true,
      'UNSTAGED' => false,
      _ => null,
    };
  }

  Future<void> _loadScope(String scope) async {
    await _loadDiff(scope: scope);
  }

  Future<void> _loadChangeSet(String changeSet) async {
    await _loadDiff(changeSet: changeSet);
  }

  Future<void> _startReview() => _run(
        () => widget.apiClient.startTaskReview(widget.detail.task.id!, _scope),
      );

  Future<void> _stage() async {
    final file = _file;
    if (file == null) {
      return;
    }
    await _run(
      () => widget.apiClient.stageTaskGitChanges(
        widget.detail.task.id!,
        [file.path],
      ),
    );
  }

  Future<void> _stageHunk(String patch) async {
    final file = _file;
    if (file == null || patch.trim().isEmpty) {
      return;
    }
    await _run(
      () => widget.apiClient.stageTaskGitChanges(
        widget.detail.task.id!,
        [file.path],
        patch: patch,
      ),
    );
  }

  Future<void> _unstage() async {
    final file = _file;
    if (file == null) {
      return;
    }
    await _run(
      () => widget.apiClient.unstageTaskGitChanges(
        widget.detail.task.id!,
        [file.path],
      ),
    );
  }

  Future<void> _unstageHunk(String patch) async {
    final file = _file;
    if (file == null || patch.trim().isEmpty) {
      return;
    }
    await _run(
      () => widget.apiClient.unstageTaskGitChanges(
        widget.detail.task.id!,
        [file.path],
        patch: patch,
      ),
    );
  }

  Future<void> _discard() async {
    final file = _file;
    if (file == null) {
      return;
    }
    final confirmed = await confirmAction(
      context,
      title: 'Discard changes',
      message:
          'Discard changes in ${file.path}? A backup patch will be created before the worker modifies the worktree.',
      confirmLabel: 'Discard',
    );
    if (!confirmed) {
      return;
    }
    TaskGitBackupData? backup;
    await _run(
      () async {
        backup = await widget.apiClient.discardTaskGitChanges(
          widget.detail.task.id!,
          [file.path],
        );
        if (mounted && backup != null) {
          setState(() => _message = 'Backup created: ${backup!.id}');
        }
      },
    );
  }

  Future<void> _discardHunk(String patch) async {
    final file = _file;
    if (file == null || patch.trim().isEmpty) {
      return;
    }
    final confirmed = await confirmAction(
      context,
      title: 'Discard hunk',
      message:
          'Discard this hunk in ${file.path}? A backup patch will be created before the worker modifies the worktree.',
      confirmLabel: 'Discard hunk',
    );
    if (!confirmed) {
      return;
    }
    TaskGitBackupData? backup;
    await _run(
      () async {
        backup = await widget.apiClient.discardTaskGitChanges(
          widget.detail.task.id!,
          [file.path],
          patch: patch,
        );
        if (mounted && backup != null) {
          setState(() => _message = 'Backup created: ${backup!.id}');
        }
      },
    );
  }

  Future<void> _restoreLatest() async {
    if (widget.detail.gitBackups.isEmpty) {
      return;
    }
    final backup = widget.detail.gitBackups.last;
    await _run(
      () => widget.apiClient.restoreTaskGitBackup(
        widget.detail.task.id!,
        backup.id,
      ),
    );
  }

  Future<void> _addComment() async {
    final file = _file;
    final body = _commentController.text.trim();
    if (file == null || body.isEmpty) {
      return;
    }
    await _run(
      () => widget.apiClient.addTaskReviewComment(
        taskId: widget.detail.task.id!,
        path: file.path,
        line: _firstChangedLine(file.patch),
        body: body,
      ),
    );
    _commentController.clear();
  }

  Future<void> _resolveFinding(String id) =>
      _run(() => widget.apiClient.resolveTaskReviewFinding(id));

  Future<void> _dismissFinding(String id) =>
      _run(() => widget.apiClient.dismissTaskReviewFinding(id));

  Future<void> _continueWithFeedback() => _run(
        () => widget.apiClient.continueTaskWithReviewFeedback(
          taskId: widget.detail.task.id!,
          findingIds: _findings
              .where((finding) => finding.status == 'OPEN')
              .map((finding) => finding.id)
              .toList(),
          commentIds: widget.detail.reviewComments
              .where((comment) => !comment.resolved)
              .map((comment) => comment.id)
              .toList(),
          message: 'Please address the selected review feedback.',
        ),
      );

  @override
  Widget build(BuildContext context) {
    final files = _diff?.files ?? const <TaskGitDiffFileData>[];
    final file = _file;
    final backups = widget.detail.gitBackups;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Wrap(
          spacing: 8,
          runSpacing: 8,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(value: 'UNCOMMITTED', label: Text('Uncommitted')),
                ButtonSegment(value: 'BRANCH', label: Text('Branch')),
                ButtonSegment(value: 'LAST_TURN', label: Text('Last turn')),
              ],
              selected: {_scope},
              onSelectionChanged: _busy
                  ? null
                  : (values) => _loadScope(values.first),
            ),
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(value: 'ALL', label: Text('All')),
                ButtonSegment(value: 'STAGED', label: Text('Staged')),
                ButtonSegment(value: 'UNSTAGED', label: Text('Unstaged')),
              ],
              selected: {_changeSet},
              onSelectionChanged: _busy || _scope != 'UNCOMMITTED'
                  ? null
                  : (values) => _loadChangeSet(values.first),
            ),
            FilledButton.icon(
              key: const ValueKey('review-action-ai-review'),
              onPressed: _busy ? null : _startReview,
              icon: const Icon(Icons.rate_review),
              label: const Text('AI Review'),
            ),
            OutlinedButton.icon(
              onPressed: _busy || file == null ? null : _stage,
              icon: const Icon(Icons.add_task),
              label: const Text('Stage'),
            ),
            OutlinedButton.icon(
              onPressed: _busy || file == null ? null : _unstage,
              icon: const Icon(Icons.remove_done),
              label: const Text('Unstage'),
            ),
            OutlinedButton.icon(
              key: const ValueKey('review-action-discard'),
              onPressed: _busy || file == null ? null : _discard,
              icon: const Icon(Icons.undo),
              label: const Text('Discard'),
            ),
            OutlinedButton.icon(
              onPressed: _busy || backups.isEmpty ? null : _restoreLatest,
              icon: const Icon(Icons.restore),
              label: Text(
                backups.isEmpty ? 'Restore' : 'Restore ${backups.last.id}',
              ),
            ),
            FilledButton.tonalIcon(
              key: const ValueKey('review-action-feedback'),
              onPressed:
                  _busy || !_canContinueWithFeedback()
                      ? null
                      : _continueWithFeedback,
              icon: const Icon(Icons.send),
              label: const Text('Handle feedback'),
            ),
          ],
        ),
        if (_busy) const LinearProgressIndicator(minHeight: 2),
        if ((_message ?? '').isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(_message!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
          ),
        const SizedBox(height: 12),
        Expanded(
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              SizedBox(
                width: 260,
                child: _ReviewFileList(
                  files: files,
                  selected: _selectedFile,
                  onSelected: (index) => setState(() => _selectedFile = index),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: _ReviewDiffPane(
                  file: file,
                  findings: _findings,
                  comments: widget.detail.reviewComments,
                  controller: _commentController,
                  onAddComment: _busy ? null : _addComment,
                  onResolveFinding: _busy ? null : _resolveFinding,
                  onDismissFinding: _busy ? null : _dismissFinding,
                  onStageHunk: _busy ? null : _stageHunk,
                  onUnstageHunk: _busy ? null : _unstageHunk,
                  onDiscardHunk: _busy ? null : _discardHunk,
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }

  bool _canContinueWithFeedback() =>
      widget.detail.task.status == 'COMPLETED' &&
      (widget.detail.task.agentSessionId ?? '').isNotEmpty &&
      (_findings.any((finding) => finding.status == 'OPEN') ||
          widget.detail.reviewComments.any((comment) => !comment.resolved));
}

class _ReviewFileList extends StatelessWidget {
  const _ReviewFileList({
    required this.files,
    required this.selected,
    required this.onSelected,
  });

  final List<TaskGitDiffFileData> files;
  final int selected;
  final ValueChanged<int> onSelected;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border.all(color: Theme.of(context).dividerColor),
        borderRadius: BorderRadius.circular(8),
      ),
      child: ListView.builder(
        itemCount: files.isEmpty ? 1 : files.length,
        itemBuilder: (context, index) {
          if (files.isEmpty) {
            return const ListTile(title: Text('No changes'));
          }
          final file = files[index];
          return ListTile(
            selected: selected == index,
            dense: true,
            title: Text(file.path, overflow: TextOverflow.ellipsis),
            subtitle: Text('${file.status}  +${file.additions} -${file.deletions}'),
            trailing: file.staged ? const Icon(Icons.check, size: 18) : null,
            onTap: () => onSelected(index),
          );
        },
      ),
    );
  }
}

class _ReviewDiffPane extends StatelessWidget {
  const _ReviewDiffPane({
    required this.file,
    required this.findings,
    required this.comments,
    required this.controller,
    required this.onAddComment,
    required this.onResolveFinding,
    required this.onDismissFinding,
    required this.onStageHunk,
    required this.onUnstageHunk,
    required this.onDiscardHunk,
  });

  final TaskGitDiffFileData? file;
  final List<TaskReviewFindingData> findings;
  final List<TaskReviewCommentData> comments;
  final TextEditingController controller;
  final VoidCallback? onAddComment;
  final ValueChanged<String>? onResolveFinding;
  final ValueChanged<String>? onDismissFinding;
  final Future<void> Function(String patch)? onStageHunk;
  final Future<void> Function(String patch)? onUnstageHunk;
  final Future<void> Function(String patch)? onDiscardHunk;

  @override
  Widget build(BuildContext context) {
    final selectedPath = file?.path ?? '';
    final selectedFindings =
        findings.where((finding) => finding.path == selectedPath).toList();
    final selectedComments =
        comments.where((comment) => comment.path == selectedPath).toList();
    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border.all(color: Theme.of(context).dividerColor),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: file == null
            ? Text('No diff selected', style: Theme.of(context).textTheme.bodySmall)
            : Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Wrap(
                    spacing: 12,
                    runSpacing: 8,
                    children: [
                      DetailText(icon: Icons.description_outlined, text: file!.path),
                      DetailText(icon: Icons.add, text: '+${file!.additions}'),
                      DetailText(icon: Icons.remove, text: '-${file!.deletions}'),
                    ],
                  ),
                  const SizedBox(height: 10),
                  Expanded(
                    child: _ReviewHunkList(
                      file: file!,
                      findings: selectedFindings,
                      comments: selectedComments,
                      onStageHunk: onStageHunk,
                      onUnstageHunk: onUnstageHunk,
                      onDiscardHunk: onDiscardHunk,
                    ),
                  ),
                  const SizedBox(height: 10),
                  _ReviewAnnotations(
                    findings: selectedFindings,
                    comments: selectedComments,
                    onResolveFinding: onResolveFinding,
                    onDismissFinding: onDismissFinding,
                  ),
                  const SizedBox(height: 10),
                  Row(
                    children: [
                      Expanded(
                        child: TextField(
                          controller: controller,
                          minLines: 1,
                          maxLines: 2,
                          decoration: const InputDecoration(
                            labelText: 'Inline comment',
                            border: OutlineInputBorder(),
                          ),
                        ),
                      ),
                      const SizedBox(width: 8),
                      IconButton.filled(
                        tooltip: 'Add inline comment',
                        onPressed: onAddComment,
                        icon: const Icon(Icons.add_comment_outlined),
                      ),
                    ],
                  ),
                ],
              ),
      ),
    );
  }
}

class _ReviewHunkList extends StatelessWidget {
  const _ReviewHunkList({
    required this.file,
    required this.findings,
    required this.comments,
    required this.onStageHunk,
    required this.onUnstageHunk,
    required this.onDiscardHunk,
  });

  final TaskGitDiffFileData file;
  final List<TaskReviewFindingData> findings;
  final List<TaskReviewCommentData> comments;
  final Future<void> Function(String patch)? onStageHunk;
  final Future<void> Function(String patch)? onUnstageHunk;
  final Future<void> Function(String patch)? onDiscardHunk;

  @override
  Widget build(BuildContext context) {
    final hunks = _parseDiffHunks(file.patch);
    if (hunks.isEmpty) {
      return Text(
        file.truncated ? '(diff truncated)' : '(empty patch)',
        style: Theme.of(context).textTheme.bodySmall,
      );
    }
    return ListView.separated(
      itemCount: hunks.length + (file.truncated ? 1 : 0),
      separatorBuilder: (context, index) => const SizedBox(height: 8),
      itemBuilder: (context, index) {
        if (index >= hunks.length) {
          return Text(
            'Diff truncated by worker response limit.',
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          );
        }
        return _ReviewHunkView(
          hunk: hunks[index],
          findings: findings,
          comments: comments,
          onStageHunk: onStageHunk,
          onUnstageHunk: onUnstageHunk,
          onDiscardHunk: onDiscardHunk,
        );
      },
    );
  }
}

class _ReviewHunkView extends StatelessWidget {
  const _ReviewHunkView({
    required this.hunk,
    required this.findings,
    required this.comments,
    required this.onStageHunk,
    required this.onUnstageHunk,
    required this.onDiscardHunk,
  });

  final _DiffHunk hunk;
  final List<TaskReviewFindingData> findings;
  final List<TaskReviewCommentData> comments;
  final Future<void> Function(String patch)? onStageHunk;
  final Future<void> Function(String patch)? onUnstageHunk;
  final Future<void> Function(String patch)? onDiscardHunk;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final findingsByLine = _findingsByLine(findings);
    final commentsByLine = _commentsByLine(comments);
    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border.all(color: theme.dividerColor),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Container(
            color: theme.colorScheme.surfaceContainerHighest.withOpacity(0.55),
            padding: const EdgeInsets.only(left: 10, right: 4),
            child: Row(
              children: [
                Expanded(
                  child: SelectableText(
                    hunk.header,
                    style: const TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 12,
                    ),
                  ),
                ),
                IconButton(
                  tooltip: 'Stage hunk',
                  onPressed: onStageHunk == null
                      ? null
                      : () => onStageHunk?.call(hunk.patch),
                  icon: const Icon(Icons.add_task, size: 18),
                ),
                IconButton(
                  tooltip: 'Unstage hunk',
                  onPressed: onUnstageHunk == null
                      ? null
                      : () => onUnstageHunk?.call(hunk.patch),
                  icon: const Icon(Icons.remove_done, size: 18),
                ),
                IconButton(
                  tooltip: 'Discard hunk',
                  onPressed: onDiscardHunk == null
                      ? null
                      : () => onDiscardHunk?.call(hunk.patch),
                  icon: const Icon(Icons.undo, size: 18),
                ),
              ],
            ),
          ),
          for (final line in hunk.lines)
            _ReviewDiffLineView(
              line: line,
              findings:
                  findingsByLine[line.newLine] ??
                      const <TaskReviewFindingData>[],
              comments:
                  commentsByLine[line.newLine] ??
                      const <TaskReviewCommentData>[],
            ),
        ],
      ),
    );
  }
}

class _ReviewDiffLineView extends StatelessWidget {
  const _ReviewDiffLineView({
    required this.line,
    required this.findings,
    required this.comments,
  });

  final _DiffLine line;
  final List<TaskReviewFindingData> findings;
  final List<TaskReviewCommentData> comments;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final color = switch (line.kind) {
      _DiffLineKind.added => Colors.green.withOpacity(0.08),
      _DiffLineKind.deleted => Colors.red.withOpacity(0.08),
      _DiffLineKind.header => theme.colorScheme.surfaceContainerHighest,
      _ => null,
    };
    final hasAnnotations = findings.isNotEmpty || comments.isNotEmpty;
    return Container(
      color: color,
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SizedBox(
                width: 72,
                child: Text(
                  '${line.oldLine?.toString() ?? ''}'.padLeft(4) +
                      ' ' +
                      '${line.newLine?.toString() ?? ''}'.padLeft(4),
                  style: theme.textTheme.bodySmall?.copyWith(
                    fontFamily: 'monospace',
                    color: theme.hintColor,
                  ),
                ),
              ),
              Expanded(
                child: SelectableText(
                  line.text,
                  style: const TextStyle(
                    fontFamily: 'monospace',
                    fontSize: 13,
                    height: 1.35,
                  ),
                ),
              ),
            ],
          ),
          if (hasAnnotations)
            Padding(
              padding: const EdgeInsets.only(left: 72, top: 4, bottom: 4),
              child: Wrap(
                spacing: 6,
                runSpacing: 4,
                children: [
                  for (final finding in findings)
                    Chip(
                      avatar: const Icon(
                        Icons.report_problem_outlined,
                        size: 16,
                      ),
                      label: Text(
                        '${finding.severity}: ${finding.title}',
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  for (final comment in comments)
                    Chip(
                      avatar: const Icon(
                        Icons.mode_comment_outlined,
                        size: 16,
                      ),
                      label: Text(
                        comment.body,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

class _ReviewAnnotations extends StatelessWidget {
  const _ReviewAnnotations({
    required this.findings,
    required this.comments,
    required this.onResolveFinding,
    required this.onDismissFinding,
  });

  final List<TaskReviewFindingData> findings;
  final List<TaskReviewCommentData> comments;
  final ValueChanged<String>? onResolveFinding;
  final ValueChanged<String>? onDismissFinding;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final finding in findings)
          ListTile(
            dense: true,
            leading: const Icon(Icons.report_problem_outlined),
            title: Text(finding.title, overflow: TextOverflow.ellipsis),
            subtitle: Text(
              '${finding.severity} ${finding.status} line ${finding.line}: ${finding.body}',
              overflow: TextOverflow.ellipsis,
            ),
            trailing: SizedBox(
              width: 96,
              child: Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  IconButton(
                    tooltip: 'Resolve finding',
                    onPressed:
                        finding.status == 'OPEN' && onResolveFinding != null
                            ? () => onResolveFinding?.call(finding.id)
                            : null,
                    icon: const Icon(Icons.check_circle_outline),
                  ),
                  IconButton(
                    tooltip: 'Dismiss finding',
                    onPressed:
                        finding.status == 'OPEN' && onDismissFinding != null
                            ? () => onDismissFinding?.call(finding.id)
                            : null,
                    icon: const Icon(Icons.block),
                  ),
                ],
              ),
            ),
          ),
        for (final comment in comments)
          ListTile(
            dense: true,
            leading: const Icon(Icons.mode_comment_outlined),
            title: Text(comment.body, overflow: TextOverflow.ellipsis),
            subtitle: Text('line ${comment.line}'),
          ),
      ],
    );
  }
}

enum _DiffLineKind { context, added, deleted, header }

class _DiffHunk {
  const _DiffHunk({
    required this.header,
    required this.patch,
    required this.lines,
  });

  final String header;
  final String patch;
  final List<_DiffLine> lines;
}

class _DiffLine {
  const _DiffLine({
    required this.text,
    required this.kind,
    this.oldLine,
    this.newLine,
  });

  final String text;
  final _DiffLineKind kind;
  final int? oldLine;
  final int? newLine;
}

class _HunkCursor {
  const _HunkCursor({required this.oldLine, required this.newLine});

  final int oldLine;
  final int newLine;
}

List<_DiffHunk> _parseDiffHunks(String patch) {
  if (patch.trim().isEmpty) {
    return const [];
  }
  final prefix = <String>[];
  final hunks = <_DiffHunk>[];
  List<String>? rawHunk;
  var parsedLines = <_DiffLine>[];
  var oldLine = 0;
  var newLine = 0;

  void finishHunk() {
    final raw = rawHunk;
    if (raw == null || raw.isEmpty) {
      return;
    }
    hunks.add(
      _DiffHunk(
        header: raw.first,
        patch: _joinPatchLines([...prefix, ...raw]),
        lines: parsedLines,
      ),
    );
  }

  for (final line in const LineSplitter().convert(patch)) {
    if (line.startsWith('@@')) {
      finishHunk();
      rawHunk = [line];
      parsedLines = <_DiffLine>[];
      final cursor = _parseHunkCursor(line);
      oldLine = cursor.oldLine;
      newLine = cursor.newLine;
      continue;
    }
    if (rawHunk == null) {
      prefix.add(line);
      continue;
    }
    rawHunk!.add(line);
    if (line.startsWith('\\')) {
      parsedLines.add(
        _DiffLine(text: line, kind: _DiffLineKind.header),
      );
      continue;
    }
    if (line.startsWith('+') && !line.startsWith('+++')) {
      parsedLines.add(
        _DiffLine(
          text: line,
          kind: _DiffLineKind.added,
          newLine: newLine,
        ),
      );
      newLine++;
      continue;
    }
    if (line.startsWith('-') && !line.startsWith('---')) {
      parsedLines.add(
        _DiffLine(
          text: line,
          kind: _DiffLineKind.deleted,
          oldLine: oldLine,
        ),
      );
      oldLine++;
      continue;
    }
    parsedLines.add(
      _DiffLine(
        text: line,
        kind: _DiffLineKind.context,
        oldLine: oldLine,
        newLine: newLine,
      ),
    );
    oldLine++;
    newLine++;
  }
  finishHunk();
  if (hunks.isEmpty) {
    return [
      _DiffHunk(
        header: 'File diff',
        patch: patch.endsWith('\n') ? patch : '$patch\n',
        lines: const LineSplitter()
            .convert(patch)
            .map(
              (line) => _DiffLine(
                text: line,
                kind: _DiffLineKind.context,
              ),
            )
            .toList(),
      ),
    ];
  }
  return hunks;
}

_HunkCursor _parseHunkCursor(String header) {
  final match = RegExp(r'@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@')
      .firstMatch(header);
  if (match == null) {
    return const _HunkCursor(oldLine: 0, newLine: 0);
  }
  return _HunkCursor(
    oldLine: int.tryParse(match.group(1) ?? '') ?? 0,
    newLine: int.tryParse(match.group(2) ?? '') ?? 0,
  );
}

String _joinPatchLines(List<String> lines) => '${lines.join('\n')}\n';

Map<int?, List<TaskReviewFindingData>> _findingsByLine(
  List<TaskReviewFindingData> findings,
) {
  final grouped = <int?, List<TaskReviewFindingData>>{};
  for (final finding in findings) {
    grouped.putIfAbsent(finding.line, () => []).add(finding);
  }
  return grouped;
}

Map<int?, List<TaskReviewCommentData>> _commentsByLine(
  List<TaskReviewCommentData> comments,
) {
  final grouped = <int?, List<TaskReviewCommentData>>{};
  for (final comment in comments) {
    grouped.putIfAbsent(comment.line, () => []).add(comment);
  }
  return grouped;
}

int _firstChangedLine(String patch) {
  for (final line in const LineSplitter().convert(patch)) {
    if (line.startsWith('@@')) {
      final match = RegExp(r'\+(\d+)').firstMatch(line);
      if (match != null) {
        return int.tryParse(match.group(1) ?? '') ?? 0;
      }
    }
  }
  return 0;
}

class _WorkerWebTab extends StatefulWidget {
  const _WorkerWebTab({
    required this.apiClient,
    required this.task,
    required this.workers,
  });

  final ApiClient apiClient;
  final TaskItem task;
  final List<WorkerItem> workers;

  @override
  State<_WorkerWebTab> createState() => _WorkerWebTabState();
}

class _WorkerWebTabState extends State<_WorkerWebTab> {
  final TextEditingController _address = TextEditingController();
  late String _selectedWorkerId = _initialWorkerId();
  String _previewUrl = '';
  String _error = '';

  @override
  void initState() {
    super.initState();
    _address.addListener(_addressChanged);
  }

  @override
  void didUpdateWidget(covariant _WorkerWebTab oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!widget.workers.any((worker) => worker.id == _selectedWorkerId)) {
      _selectedWorkerId = _initialWorkerId();
      _previewUrl = '';
      _error = '';
    }
  }

  @override
  void dispose() {
    _address.removeListener(_addressChanged);
    _address.dispose();
    super.dispose();
  }

  String _initialWorkerId() {
    final taskWorkerId = widget.task.workerId ?? '';
    if (taskWorkerId.isNotEmpty &&
        widget.workers.any((worker) => worker.id == taskWorkerId)) {
      return taskWorkerId;
    }
    return widget.workers.isEmpty ? '' : widget.workers.first.id;
  }

  void _addressChanged() {
    if (mounted) {
      setState(() {});
    }
  }

  void _openPreview() {
    final worker = _workerById(widget.workers, _selectedWorkerId);
    if (worker == null) {
      setState(() {
        _previewUrl = '';
        _error = 'Worker is required';
      });
      return;
    }
    try {
      final url = widget.apiClient.workerWebProxyUrl(
        workerName: worker.name,
        address: _address.text,
      );
      setState(() {
        _previewUrl = url;
        _error = '';
      });
    } on FormatException catch (err) {
      setState(() {
        _previewUrl = '';
        _error = err.message;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final hasWorkers = widget.workers.isNotEmpty;
    final canOpen = hasWorkers && _address.text.trim().isNotEmpty;
    return SingleChildScrollView(
      padding: const EdgeInsets.only(top: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          LayoutBuilder(
            builder: (context, constraints) {
              final compact = constraints.maxWidth < 560;
              final workerField = DropdownButtonFormField<String>(
                value: hasWorkers ? _selectedWorkerId : null,
                isExpanded: true,
                decoration: const InputDecoration(
                  labelText: 'Worker',
                  border: OutlineInputBorder(),
                ),
                items: widget.workers
                    .map(
                      (worker) => DropdownMenuItem(
                        value: worker.id,
                        child: Text(
                          worker.name,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    )
                    .toList(),
                selectedItemBuilder: (context) => widget.workers
                    .map(
                      (worker) =>
                          Text(worker.name, overflow: TextOverflow.ellipsis),
                    )
                    .toList(),
                onChanged: hasWorkers
                    ? (value) => setState(() {
                        _selectedWorkerId = value ?? _selectedWorkerId;
                        _previewUrl = '';
                        _error = '';
                      })
                    : null,
              );
              final addressField = TextField(
                controller: _address,
                enabled: hasWorkers,
                decoration: const InputDecoration(
                  labelText: 'Worker web address',
                  hintText: 'localhost:3000',
                  border: OutlineInputBorder(),
                ),
                onSubmitted: (_) {
                  if (canOpen) {
                    _openPreview();
                  }
                },
              );
              final openButton = IconButton.filled(
                tooltip: 'Open worker web preview',
                onPressed: canOpen ? _openPreview : null,
                icon: const Icon(Icons.open_in_browser),
              );
              if (compact) {
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    workerField,
                    const SizedBox(height: 10),
                    Row(
                      crossAxisAlignment: CrossAxisAlignment.end,
                      children: [
                        Expanded(child: addressField),
                        const SizedBox(width: 8),
                        openButton,
                      ],
                    ),
                  ],
                );
              }
              return Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  SizedBox(width: 220, child: workerField),
                  const SizedBox(width: 10),
                  Expanded(child: addressField),
                  const SizedBox(width: 8),
                  openButton,
                ],
              );
            },
          ),
          if (_error.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(
              _error,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ],
          if (_previewUrl.isNotEmpty) ...[
            const SizedBox(height: 12),
            SelectableText(
              _previewUrl,
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 8),
            SizedBox(
              key: const ValueKey('task-detail-worker-web-preview'),
              height: 460,
              child: WorkerWebPreview(url: _previewUrl),
            ),
          ],
        ],
      ),
    );
  }
}

class _WorkerTerminalTab extends StatefulWidget {
  const _WorkerTerminalTab({
    required this.apiClient,
    required this.task,
    required this.worker,
  });

  final ApiClient apiClient;
  final TaskItem task;
  final WorkerItem? worker;

  @override
  State<_WorkerTerminalTab> createState() => _WorkerTerminalTabState();
}

class _WorkerTerminalTabState extends State<_WorkerTerminalTab> {
  late final Terminal _terminal = Terminal(
    maxLines: 2000,
    onOutput: _sendInput,
    onResize: (width, height, pixelWidth, pixelHeight) {
      _socket?.sendResize(height, width);
    },
  );
  WorkerTerminalSocket? _socket;
  StreamSubscription<WorkerTerminalEvent>? _subscription;
  String _status = 'Disconnected';
  String _error = '';
  bool _connecting = false;

  @override
  void didUpdateWidget(covariant _WorkerTerminalTab oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.task.id != widget.task.id ||
        oldWidget.task.worktreePath != widget.task.worktreePath ||
        oldWidget.worker?.id != widget.worker?.id) {
      _disconnect();
    }
  }

  @override
  void dispose() {
    _disconnect(notify: false);
    super.dispose();
  }

  String get _terminalUrl {
    final taskID = widget.task.id ?? '';
    if (taskID.isEmpty) {
      return '';
    }
    return widget.apiClient.workerTerminalWebSocketUrl(taskID);
  }

  String _unavailableReason() {
    final task = widget.task;
    final worker = widget.worker;
    if (worker == null) {
      return 'Task worker is not available';
    }
    if (worker.status != 'ONLINE') {
      return 'Worker is not online';
    }
    if ((task.worktreePath ?? '').trim().isEmpty) {
      return 'Task worktree is not ready';
    }
    if (task.status == 'ARCHIVED') {
      return 'Task is archived';
    }
    if (worker.capabilities['terminal_enabled'] != 'true') {
      return 'Worker terminal is disabled';
    }
    return '';
  }

  Future<void> _connect() async {
    final reason = _unavailableReason();
    if (reason.isNotEmpty || _socket != null || _connecting) {
      return;
    }
    final url = _terminalUrl;
    setState(() {
      _connecting = true;
      _status = 'Checking';
      _error = '';
    });
    try {
      await widget.apiClient.checkWorkerTerminal(widget.task.id ?? '');
      if (!mounted) {
        return;
      }
      _terminal.write('\r\nConnecting worker terminal...\r\n');
      final socket = connectWorkerTerminal(url);
      _socket = socket;
      _subscription = socket.events.listen(_handleEvent);
      setState(() {
        _connecting = false;
        _status = 'Connected';
        _error = '';
      });
    } catch (error) {
      if (!mounted) {
        return;
      }
      final message = _terminalErrorMessage(error);
      _terminal.write('\r\n[terminal error] $message\r\n');
      setState(() {
        _connecting = false;
        _status = 'Disconnected';
        _error = message;
      });
    }
  }

  void _disconnect({bool notify = true}) {
    _subscription?.cancel();
    _subscription = null;
    _socket?.close();
    _socket = null;
    _connecting = false;
    if (mounted && notify) {
      setState(() {
        _status = 'Disconnected';
      });
    } else {
      _status = 'Disconnected';
    }
  }

  void _sendInput(String data) {
    _socket?.sendInput(data);
  }

  void _handleEvent(WorkerTerminalEvent event) {
    if (!mounted) {
      return;
    }
    switch (event.type) {
      case 'output':
        _terminal.write(event.data);
        break;
      case 'exit':
        _terminal.write('\r\n[terminal exited]\r\n');
        _subscription?.cancel();
        _subscription = null;
        _socket = null;
        setState(() {
          _connecting = false;
          _status = 'Disconnected';
        });
        break;
      case 'error':
        _terminal.write('\r\n[terminal error] ${event.data}\r\n');
        setState(() {
          _connecting = false;
          _error = event.data;
          _status = 'Disconnected';
        });
        break;
      default:
        break;
    }
  }

  @override
  Widget build(BuildContext context) {
    final reason = _unavailableReason();
    final url = _terminalUrl;
    return Padding(
      padding: const EdgeInsets.only(top: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              StatusPill(value: _status),
              const SizedBox(width: 10),
              Expanded(
                child: SelectableText(
                  url,
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ),
              const SizedBox(width: 8),
              if (reason.isEmpty)
                IconButton.filled(
                  tooltip: _connecting
                      ? 'Connecting worker terminal'
                      : _socket == null
                      ? 'Connect worker terminal'
                      : 'Disconnect worker terminal',
                  onPressed: _connecting
                      ? null
                      : (_socket == null ? _connect : _disconnect),
                  icon: Icon(_socket == null ? Icons.power : Icons.power_off),
                ),
            ],
          ),
          if (reason.isNotEmpty) ...[
            const SizedBox(height: 16),
            _TerminalUnavailable(reason: reason),
          ] else ...[
            if (_error.isNotEmpty) ...[
              const SizedBox(height: 8),
              Text(
                _error,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ],
            const SizedBox(height: 12),
            Expanded(
              child: DecoratedBox(
                key: const ValueKey('task-detail-worker-terminal'),
                decoration: BoxDecoration(
                  color: const Color(0xff111318),
                  borderRadius: BorderRadius.circular(6),
                  border: Border.all(color: Theme.of(context).dividerColor),
                ),
                child: TerminalView(
                  _terminal,
                  autofocus: true,
                  padding: const EdgeInsets.all(10),
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

String _terminalErrorMessage(Object error) {
  final text = error.toString();
  return text
      .replaceFirst(RegExp(r'^Bad state:\s*'), '')
      .replaceFirst(RegExp(r'^Exception:\s*'), '')
      .trim();
}

class _TerminalUnavailable extends StatelessWidget {
  const _TerminalUnavailable({required this.reason});

  final String reason;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return DecoratedBox(
      decoration: BoxDecoration(
        color: scheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: Theme.of(context).dividerColor),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              'Terminal unavailable',
              style: Theme.of(context).textTheme.titleMedium,
            ),
            const SizedBox(height: 6),
            Text(reason),
          ],
        ),
      ),
    );
  }
}

class _PendingInteractions extends StatelessWidget {
  const _PendingInteractions({
    required this.interactions,
    required this.onRespondInteraction,
  });

  final List<TaskInteractionItem> interactions;
  final _TaskInteractionResponder onRespondInteraction;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final interaction in interactions)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: _InteractionCard(
              interaction: interaction,
              onRespondInteraction: onRespondInteraction,
            ),
          ),
      ],
    );
  }
}

class _InteractionCard extends StatelessWidget {
  const _InteractionCard({
    required this.interaction,
    required this.onRespondInteraction,
  });

  final TaskInteractionItem interaction;
  final _TaskInteractionResponder onRespondInteraction;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final raw = interaction.rawJson;
    final details = _interactionDetails(interaction, raw);
    return Card(
      margin: EdgeInsets.zero,
      color: scheme.tertiaryContainer.withOpacity(0.55),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Wrap(
              spacing: 8,
              runSpacing: 6,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                Icon(_interactionIcon(interaction.kind), size: 18),
                Text(
                  interaction.title.isEmpty
                      ? _interactionKindLabel(interaction.kind)
                      : interaction.title,
                  style: Theme.of(context).textTheme.titleSmall,
                ),
                StatusPill(value: interaction.kind),
              ],
            ),
            if (interaction.body.trim().isNotEmpty) ...[
              const SizedBox(height: 8),
              SelectableText(interaction.body),
            ],
            if (details.isNotEmpty) ...[
              const SizedBox(height: 10),
              ...details.map(
                (item) => Padding(
                  padding: const EdgeInsets.only(bottom: 4),
                  child: DetailText(icon: item.icon, text: item.text),
                ),
              ),
            ],
            if (interaction.rawPayload.trim().isNotEmpty) ...[
              const SizedBox(height: 10),
              _RawPayloadBlock(payload: interaction.rawPayload),
            ],
            const SizedBox(height: 12),
            if (interaction.kind == 'USER_INPUT')
              _UserInputInteractionForm(
                interaction: interaction,
                onRespondInteraction: onRespondInteraction,
              )
            else
              _ApprovalInteractionActions(
                interaction: interaction,
                onRespondInteraction: onRespondInteraction,
              ),
          ],
        ),
      ),
    );
  }
}

class _ApprovalInteractionActions extends StatefulWidget {
  const _ApprovalInteractionActions({
    required this.interaction,
    required this.onRespondInteraction,
  });

  final TaskInteractionItem interaction;
  final _TaskInteractionResponder onRespondInteraction;

  @override
  State<_ApprovalInteractionActions> createState() =>
      _ApprovalInteractionActionsState();
}

class _ApprovalInteractionActionsState
    extends State<_ApprovalInteractionActions> {
  bool _sending = false;

  Future<void> _send(String decision) async {
    if (_sending) {
      return;
    }
    setState(() => _sending = true);
    try {
      await widget.onRespondInteraction(widget.interaction, decision: decision);
    } finally {
      if (mounted) {
        setState(() => _sending = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        FilledButton.icon(
          onPressed: _sending ? null : () => _send('APPROVE'),
          icon: const Icon(Icons.check),
          label: const Text('Approve'),
        ),
        OutlinedButton.icon(
          onPressed: _sending ? null : () => _send('APPROVE_FOR_SESSION'),
          icon: const Icon(Icons.done_all),
          label: const Text('Approve for session'),
        ),
        OutlinedButton.icon(
          onPressed: _sending ? null : () => _send('DENY'),
          icon: const Icon(Icons.block),
          label: const Text('Deny'),
        ),
        TextButton.icon(
          onPressed: _sending ? null : () => _send('CANCEL'),
          icon: const Icon(Icons.close),
          label: const Text('Cancel'),
        ),
      ],
    );
  }
}

class _UserInputInteractionForm extends StatefulWidget {
  const _UserInputInteractionForm({
    required this.interaction,
    required this.onRespondInteraction,
  });

  final TaskInteractionItem interaction;
  final _TaskInteractionResponder onRespondInteraction;

  @override
  State<_UserInputInteractionForm> createState() =>
      _UserInputInteractionFormState();
}

class _UserInputInteractionFormState extends State<_UserInputInteractionForm> {
  final TextEditingController _controller = TextEditingController();
  String _selected = '';
  bool _sending = false;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final message = _controller.text.trim().isNotEmpty
        ? _controller.text.trim()
        : _selected;
    if (message.isEmpty || _sending) {
      return;
    }
    setState(() => _sending = true);
    try {
      final payload = _userInputPayload(widget.interaction, message);
      await widget.onRespondInteraction(
        widget.interaction,
        message: message,
        payload: payload,
      );
      _controller.clear();
    } finally {
      if (mounted) {
        setState(() => _sending = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final question = _firstInteractionQuestion(widget.interaction);
    final options = _questionOptions(question);
    final isSecret = question['isSecret'] == true;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (options.isNotEmpty) ...[
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: options
                .map(
                  (option) => ChoiceChip(
                    label: Text(option.label),
                    selected: _selected == option.label,
                    onSelected: _sending
                        ? null
                        : (selected) => setState(
                            () => _selected = selected ? option.label : '',
                          ),
                  ),
                )
                .toList(),
          ),
          const SizedBox(height: 10),
        ],
        Row(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Expanded(
              child: TextField(
                controller: _controller,
                enabled: !_sending,
                obscureText: isSecret,
                minLines: 1,
                maxLines: isSecret ? 1 : 3,
                decoration: const InputDecoration(
                  labelText: 'Response',
                  border: OutlineInputBorder(),
                ),
                onSubmitted: (_) => _send(),
              ),
            ),
            const SizedBox(width: 8),
            IconButton.filled(
              tooltip: 'Submit response',
              onPressed: _sending ? null : _send,
              icon: _sending
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.send),
            ),
          ],
        ),
      ],
    );
  }
}

class _RawPayloadBlock extends StatelessWidget {
  const _RawPayloadBlock({required this.payload});

  final String payload;

  @override
  Widget build(BuildContext context) {
    final pretty = _prettyJson(payload);
    return Container(
      width: double.infinity,
      constraints: const BoxConstraints(maxHeight: 180),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surface.withOpacity(0.72),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: Theme.of(context).dividerColor),
      ),
      child: SingleChildScrollView(
        child: SelectableText(
          pretty,
          style: Theme.of(
            context,
          ).textTheme.bodySmall?.copyWith(fontFamily: 'monospace'),
        ),
      ),
    );
  }
}

class _InteractionDetail {
  const _InteractionDetail({required this.icon, required this.text});

  final IconData icon;
  final String text;
}

class _InteractionOption {
  const _InteractionOption(this.label);

  final String label;
}

List<_InteractionDetail> _interactionDetails(
  TaskInteractionItem interaction,
  Map<String, dynamic> raw,
) {
  final details = <_InteractionDetail>[];
  void add(IconData icon, String label, dynamic value) {
    final text = value?.toString().trim() ?? '';
    if (text.isNotEmpty) {
      details.add(_InteractionDetail(icon: icon, text: '$label: $text'));
    }
  }

  add(Icons.info_outline, 'Reason', raw['reason']);
  add(Icons.build_outlined, 'Tool', raw['tool_name']);
  add(Icons.fingerprint, 'Tool use', raw['tool_use_id']);
  add(Icons.folder_open, 'Cwd', raw['cwd']);
  add(Icons.terminal, 'Command', raw['command']);
  final toolInput = raw['tool_input'];
  if (toolInput is Map<String, dynamic>) {
    add(Icons.terminal, 'Command', toolInput['command']);
    add(Icons.description_outlined, 'Description', toolInput['description']);
    add(Icons.insert_drive_file_outlined, 'File', toolInput['file_path']);
    add(Icons.insert_drive_file_outlined, 'Path', toolInput['path']);
  }
  add(Icons.folder_copy, 'Grant root', raw['grantRoot']);
  add(Icons.key, 'Session', interaction.agentSessionId);
  return details;
}

IconData _interactionIcon(String kind) => switch (kind) {
  'COMMAND_APPROVAL' => Icons.terminal,
  'FILE_APPROVAL' => Icons.edit_document,
  'PERMISSION_APPROVAL' => Icons.admin_panel_settings_outlined,
  _ => Icons.question_answer_outlined,
};

String _interactionKindLabel(String kind) => switch (kind) {
  'COMMAND_APPROVAL' => 'Command approval',
  'FILE_APPROVAL' => 'File approval',
  'PERMISSION_APPROVAL' => 'Permission approval',
  'USER_INPUT' => 'User input',
  _ => kind,
};

Map<String, dynamic> _firstInteractionQuestion(
  TaskInteractionItem interaction,
) {
  final questions = interaction.rawJson['questions'];
  if (questions is List && questions.isNotEmpty) {
    final first = questions.first;
    if (first is Map<String, dynamic>) {
      return first;
    }
  }
  return const {};
}

List<_InteractionOption> _questionOptions(Map<String, dynamic> question) {
  final options = question['options'];
  if (options is! List) {
    return const [];
  }
  return options
      .whereType<Map<String, dynamic>>()
      .map((item) => item['label']?.toString() ?? '')
      .where((label) => label.isNotEmpty)
      .map(_InteractionOption.new)
      .toList();
}

String _userInputPayload(TaskInteractionItem interaction, String answer) {
  final questions = interaction.rawJson['questions'];
  if (questions is! List) {
    return '';
  }
  final answers = <String, dynamic>{};
  for (final question in questions) {
    if (question is! Map<String, dynamic>) {
      continue;
    }
    final id = question['id']?.toString() ?? '';
    if (id.isEmpty) {
      continue;
    }
    answers[id] = {
      'answers': [answer],
    };
  }
  if (answers.isEmpty) {
    return '';
  }
  return jsonEncode({'answers': answers});
}

String _prettyJson(String value) {
  try {
    return const JsonEncoder.withIndent('  ').convert(jsonDecode(value));
  } catch (_) {
    return value;
  }
}

class _ConversationList extends StatelessWidget {
  const _ConversationList({required this.conversations});

  final List<ConversationItem> conversations;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border.all(color: Theme.of(context).dividerColor),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (conversations.isEmpty)
              Text('No entries', style: Theme.of(context).textTheme.bodySmall),
            ...conversations.map(
              (item) => Padding(
                padding: const EdgeInsets.only(bottom: 10),
                child: _ConversationMessageCard(item: item),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ConversationMessageCard extends StatelessWidget {
  const _ConversationMessageCard({required this.item});

  final ConversationItem item;

  @override
  Widget build(BuildContext context) {
    final format = _conversationFormat(item);
    final textTheme = Theme.of(context).textTheme;
    return DecoratedBox(
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Padding(
        padding: const EdgeInsets.all(10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Wrap(
              spacing: 8,
              runSpacing: 6,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                Text(
                  item.role,
                  style: textTheme.labelLarge?.copyWith(
                    color: Theme.of(context).colorScheme.primary,
                  ),
                ),
                if (format.label.isNotEmpty)
                  Chip(
                    label: Text(format.label),
                    visualDensity: VisualDensity.compact,
                    materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
              ],
            ),
            const SizedBox(height: 8),
            format.build(context, item.content),
          ],
        ),
      ),
    );
  }
}

class _ConversationFormat {
  const _ConversationFormat({required this.label, required this.build});

  final String label;
  final Widget Function(BuildContext context, String content) build;
}

_ConversationFormat _conversationFormat(ConversationItem item) {
  final value =
      (item.metadata['format'] ??
              item.metadata['contentType'] ??
              item.metadata['mimeType'] ??
              item.metadata['type'] ??
              '')
          .toLowerCase();
  if (value.contains('json') || _looksLikeJson(item.content)) {
    return _ConversationFormat(label: 'JSON', build: _buildJsonContent);
  }
  if (value.contains('markdown') || value == 'md') {
    return _ConversationFormat(label: 'Markdown', build: _buildTextContent);
  }
  if (value.contains('code') || value.contains('text/x-')) {
    return _ConversationFormat(label: 'Code', build: _buildMonospaceContent);
  }
  return const _ConversationFormat(label: '', build: _buildTextContent);
}

bool _looksLikeJson(String content) {
  final trimmed = content.trimLeft();
  return trimmed.startsWith('{') || trimmed.startsWith('[');
}

Widget _buildJsonContent(BuildContext context, String content) {
  try {
    const encoder = JsonEncoder.withIndent('  ');
    return _buildMonospaceContent(
      context,
      encoder.convert(jsonDecode(content)),
    );
  } catch (_) {
    return _buildMonospaceContent(context, content);
  }
}

Widget _buildTextContent(BuildContext context, String content) {
  return SelectableText(content, style: Theme.of(context).textTheme.bodySmall);
}

Widget _buildMonospaceContent(BuildContext context, String content) {
  return SelectableText(
    content,
    style: Theme.of(
      context,
    ).textTheme.bodySmall?.copyWith(fontFamily: 'monospace'),
  );
}

class _RuntimeTab extends StatelessWidget {
  const _RuntimeTab({required this.children, this.footer});

  final List<String> children;
  final Widget? footer;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: const EdgeInsets.only(top: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _RuntimeList(children: children),
          if (footer != null) ...[const SizedBox(height: 12), footer!],
        ],
      ),
    );
  }
}

class _ContinuationComposer extends StatefulWidget {
  const _ContinuationComposer({required this.task, required this.onContinue});

  final TaskItem task;
  final Future<void> Function(String message) onContinue;

  @override
  State<_ContinuationComposer> createState() => _ContinuationComposerState();
}

class _ContinuationComposerState extends State<_ContinuationComposer> {
  final TextEditingController _controller = TextEditingController();
  bool _sending = false;

  bool get _canContinue =>
      widget.task.status == 'COMPLETED' &&
      (widget.task.agentSessionId ?? '').isNotEmpty;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final message = _controller.text.trim();
    if (!_canContinue || message.isEmpty || _sending) {
      return;
    }
    setState(() => _sending = true);
    try {
      await widget.onContinue(message);
      _controller.clear();
    } finally {
      if (mounted) {
        setState(() => _sending = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Expanded(
          child: TextField(
            controller: _controller,
            enabled: _canContinue && !_sending,
            minLines: 1,
            maxLines: 3,
            decoration: const InputDecoration(
              labelText: 'Continue conversation',
              border: OutlineInputBorder(),
            ),
            onSubmitted: (_) => _send(),
          ),
        ),
        const SizedBox(width: 8),
        IconButton.filled(
          tooltip: 'Send continuation',
          onPressed: _canContinue && !_sending ? _send : null,
          icon: _sending
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Icon(Icons.send),
        ),
      ],
    );
  }
}

class _RuntimeList extends StatelessWidget {
  const _RuntimeList({required this.children});

  final List<String> children;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border.all(color: Theme.of(context).dividerColor),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (children.isEmpty)
              Text('No entries', style: Theme.of(context).textTheme.bodySmall),
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
    );
  }
}
