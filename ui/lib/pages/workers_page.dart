import 'dart:async';

import 'package:flutter/material.dart';

import '../api_client.dart';
import '../models.dart';
import '../widgets.dart';

class WorkersPage extends StatefulWidget {
  const WorkersPage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<WorkersPage> createState() => _WorkersPageState();
}

class _WorkersPageState extends State<WorkersPage> {
  late Future<_WorkersData> _future;
  _WorkersData? _lastData;
  StreamSubscription<DomainEventItem>? _subscription;
  Timer? _refreshTimer;

  @override
  void initState() {
    super.initState();
    _future = _load();
    _subscription = widget.apiClient.subscribeDomainEvents().listen((event) {
      if (event.aggregateType == 'Worker' || event.aggregateType == 'Project') {
        _scheduleReload();
      }
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    _subscription?.cancel();
    super.dispose();
  }

  Future<_WorkersData> _load() async {
    final results = await Future.wait([
      widget.apiClient.fetchWorkers(),
      widget.apiClient.fetchProjects(),
    ]);
    final data = _WorkersData(
      workers: results[0] as List<WorkerItem>,
      projects: results[1] as List<ProjectItem>,
    );
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

  @override
  Widget build(BuildContext context) {
    return PageScaffold(
      title: 'Workers',
      icon: Icons.memory,
      actions: [
        IconButton(
          tooltip: 'Refresh workers',
          onPressed: _reload,
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
              onRetry: _reload,
            );
          }
          final items = data?.workers ?? const <WorkerItem>[];
          if (items.isEmpty) {
            return const EmptyState(
              icon: Icons.memory_outlined,
              title: 'No workers',
              message: 'Create or register a worker to execute tasks.',
            );
          }
          return Stack(
            children: [
              ListView.separated(
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
                          Text(worker.currentTaskId ?? 'Available'),
                          IconButton(
                            tooltip: 'Edit worker',
                            onPressed: () => _openWorkerDialog(worker),
                            icon: const Icon(Icons.edit_outlined),
                          ),
                          IconButton(
                            tooltip: 'Enable worker',
                            onPressed: () => _run(
                              () => widget.apiClient.enableWorker(worker.id),
                            ),
                            icon: const Icon(Icons.toggle_on_outlined),
                          ),
                          IconButton(
                            tooltip: 'Disable worker',
                            onPressed: () => _run(
                              () => widget.apiClient.disableWorker(worker.id),
                            ),
                            icon: const Icon(Icons.toggle_off_outlined),
                          ),
                          IconButton(
                            tooltip: 'Delete worker',
                            onPressed: (worker.currentTaskId ?? '').isEmpty
                                ? () => _deleteWorker(worker)
                                : null,
                            icon: const Icon(Icons.delete_outline),
                          ),
                        ],
                      ),
                    ),
                  );
                },
                separatorBuilder: (context, index) => const SizedBox(height: 8),
                itemCount: items.length,
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
      _reload();
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
  const _WorkersData({required this.workers, required this.projects});

  final List<WorkerItem> workers;
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
                    lastHeartbeatAt: worker.lastHeartbeatAt,
                    currentTaskId: worker.currentTaskId,
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
