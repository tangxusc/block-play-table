import 'package:flutter/material.dart';

import '../api_client.dart';
import '../models.dart';
import '../realtime_refresh.dart';
import '../widgets.dart';

class WorkersPage extends StatefulWidget {
  const WorkersPage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<WorkersPage> createState() => _WorkersPageState();
}

class _WorkersPageState extends State<WorkersPage> {
  PageRequest _page = const PageRequest();
  late Future<_WorkersData> _future;
  _WorkersData? _lastData;
  RealtimeRefreshController? _realtime;

  @override
  void initState() {
    super.initState();
    _future = _load();
    _realtime = RealtimeRefreshController(
      events: widget.apiClient.subscribeDomainEvents(),
      reload: () => _reload(),
      shouldReload: (event) =>
          event.aggregateType == 'Worker' || event.aggregateType == 'Project',
    );
  }

  @override
  void dispose() {
    _realtime?.dispose();
    super.dispose();
  }

  Future<_WorkersData> _load() async {
    final results = await Future.wait([
      widget.apiClient.fetchWorkersPage(page: _page),
      widget.apiClient.fetchProjects(),
    ]);
    var workersPage = results[0] as PagedResult<WorkerItem>;
    if (workersPage.items.isEmpty &&
        workersPage.totalCount > 0 &&
        _page.offset > 0) {
      final corrected = _page.withOffset(_page.lastOffset(workersPage.totalCount));
      if (corrected.offset != _page.offset) {
        _page = corrected;
        workersPage = await widget.apiClient.fetchWorkersPage(page: _page);
      }
    }
    final data = _WorkersData(
      workersPage: workersPage,
      projects: results[1] as List<ProjectItem>,
    );
    _lastData = data;
    return data;
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

  @override
  Widget build(BuildContext context) {
    return PageScaffold(
      title: 'Workers',
      icon: Icons.memory,
      actions: [
        IconButton(
          tooltip: 'Refresh workers',
          onPressed: () => _reload(),
          icon: const Icon(Icons.refresh),
        ),
        FilledButton.icon(
          onPressed: () => _openWorkerDialog(),
          icon: const Icon(Icons.add),
          label: const Text('New worker'),
        ),
      ],
      child: FutureBuilder<_WorkersData>(
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
          final items = data?.workersPage.items ?? const <WorkerItem>[];
          if (items.isEmpty) {
            return const EmptyState(
              icon: Icons.memory_outlined,
              title: 'No workers',
              message: 'Create or register a worker to execute tasks.',
            );
          }
          return Stack(
            children: [
              Column(
                children: [
                  Expanded(
                    child: ListView.separated(
                      padding: const EdgeInsets.all(16),
                      itemBuilder: (context, index) {
                        final worker = items[index];
                        return Card(
                          child: ListTile(
                            leading: const Icon(Icons.memory_outlined),
                            title: Text(worker.name),
                            subtitle: Text(
                              '${worker.workDir}\n${worker.supportedAgents.join(', ')}  ${worker.projectBindingMode}',
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                            ),
                            trailing: Wrap(
                              spacing: 8,
                              crossAxisAlignment: WrapCrossAlignment.center,
                              children: [
                                StatusPill(value: worker.status),
                                Text(
                                  worker.currentTaskIds.isEmpty
                                      ? 'No running tasks'
                                      : '${worker.currentTaskIds.length} running',
                                ),
                                IconButton(
                                  tooltip: 'Edit worker',
                                  onPressed: () => _openWorkerDialog(worker),
                                  icon: const Icon(Icons.edit_outlined),
                                ),
                                IconButton(
                                  tooltip: 'Enable worker',
                                  onPressed: () => _run(
                                    () => widget.apiClient
                                        .enableWorker(worker.id),
                                  ),
                                  icon: const Icon(Icons.toggle_on_outlined),
                                ),
                                IconButton(
                                  tooltip: 'Disable worker',
                                  onPressed: () => _run(
                                    () => widget.apiClient
                                        .disableWorker(worker.id),
                                  ),
                                  icon: const Icon(Icons.toggle_off_outlined),
                                ),
                                IconButton(
                                  tooltip: 'Delete worker',
                                  onPressed: worker.currentTaskIds.isEmpty
                                      ? () => _deleteWorker(worker)
                                      : null,
                                  icon: const Icon(Icons.delete_outline),
                                ),
                              ],
                            ),
                          ),
                        );
                      },
                      separatorBuilder: (context, index) =>
                          const SizedBox(height: 8),
                      itemCount: items.length,
                    ),
                  ),
                  PaginationBar(
                    page: _page,
                    totalCount: data?.workersPage.totalCount ?? 0,
                    onPageChanged: _goToPage,
                  ),
                ],
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

  Future<void> _openWorkerDialog([WorkerItem? worker]) async {
    final data = _lastData;
    final saved = await showWorkerFormDialog(
      context,
      apiClient: widget.apiClient,
      projects: data?.projects ?? const [],
      worker: worker,
    );
    if (saved == true) {
      _reload(firstPage: worker == null);
    }
  }

  Future<void> _run(Future<void> Function() action) async {
    await action();
    _reload();
  }

  Future<void> _deleteWorker(WorkerItem worker) async {
    final confirmed = await confirmAction(
      context,
      title: 'Delete worker',
      message: 'Delete "${worker.name}"?',
      confirmLabel: 'Delete',
    );
    if (!confirmed) {
      return;
    }
    await widget.apiClient.deleteWorker(worker.id);
    _reload();
  }
}

class _WorkersData {
  const _WorkersData({required this.workersPage, required this.projects});

  final PagedResult<WorkerItem> workersPage;
  final List<ProjectItem> projects;
}

Future<bool?> showWorkerFormDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required List<ProjectItem> projects,
  WorkerItem? worker,
}) {
  final id = TextEditingController(text: worker?.id ?? '');
  final name = TextEditingController(text: worker?.name ?? 'local-worker');
  final workDir = TextEditingController(
    text: worker?.workDir ?? './worker-data',
  );
  final startupCommand = TextEditingController(
    text: worker?.startupCommand ?? '',
  );
  final agents = <String>{
    ...(worker?.supportedAgents ?? const ['codex']),
  };
  var bindingMode = worker?.projectBindingMode ?? 'ALL_PROJECTS';
  final boundProjectIds = <String>{...(worker?.boundProjectIds ?? const [])};
  final envByAgent = <String, List<EnvVarItem>>{
    for (final group
        in worker?.agentRuntimeEnv ?? const <WorkerAgentRuntimeEnvItem>[])
      group.agentType: List<EnvVarItem>.from(group.vars),
  };
  var selectedEnvAgent = agents.first;

  List<WorkerAgentRuntimeEnvItem> agentRuntimeEnvInput() => agents
      .map(
        (agent) => WorkerAgentRuntimeEnvItem(
          agentType: agent,
          vars: List<EnvVarItem>.from(envByAgent[agent] ?? const []),
        ),
      )
      .where((group) => group.vars.isNotEmpty)
      .toList();

  return showDialog<bool>(
    context: context,
    builder: (context) => StatefulBuilder(
      builder: (context, setState) => AlertDialog(
        title: Text(worker == null ? 'Create worker' : 'Edit worker'),
        content: SizedBox(
          width: 680,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: TextField(
                        controller: id,
                        enabled: worker == null,
                        decoration: const InputDecoration(
                          labelText: 'Worker ID',
                        ),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: TextField(
                        controller: name,
                        decoration: const InputDecoration(labelText: 'Name'),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: workDir,
                  decoration: const InputDecoration(labelText: 'Work dir'),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: startupCommand,
                  decoration: const InputDecoration(
                    labelText: 'Startup command',
                  ),
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
                  selected: agents,
                  multiSelectionEnabled: true,
                  emptySelectionAllowed: false,
                  onSelectionChanged: (values) => setState(() {
                    agents
                      ..clear()
                      ..addAll(values);
                    if (!agents.contains(selectedEnvAgent)) {
                      selectedEnvAgent = agents.first;
                    }
                  }),
                ),
                const SizedBox(height: 18),
                Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    'Runtime environment',
                    style: Theme.of(context).textTheme.titleSmall,
                  ),
                ),
                const SizedBox(height: 8),
                SegmentedButton<String>(
                  segments: agents
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
                  selected: {selectedEnvAgent},
                  onSelectionChanged: (values) =>
                      setState(() => selectedEnvAgent = values.first),
                ),
                const SizedBox(height: 8),
                _WorkerEnvEditor(
                  agent: selectedEnvAgent,
                  vars: envByAgent[selectedEnvAgent] ?? const [],
                  onAdd: () async {
                    final item = await showWorkerEnvVarDialog(context);
                    if (item == null) {
                      return;
                    }
                    setState(() {
                      final vars = [
                        ...(envByAgent[selectedEnvAgent] ?? const [])
                            .where((existing) => existing.key != item.key),
                        item,
                      ];
                      envByAgent[selectedEnvAgent] = vars;
                    });
                  },
                  onEdit: (item) async {
                    final result =
                        await showWorkerEnvVarDialog(context, item: item);
                    if (result == null) {
                      return;
                    }
                    setState(() {
                      final vars = [
                        ...(envByAgent[selectedEnvAgent] ?? const [])
                            .where((existing) => existing.key != item.key),
                        result,
                      ];
                      envByAgent[selectedEnvAgent] = vars;
                    });
                  },
                  onRemove: (item) => setState(() {
                    envByAgent[selectedEnvAgent] =
                        (envByAgent[selectedEnvAgent] ?? const [])
                            .where((existing) => existing.key != item.key)
                            .toList();
                  }),
                ),
                const SizedBox(height: 12),
                DropdownButtonFormField<String>(
                  value: bindingMode,
                  decoration: const InputDecoration(
                    labelText: 'Project binding',
                  ),
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
                      setState(() => bindingMode = value ?? 'ALL_PROJECTS'),
                ),
                const SizedBox(height: 12),
                Align(
                  alignment: Alignment.centerLeft,
                  child: Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: projects
                        .map(
                          (project) => FilterChip(
                            label: Text(project.name),
                            selected: boundProjectIds.contains(project.id),
                            onSelected: bindingMode == 'ALL_PROJECTS'
                                ? null
                                : (selected) => setState(() {
                                      if (selected) {
                                        boundProjectIds.add(project.id);
                                      } else {
                                        boundProjectIds.remove(project.id);
                                      }
                                    }),
                          ),
                        )
                        .toList(),
                  ),
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
              if (name.text.trim().isEmpty || workDir.text.trim().isEmpty) {
                return;
              }
              if (worker == null) {
                await apiClient.createWorker(
                  id: id.text.trim(),
                  name: name.text.trim(),
                  supportedAgents: agents.toList(),
                  workDir: workDir.text.trim(),
                  startupCommand: startupCommand.text.trim(),
                  projectBindingMode: bindingMode,
                  boundProjectIds: bindingMode == 'ALL_PROJECTS'
                      ? const []
                      : boundProjectIds.toList(),
                  agentRuntimeEnv: agentRuntimeEnvInput(),
                );
              } else {
                await apiClient.updateWorker(
                  WorkerItem(
                    id: worker.id,
                    name: name.text.trim(),
                    status: worker.status,
                    supportedAgents: agents.toList(),
                    workDir: workDir.text.trim(),
                    startupCommand: startupCommand.text.trim(),
                    projectBindingMode: bindingMode,
                    boundProjectIds: bindingMode == 'ALL_PROJECTS'
                        ? const []
                        : boundProjectIds.toList(),
                    agentRuntimeEnv: agentRuntimeEnvInput(),
                    lastHeartbeatAt: worker.lastHeartbeatAt,
                    currentTaskIds: worker.currentTaskIds,
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

String _agentLabel(String agent) => switch (agent) {
      'codex' => 'Codex',
      'claude' => 'Claude',
      _ => agent,
    };

class _WorkerEnvEditor extends StatelessWidget {
  const _WorkerEnvEditor({
    required this.agent,
    required this.vars,
    required this.onAdd,
    required this.onEdit,
    required this.onRemove,
  });

  final String agent;
  final List<EnvVarItem> vars;
  final VoidCallback onAdd;
  final ValueChanged<EnvVarItem> onEdit;
  final ValueChanged<EnvVarItem> onRemove;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border.all(color: Theme.of(context).dividerColor),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Padding(
        padding: const EdgeInsets.all(10),
        child: Column(
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    _agentLabel(agent),
                    style: Theme.of(context).textTheme.bodyMedium,
                  ),
                ),
                FilledButton.icon(
                  onPressed: onAdd,
                  icon: const Icon(Icons.add),
                  label: const Text('New env var'),
                ),
              ],
            ),
            const SizedBox(height: 8),
            if (vars.isEmpty)
              const Align(
                alignment: Alignment.centerLeft,
                child: Text('No environment variables'),
              )
            else
              ...vars.map(
                (item) => ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(
                    item.enabled
                        ? Icons.toggle_on_outlined
                        : Icons.toggle_off_outlined,
                  ),
                  title: Text(item.key),
                  subtitle: Text('${item.valueMasked}  ${item.description}'),
                  trailing: Wrap(
                    spacing: 6,
                    children: [
                      if (item.sensitive)
                        const Icon(Icons.visibility_off_outlined),
                      IconButton(
                        tooltip: 'Edit env var',
                        onPressed: () => onEdit(item),
                        icon: const Icon(Icons.edit_outlined),
                      ),
                      IconButton(
                        tooltip: 'Remove env var',
                        onPressed: () => onRemove(item),
                        icon: const Icon(Icons.delete_outline),
                      ),
                    ],
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

Future<EnvVarItem?> showWorkerEnvVarDialog(
  BuildContext context, {
  EnvVarItem? item,
}) {
  final key = TextEditingController(text: item?.key ?? '');
  final value = TextEditingController(
    text: item != null && !item.sensitive ? item.valueMasked : '',
  );
  final description = TextEditingController(text: item?.description ?? '');
  var enabled = item?.enabled ?? true;
  var sensitive = item?.sensitive ?? true;
  return showDialog<EnvVarItem>(
    context: context,
    builder: (context) => StatefulBuilder(
      builder: (context, setState) => AlertDialog(
        title: Text(item == null ? 'Create env var' : 'Edit env var'),
        content: SizedBox(
          width: 520,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: key,
                decoration: const InputDecoration(labelText: 'Key'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: value,
                decoration: InputDecoration(
                  labelText: item == null ? 'Value' : 'New value',
                  helperText: item != null && sensitive
                      ? 'Leave blank to keep current value.'
                      : null,
                ),
                obscureText: sensitive,
              ),
              const SizedBox(height: 12),
              TextField(
                controller: description,
                decoration: const InputDecoration(labelText: 'Description'),
              ),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                children: [
                  FilterChip(
                    label: const Text('Enabled'),
                    selected: enabled,
                    onSelected: (value) => setState(() => enabled = value),
                  ),
                  FilterChip(
                    label: const Text('Sensitive'),
                    selected: sensitive,
                    onSelected: (value) => setState(() => sensitive = value),
                  ),
                ],
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () {
              if (key.text.trim().isEmpty) {
                return;
              }
              Navigator.of(context).pop(
                EnvVarItem(
                  key: key.text.trim(),
                  valueMasked: sensitive ? '********' : value.text.trim(),
                  description: description.text.trim(),
                  enabled: enabled,
                  sensitive: sensitive,
                  valueInput: value.text,
                ),
              );
            },
            child: const Text('Save'),
          ),
        ],
      ),
    ),
  );
}
