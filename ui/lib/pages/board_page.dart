import 'dart:math' as math;

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
          child: Text(
            text,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
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
              '${project ?? task.projectId}  ${worker ?? task.workerId ?? 'Unassigned'}  ${_taskDateRangeLabel(task)}',
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

List<Widget> _agentConfigDetailWidgets(TaskItem task) {
  final config = task.agentConfig;
  if (config.isEmpty) {
    return const [];
  }
  final widgets = <Widget>[];
  if (config.workMode.isNotEmpty) {
    widgets.add(
      DetailText(icon: Icons.rule_folder_outlined, text: _enumLabel(config.workMode)),
    );
  }
  if (task.agentType == 'codex') {
    final codex = config.codex;
    if (codex.model.isNotEmpty) {
      widgets.add(DetailText(icon: Icons.smart_toy_outlined, text: codex.model));
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
        DetailText(icon: Icons.inventory_2_outlined, text: _enumLabel(codex.sandboxMode)),
      );
    }
    if (codex.approvalPolicy.isNotEmpty) {
      widgets.add(
        DetailText(icon: Icons.verified_user_outlined, text: _enumLabel(codex.approvalPolicy)),
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
      widgets.add(DetailText(icon: Icons.smart_toy_outlined, text: claude.model));
    }
    if (claude.effort.isNotEmpty) {
      widgets.add(
        DetailText(icon: Icons.psychology_alt_outlined, text: _enumLabel(claude.effort)),
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
  _AgentConfigDraft({
    required AgentExecutionConfigItem config,
  })  : workMode = config.workMode,
        codexModel = TextEditingController(text: config.codex.model),
        codexReasoningEffort = config.codex.reasoningEffort,
        codexSandboxMode = config.codex.sandboxMode,
        codexApprovalPolicy = config.codex.approvalPolicy,
        codexFullAuto = config.codex.fullAuto,
        codexBypassApprovalsAndSandbox =
            config.codex.bypassApprovalsAndSandbox,
        claudeModel = TextEditingController(text: config.claude.model),
        claudeEffort = config.claude.effort,
        claudePermissionMode = config.claude.permissionMode;

  factory _AgentConfigDraft.empty() => _AgentConfigDraft(
        config: const AgentExecutionConfigItem(),
      );

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
              child: Text(
                entry.value,
                overflow: TextOverflow.ellipsis,
              ),
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
  const _CalendarView({
    required this.tasks,
    required this.onTaskSelected,
  });

  final List<TaskItem> tasks;
  final ValueChanged<TaskItem> onTaskSelected;

  @override
  State<_CalendarView> createState() => _CalendarViewState();
}

class _CalendarViewState extends State<_CalendarView> {
  final TextEditingController _searchController = TextEditingController();
  _CalendarMode _mode = _CalendarMode.month;
  late DateTime _focusedDate;
  String _searchQuery = '';

  @override
  void initState() {
    super.initState();
    final ranges = _taskRanges(widget.tasks);
    _focusedDate = ranges.isEmpty ? _todayTaskDate() : ranges.first.start;
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  @override
  void didUpdateWidget(covariant _CalendarView oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.tasks.isEmpty && widget.tasks.isNotEmpty) {
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
          searchController: _searchController,
          onModeChanged: (mode) => setState(() => _mode = mode),
          onSearchChanged: _setSearchQuery,
          onPrevious: () => _moveFocus(-1),
          onToday: () => setState(() => _focusedDate = _todayTaskDate()),
          onNext: () => _moveFocus(1),
        ),
        Expanded(
          child: ranges.isEmpty
              ? EmptyState(
                  icon: Icons.search_off,
                  title: _searchQuery.trim().isEmpty
                      ? 'No scheduled tasks'
                      : 'No matching tasks',
                  message: _searchQuery.trim().isEmpty
                      ? 'Create a task with dates to fill the calendar.'
                      : 'Clear search or try another term.',
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
    final ranges = _taskRanges(widget.tasks);
    final query = _searchQuery.trim().toLowerCase();
    if (query.isEmpty) {
      return ranges;
    }
    return ranges.where((range) {
      final task = range.task;
      return task.title.toLowerCase().contains(query) ||
          task.description.toLowerCase().contains(query) ||
          task.status.toLowerCase().contains(query);
    }).toList();
  }

  void _setSearchQuery(String value) {
    setState(() {
      _searchQuery = value;
      final query = value.trim().toLowerCase();
      if (query.isEmpty) {
        return;
      }
      final matches = _taskRanges(widget.tasks).where((range) {
        final task = range.task;
        return task.title.toLowerCase().contains(query) ||
            task.description.toLowerCase().contains(query) ||
            task.status.toLowerCase().contains(query);
      }).toList();
      if (matches.isNotEmpty) {
        _focusedDate = matches.first.start;
      }
    });
  }

  void _moveFocus(int delta) {
    setState(() {
      _focusedDate = switch (_mode) {
        _CalendarMode.day => _dateOnly(_focusedDate.add(Duration(days: delta))),
        _CalendarMode.week =>
          _dateOnly(_focusedDate.add(Duration(days: delta * 7))),
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
    required this.searchController,
    required this.onModeChanged,
    required this.onSearchChanged,
    required this.onPrevious,
    required this.onToday,
    required this.onNext,
  });

  final _CalendarMode mode;
  final String title;
  final TextEditingController searchController;
  final ValueChanged<_CalendarMode> onModeChanged;
  final ValueChanged<String> onSearchChanged;
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
      SizedBox(
        width: 260,
        height: 40,
        child: TextField(
          key: const ValueKey('calendar-search-field'),
          controller: searchController,
          onChanged: onSearchChanged,
          decoration: const InputDecoration(
            prefixIcon: Icon(Icons.search),
            hintText: 'Search',
            contentPadding: EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          ),
        ),
      ),
      Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            tooltip: 'Previous period',
            onPressed: onPrevious,
            icon: const Icon(Icons.chevron_left),
          ),
          OutlinedButton(
            onPressed: onToday,
            child: const Text('Today'),
          ),
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
        border: Border(bottom: BorderSide(color: Theme.of(context).dividerColor)),
      ),
      child: LayoutBuilder(
        builder: (context, constraints) {
          final titleWidget = Text(
            title,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(context).textTheme.headlineSmall?.copyWith(
                  fontWeight: FontWeight.w700,
                ),
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
    for (var date = gridStart;
        !date.isAfter(gridEnd);
        date = date.add(const Duration(days: 7))) {
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
        border: Border(bottom: BorderSide(color: Theme.of(context).dividerColor)),
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
    final weekRanges =
        ranges.where((range) => range.overlaps(weekStart, weekEnd)).toList();

    return LayoutBuilder(
      builder: (context, constraints) {
        final width = constraints.maxWidth.isFinite ? constraints.maxWidth : 0.0;
        final height =
            constraints.maxHeight.isFinite ? constraints.maxHeight : 150.0;
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
                        inPrimaryMonth: primaryMonth == 0 ||
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
    final segmentStart = range.start.isBefore(weekStart) ? weekStart : range.start;
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
              ? BoxDecoration(
                  color: scheme.error,
                  shape: BoxShape.circle,
                )
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
    final monthRanges =
        ranges.where((range) => range.overlaps(monthStart, monthEnd)).toList();
    final busyDays = <int>{};
    for (final range in monthRanges) {
      final start = range.start.isBefore(monthStart) ? monthStart : range.start;
      final end = range.end.isAfter(monthEnd) ? monthEnd : range.end;
      for (var date = start;
          !date.isAfter(end);
          date = date.add(const Duration(days: 1))) {
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
                                ? Theme.of(context)
                                    .colorScheme
                                    .error
                                    .withOpacity(0.12)
                                : null,
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: Text(
                        inMonth ? date.day.toString() : '',
                        style: Theme.of(context).textTheme.labelSmall?.copyWith(
                              color: busy
                                  ? color
                                  : Theme.of(context)
                                      .colorScheme
                                      .onSurfaceVariant,
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
  String? projectId = task?.projectId ??
      (data.projects.isNotEmpty ? data.projects.first.id : null);
  String selectedWorkerId = task?.workerId ?? '';
  String? selectedAgent =
      (task?.agentType ?? '').isEmpty ? null : task!.agentType;
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
        final canSave = title.text.trim().isNotEmpty &&
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
                _TaskDetailBody(
                  detail: detail!,
                  projects: widget.boardData.projects,
                  onContinue: _continueTask,
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
    final task = _lastDetail?.task ?? widget.task;
    final currentWorkerId = task.workerId ?? '';
    final candidates = widget.boardData.workers.where((worker) {
      final supports = task.agentType.isEmpty ||
          worker.supportedAgents.contains(task.agentType);
      final projectMatches = worker.projectBindingMode == 'ALL_PROJECTS' ||
          worker.boundProjectIds.contains(task.projectId);
      return worker.status == 'ONLINE' &&
          supports &&
          worker.supportedAgents.isNotEmpty &&
          projectMatches;
    }).toList();
    if (candidates.isEmpty) {
      return;
    }
    String selected = currentWorkerId.isNotEmpty &&
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
                        final worker = _workerById(candidates, selected) ??
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

class _TaskDetailBody extends StatelessWidget {
  const _TaskDetailBody({
    required this.detail,
    required this.projects,
    required this.onContinue,
  });

  final TaskDetailData detail;
  final List<ProjectItem> projects;
  final Future<void> Function(String message) onContinue;

  @override
  Widget build(BuildContext context) {
    final task = detail.task;
    final projectName = _projectName(projects, task.projectId);
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
