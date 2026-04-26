import 'dart:async';

import 'package:flutter/material.dart';

import '../api_client.dart';
import '../models.dart';
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
  StreamSubscription<DomainEventItem>? _subscription;
  Timer? _refreshTimer;

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
      _subscription?.cancel();
      _future = _load();
      _subscribe();
    }
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    _subscription?.cancel();
    super.dispose();
  }

  Future<BoardData> _load() async {
    final data = await widget.apiClient.fetchBoardData(_view);
    _lastData = data;
    return data;
  }

  void _reload() {
    setState(() {
      _future = _load();
    });
  }

  void _scheduleReload() {
    _refreshTimer?.cancel();
    _refreshTimer = Timer(const Duration(milliseconds: 300), () {
      if (mounted) {
        _reload();
      }
    });
  }

  void _subscribe() {
    _subscription = widget.apiClient.subscribeDomainEvents().listen((event) {
      if (event.aggregateType == 'Task' ||
          event.aggregateType == 'Worker' ||
          event.aggregateType == 'Project') {
        _scheduleReload();
      }
    });
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
        columns: data.columns,
        onTaskSelected: onTaskSelected,
        onTaskEdit: onTaskEdit,
      ),
    };
  }
}

class _KanbanView extends StatelessWidget {
  const _KanbanView({
    required this.columns,
    required this.onTaskSelected,
    required this.onTaskEdit,
  });

