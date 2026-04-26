import 'package:flutter/material.dart';

import '../api_client.dart';
import '../board_status_groups.dart';
import '../models.dart';
import '../realtime_refresh.dart';
import '../widgets.dart';

class BoardPage extends StatefulWidget {
  const BoardPage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<BoardPage> createState() => _BoardPageState();
}

class _BoardPageState extends State<BoardPage> {
  String _view = 'KANBAN';
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
    super.dispose();
  }

  Future<BoardData> _load() async {
    final data = await widget.apiClient.fetchBoardData(_view);
    _lastData = data;
    return data;
  }

  void _reload() {
    if (!mounted) {
      return;
    }
    setState(() {
      _future = _load();
    });
  }

  void _subscribe() {
    _realtime = RealtimeRefreshController(
      events: widget.apiClient.subscribeDomainEvents(),
      reload: _reload,
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
          ],
          selected: {_view},
          onSelectionChanged: (values) {
            setState(() {
              _view = values.first;
              _future = _load();
            });
          },
        ),
        IconButton(
          tooltip: 'Refresh board',
          onPressed: _reload,
          icon: const Icon(Icons.refresh),
        ),
        FilledButton.icon(
          onPressed: () => _openTaskDialog(),
          icon: const Icon(Icons.add),
          label: const Text('New task'),
        ),
      ],
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
              onRetry: _reload,
            );
          }
          final board = data ?? BoardData.empty();
          return Stack(
            children: [
              _BoardContent(
                view: _view,
                data: board,
                onTaskSelected: _openTaskDetail,
                onTaskEdit: _openTaskDialog,
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
      _reload();
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
}

class _BoardContent extends StatelessWidget {
  const _BoardContent({
    required this.view,
    required this.data,
    required this.onTaskSelected,
    required this.onTaskEdit,
  });

  final String view;
  final BoardData data;
  final ValueChanged<TaskItem> onTaskSelected;
  final ValueChanged<TaskItem> onTaskEdit;

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
      'CALENDAR' => _CalendarView(
          items: data.calendarItems,
          onTaskSelected: onTaskSelected,
          onTaskEdit: onTaskEdit,
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
        final viewportWidth =
            constraints.maxWidth.isFinite ? constraints.maxWidth : 0.0;
        final viewportHeight =
            constraints.maxHeight.isFinite ? constraints.maxHeight : 0.0;
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
                  for (var index = 0;
                      index < widget.columns.length;
                      index++) ...[
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
              if ((task.workerId ?? '').isNotEmpty) ...[
                const SizedBox(height: 8),
                DetailText(icon: Icons.memory, text: task.workerId!),
              ],
            ],
          ),
        ),
      ),
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
  });

  final List<TaskItem> tasks;
  final List<ProjectItem> projects;
  final List<WorkerItem> workers;
  final ValueChanged<TaskItem> onTaskSelected;
  final ValueChanged<TaskItem> onTaskEdit;

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
              '${project ?? task.projectId}  ${worker ?? task.workerId ?? 'Unassigned'}',
            ),
            trailing: Wrap(
              spacing: 8,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                StatusPill(value: task.status),
                IconButton(
                  tooltip: 'Edit task',
                  onPressed: () => onTaskEdit(task),
                  icon: const Icon(Icons.edit_outlined),
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

bool _workerAllowsProject(WorkerItem worker, String? projectId) {
  if (projectId == null) {
    return false;
  }
  return worker.projectBindingMode == 'ALL_PROJECTS' ||
      worker.boundProjectIds.contains(projectId);
}

bool _workerAvailableForProject(WorkerItem worker, String? projectId) {
  return worker.status == 'ONLINE' &&
      (worker.currentTaskId ?? '').isEmpty &&
      worker.supportedAgents.isNotEmpty &&
      _workerAllowsProject(worker, projectId);
}

String _agentLabel(String agent) => switch (agent) {
      'codex' => 'Codex',
      'claude' => 'Claude',
      _ => agent,
    };

class _CalendarView extends StatelessWidget {
  const _CalendarView({
    required this.items,
    required this.onTaskSelected,
    required this.onTaskEdit,
  });

  final List<BoardCalendarItemData> items;
  final ValueChanged<TaskItem> onTaskSelected;
  final ValueChanged<TaskItem> onTaskEdit;

  @override
  Widget build(BuildContext context) {
    return ListView.separated(
      padding: const EdgeInsets.all(16),
      itemBuilder: (context, index) {
        final item = items[index];
        return Card(
          child: ListTile(
            leading: const Icon(Icons.event),
            title: Text(item.task.title),
            subtitle: Text(item.date),
            trailing: Wrap(
              spacing: 8,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                StatusPill(value: item.status),
                IconButton(
                  tooltip: 'Edit task',
                  onPressed: () => onTaskEdit(item.task),
                  icon: const Icon(Icons.edit_outlined),
                ),
              ],
            ),
            onTap: () => onTaskSelected(item.task),
          ),
        );
      },
      separatorBuilder: (context, index) => const SizedBox(height: 8),
      itemCount: items.length,
    );
  }
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
  String? projectId = task?.projectId ??
      (data.projects.isNotEmpty ? data.projects.first.id : null);
  String selectedWorkerId = task?.workerId ?? '';
  String? selectedAgent =
      (task?.agentType ?? '').isEmpty ? null : task!.agentType;

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
        final canSave = title.text.trim().isNotEmpty &&
            projectId != null &&
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
                  TextField(
                    controller: baseBranch,
                    decoration: const InputDecoration(
                      labelText: 'Base branch',
                    ),
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
                  ],
                  const SizedBox(height: 12),
                  TextField(
                    controller: preCommands,
                    decoration:
                        const InputDecoration(labelText: 'Pre commands'),
                    minLines: 2,
                    maxLines: 5,
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: postCommands,
                    decoration:
                        const InputDecoration(labelText: 'Post commands'),
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
                          baseBranch: baseBranch.text.trim().isEmpty
                              ? 'main'
                              : baseBranch.text.trim(),
                          preCommands: stringList(preCommands.text),
                          postCommands: stringList(postCommands.text),
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
                            baseBranch: baseBranch.text.trim().isEmpty
                                ? 'main'
                                : baseBranch.text.trim(),
                            preCommands: stringList(preCommands.text),
                            postCommands: stringList(postCommands.text),
                            createdAt: task.createdAt,
                            updatedAt: task.updatedAt,
                            workerId: task.workerId,
                            worktreePath: task.worktreePath,
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
    return AlertDialog(
      title: Text(widget.task.title),
      content: SizedBox(
        width: 820,
        height: 560,
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
                _TaskDetailBody(detail: detail!, onContinue: _continueTask),
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
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(false),
          child: const Text('Close'),
        ),
        TextButton.icon(
          onPressed: _assign,
          icon: const Icon(Icons.person_add_alt_1),
          label: const Text('Assign'),
        ),
        TextButton.icon(
          onPressed: () =>
              _run(() => widget.apiClient.startTask(widget.task.id!)),
          icon: const Icon(Icons.play_arrow),
          label: const Text('Start'),
        ),
        TextButton.icon(
          onPressed: () =>
              _run(() => widget.apiClient.interruptTask(widget.task.id!)),
          icon: const Icon(Icons.stop),
          label: const Text('Interrupt'),
        ),
        TextButton.icon(
          onPressed: () =>
              _run(() => widget.apiClient.retryTask(widget.task.id!)),
          icon: const Icon(Icons.replay),
          label: const Text('Retry'),
        ),
        FilledButton.icon(
          onPressed: () async {
            final confirmed = await confirmAction(
              context,
              title: 'Archive task',
              message: 'Archive "${widget.task.title}"?',
              confirmLabel: 'Archive',
            );
            if (!confirmed) {
              return;
            }
            await _run(() => widget.apiClient.archiveTask(widget.task.id!));
          },
          icon: const Icon(Icons.archive_outlined),
          label: const Text('Archive'),
        ),
      ],
    );
  }

  Future<void> _run(Future<void> Function() action) async {
    await action();
    if (mounted) {
      Navigator.of(context).pop(true);
    }
  }

  Future<void> _continueTask(String message) async {
    await widget.apiClient.continueTask(widget.task.id!, message);
    _reload();
  }

  Future<void> _assign() async {
    final candidates = widget.boardData.workers.where((worker) {
      final supports = widget.task.agentType.isEmpty ||
          worker.supportedAgents.contains(widget.task.agentType);
      final projectMatches = worker.projectBindingMode == 'ALL_PROJECTS' ||
          worker.boundProjectIds.contains(widget.task.projectId);
      return worker.status == 'ONLINE' &&
          (worker.currentTaskId ?? '').isEmpty &&
          supports &&
          worker.supportedAgents.isNotEmpty &&
          projectMatches;
    }).toList();
    if (candidates.isEmpty) {
      return;
    }
    String selected = candidates.first.id;
    String selectedAgent = widget.task.agentType.isEmpty
        ? candidates.first.supportedAgents.first
        : widget.task.agentType;
    final workerId = await showDialog<String>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setState) {
          final worker = _workerById(candidates, selected) ?? candidates.first;
          if (!worker.supportedAgents.contains(selectedAgent)) {
            selectedAgent = worker.supportedAgents.first;
          }
          return AlertDialog(
            title: const Text('Assign worker'),
            content: Column(
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
                        _workerById(candidates, selected) ?? candidates.first;
                    selectedAgent = widget.task.agentType.isEmpty
                        ? worker.supportedAgents.first
                        : widget.task.agentType;
                  }),
                ),
                if (widget.task.agentType.isEmpty) ...[
                  const SizedBox(height: 12),
                  SegmentedButton<String>(
                    segments: worker.supportedAgents
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
                    onSelectionChanged: (values) =>
                        setState(() => selectedAgent = values.first),
                  ),
                ],
              ],
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(context).pop(),
                child: const Text('Cancel'),
              ),
              FilledButton(
                onPressed: () => Navigator.of(context).pop(selected),
                child: const Text('Assign'),
              ),
            ],
          );
        },
      ),
    );
    if (workerId == null) {
      return;
    }
    await _run(
      () => widget.apiClient.assignWorker(
        widget.task.id!,
        workerId,
        agentType: widget.task.agentType.isEmpty ? selectedAgent : null,
      ),
    );
  }
}