  final List<BoardColumnData> columns;
  final ValueChanged<TaskItem> onTaskSelected;
  final ValueChanged<TaskItem> onTaskEdit;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.all(16),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: columns.map((column) {
          return SizedBox(
            width: 260,
            child: Padding(
              padding: const EdgeInsets.only(right: 12),
              child: DecoratedBox(
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
                              column.title,
                              style: Theme.of(context).textTheme.titleSmall,
                            ),
                          ),
                          StatusPill(value: column.tasks.length.toString()),
                        ],
                      ),
                      const SizedBox(height: 10),
                      if (column.tasks.isEmpty)
                        Text(
                          'No tasks',
                          style: Theme.of(context).textTheme.bodySmall,
                        ),
                      ...column.tasks.map(
                        (task) => Padding(
                          padding: const EdgeInsets.only(bottom: 8),
                          child: _TaskCard(
                            task: task,
                            onTap: () => onTaskSelected(task),
                            onEdit: () => onTaskEdit(task),
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          );
        }).toList(),
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
  final targetBranch = TextEditingController(text: task?.targetBranch ?? '');
  final preCommands = TextEditingController(
    text: task?.preCommands.join('\n') ?? '',
  );
  final postCommands = TextEditingController(
    text: task?.postCommands.join('\n') ?? '',
  );
  String? projectId =
      task?.projectId ??
      (data.projects.isNotEmpty ? data.projects.first.id : null);
  String agent = task?.agentType ?? 'codex';
  return showDialog<bool>(
    context: context,
    builder: (context) => StatefulBuilder(
      builder: (context, setState) => AlertDialog(
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
                  onChanged: (value) => setState(() => projectId = value),
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Expanded(
                      child: TextField(
                        controller: baseBranch,
                        decoration: const InputDecoration(
                          labelText: 'Base branch',
                        ),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: TextField(
                        controller: targetBranch,
                        decoration: const InputDecoration(
                          labelText: 'Target branch',
                        ),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                SegmentedButton<String>(
                  segments: const [
                    ButtonSegment(
                      value: 'codex',
                      icon: Icon(Icons.terminal),
                      label: Text('Codex'),
                    ),
                    ButtonSegment(
                      value: 'claude',
                      icon: Icon(Icons.chat_bubble_outline),
                      label: Text('Claude'),
                    ),
                  ],
                  selected: {agent},
                  onSelectionChanged: (values) =>
                      setState(() => agent = values.first),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: preCommands,
                  decoration: const InputDecoration(labelText: 'Pre commands'),
                  minLines: 2,
                  maxLines: 5,
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: postCommands,
                  decoration: const InputDecoration(labelText: 'Post commands'),
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
            onPressed: () async {
              if (title.text.trim().isEmpty || projectId == null) {
                return;
              }
              if (task == null) {
                await apiClient.createTask(
                  title: title.text.trim(),
                  description: description.text.trim(),
                  projectId: projectId!,
                  agentType: agent,
                  baseBranch: baseBranch.text.trim().isEmpty
                      ? 'main'
                      : baseBranch.text.trim(),
                  targetBranch: targetBranch.text.trim(),
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
                    agentType: agent,
                    baseBranch: baseBranch.text.trim().isEmpty
                        ? 'main'
                        : baseBranch.text.trim(),
                    targetBranch: targetBranch.text.trim().isEmpty
                        ? 'task/${task.id}'
                        : targetBranch.text.trim(),
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
            },
            child: const Text('Save'),
          ),
        ],
      ),
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

  @override
  void initState() {
    super.initState();
    _future = widget.apiClient.fetchTaskDetail(widget.task.id!);
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
            if (snapshot.connectionState != ConnectionState.done) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snapshot.hasError) {
              return ErrorView(
                message: snapshot.error.toString(),
                onRetry: () {
                  setState(() {
                    _future = widget.apiClient.fetchTaskDetail(widget.task.id!);
                  });
                },
              );
            }
            final detail = snapshot.data!;
            return _TaskDetailBody(detail: detail);
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

  Future<void> _assign() async {
    final candidates = widget.boardData.workers.where((worker) {
      final supports = worker.supportedAgents.contains(widget.task.agentType);
      final projectMatches =
          worker.projectBindingMode == 'ALL_PROJECTS' ||
          worker.boundProjectIds.contains(widget.task.projectId);
      return worker.status == 'ONLINE' &&
          (worker.currentTaskId ?? '').isEmpty &&
          supports &&
          projectMatches;
    }).toList();
    if (candidates.isEmpty) {
      return;
    }
    String selected = candidates.first.id;
    final workerId = await showDialog<String>(
      context: context,
      builder: (context) => StatefulBuilder(
        builder: (context, setState) => AlertDialog(
          title: const Text('Assign worker'),
          content: DropdownButtonFormField<String>(
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
            onChanged: (value) => setState(() => selected = value ?? selected),
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
        ),
      ),
    );
    if (workerId == null) {
      return;
    }
    await _run(() => widget.apiClient.assignWorker(widget.task.id!, workerId));
  }
}

class _TaskDetailBody extends StatelessWidget {
  const _TaskDetailBody({required this.detail});

  final TaskDetailData detail;

  @override
  Widget build(BuildContext context) {
    final task = detail.task;
    return SingleChildScrollView(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(
            spacing: 12,
            runSpacing: 8,
            children: [
              StatusPill(value: task.status),
              DetailText(icon: Icons.terminal, text: task.agentType),
              DetailText(icon: Icons.folder_copy, text: task.projectId),
              if ((task.workerId ?? '').isNotEmpty)
                DetailText(icon: Icons.memory, text: task.workerId!),
              if ((task.result ?? '').isNotEmpty)
                DetailText(icon: Icons.flag, text: task.result!),
            ],
          ),
          const SizedBox(height: 16),
          _RuntimeList(
            title: 'Logs',
            icon: Icons.article_outlined,
            children: detail.logs
                .map((item) => '[${item.stream}] ${item.content}')
                .toList(),
          ),
          const SizedBox(height: 12),
          _RuntimeList(
            title: 'Conversation',
            icon: Icons.chat_bubble_outline,
            children: detail.conversations
                .map((item) => '${item.role}: ${item.content}')
                .toList(),
          ),
          const SizedBox(height: 12),
          _RuntimeList(
            title: 'Domain events',
            icon: Icons.event_note_outlined,
            children: detail.events
                .map(
                  (item) =>
                      '${item.eventType} v${item.aggregateVersion}: ${item.payload}',
                )
                .toList(),
          ),
        ],
      ),
    );
  }
}

class _RuntimeList extends StatelessWidget {
  const _RuntimeList({
    required this.title,
    required this.icon,
    required this.children,
  });

  final String title;
  final IconData icon;
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
            Row(
              children: [
                Icon(icon, size: 18),
                const SizedBox(width: 8),
                Text(title, style: Theme.of(context).textTheme.titleSmall),
              ],
            ),
            const SizedBox(height: 8),
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