class _TaskDetailBody extends StatelessWidget {
  const _TaskDetailBody({required this.detail, required this.onContinue});

  final TaskDetailData detail;
  final Future<void> Function(String message) onContinue;

  @override
  Widget build(BuildContext context) {
    final task = detail.task;
    return DefaultTabController(
      initialIndex: 0,
      length: 3,
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
              DetailText(icon: Icons.folder_copy, text: task.projectId),
              if ((task.workerId ?? '').isNotEmpty)
                DetailText(icon: Icons.memory, text: task.workerId!),
            ],
          ),
          const SizedBox(height: 16),
          const TabBar(
            tabs: [
              Tab(
                icon: Icon(Icons.chat_bubble_outline),
                text: 'Conversation',
              ),
              Tab(icon: Icon(Icons.article_outlined), text: 'Logs'),
              Tab(
                icon: Icon(Icons.event_note_outlined),
                text: 'Domain events',
              ),
            ],
          ),
          Expanded(
            child: TabBarView(
              children: [
                _RuntimeTab(
                  footer: _ContinuationComposer(
                    task: task,
                    onContinue: onContinue,
                  ),
                  children: detail.conversations
                      .map((item) => '${item.role}: ${item.content}')
                      .toList(),
                ),
                _RuntimeTab(
                  children: detail.logs
                      .map((item) => '[${item.stream}] ${item.content}')
                      .toList(),
                ),
                _RuntimeTab(
                  children: detail.events
                      .map(
                        (item) =>
                            '${item.eventType} v${item.aggregateVersion}: ${item.payload}',
                      )
                      .toList(),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
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
