import 'package:flutter/material.dart';

import '../api_client.dart';
import '../models.dart';
import '../realtime_refresh.dart';
import '../widgets.dart';

class ProjectsPage extends StatefulWidget {
  const ProjectsPage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<ProjectsPage> createState() => _ProjectsPageState();
}

class _ProjectsPageState extends State<ProjectsPage> {
  late Future<List<ProjectItem>> _future;
  List<ProjectItem>? _lastProjects;
  RealtimeRefreshController? _realtime;

  @override
  void initState() {
    super.initState();
    _future = _load();
    _realtime = RealtimeRefreshController(
      events: widget.apiClient.subscribeDomainEvents(aggregateType: 'Project'),
      reload: _reload,
      shouldReload: (_) => true,
    );
  }

  @override
  void dispose() {
    _realtime?.dispose();
    super.dispose();
  }

  Future<List<ProjectItem>> _load() async {
    final projects = await widget.apiClient.fetchProjects();
    _lastProjects = projects;
    return projects;
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
    return PageScaffold(
      title: 'Projects',
      icon: Icons.folder_copy,
      actions: [
        IconButton(
          tooltip: 'Refresh projects',
          onPressed: _reload,
          icon: const Icon(Icons.refresh),
        ),
        FilledButton.icon(
          onPressed: () => _openProjectDialog(),
          icon: const Icon(Icons.add),
          label: const Text('New project'),
        ),
      ],
      child: FutureBuilder<List<ProjectItem>>(
        future: _future,
        builder: (context, snapshot) {
          final projects = snapshot.data ?? _lastProjects;
          if (snapshot.connectionState != ConnectionState.done &&
              projects == null) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError && projects == null) {
            return ErrorView(
              message: snapshot.error.toString(),
              onRetry: _reload,
            );
          }
          final items = projects ?? const <ProjectItem>[];
          if (items.isEmpty) {
            return const EmptyState(
              icon: Icons.folder_copy_outlined,
              title: 'No projects',
              message: 'Create a project before creating tasks.',
            );
          }
          return Stack(
            children: [
              ListView.separated(
                padding: const EdgeInsets.all(16),
                itemBuilder: (context, index) {
                  final project = items[index];
                  return Card(
                    child: ListTile(
                      leading: const Icon(Icons.folder_copy_outlined),
                      title: Text(project.name),
                      subtitle: Text(
                        '${project.gitUrl}\n${project.defaultBranch}  ${project.worktreeNamePrefix}',
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                      trailing: Wrap(
                        spacing: 8,
                        crossAxisAlignment: WrapCrossAlignment.center,
                        children: [
                          if (project.archived)
                            const StatusPill(value: 'ARCHIVED'),
                          IconButton(
                            tooltip: 'Edit project',
                            onPressed: () => _openProjectDialog(project),
                            icon: const Icon(Icons.edit_outlined),
                          ),
                          IconButton(
                            tooltip: 'Archive project',
                            onPressed: project.archived
                                ? null
                                : () => _archiveProject(project),
                            icon: const Icon(Icons.archive_outlined),
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

  Future<void> _openProjectDialog([ProjectItem? project]) async {
    final saved = await showProjectFormDialog(
      context,
      apiClient: widget.apiClient,
      project: project,
    );
    if (saved == true) {
      _reload();
    }
  }

  Future<void> _archiveProject(ProjectItem project) async {
    final confirmed = await confirmAction(
      context,
      title: 'Archive project',
      message: 'Archive "${project.name}"?',
      confirmLabel: 'Archive',
    );
    if (!confirmed) {
      return;
    }
    await widget.apiClient.archiveProject(project.id);
    _reload();
  }
}

Future<bool?> showProjectFormDialog(
  BuildContext context, {
  required ApiClient apiClient,
  ProjectItem? project,
}) {
  final name = TextEditingController(text: project?.name ?? '');
  final gitUrl = TextEditingController(text: project?.gitUrl ?? '');
  final defaultBranch = TextEditingController(
    text: project?.defaultBranch ?? 'main',
  );
  final prefix = TextEditingController(
    text: project?.worktreeNamePrefix ?? 'block-play-table',
  );
  return showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: Text(project == null ? 'Create project' : 'Edit project'),
      content: SizedBox(
        width: 620,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: name,
                decoration: const InputDecoration(labelText: 'Name'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: gitUrl,
                decoration: const InputDecoration(labelText: 'Git URL'),
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: defaultBranch,
                      decoration: const InputDecoration(
                        labelText: 'Default branch',
                      ),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: TextField(
                      controller: prefix,
                      decoration: const InputDecoration(
                        labelText: 'Worktree prefix',
                      ),
                    ),
                  ),
                ],
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
            if (name.text.trim().isEmpty || gitUrl.text.trim().isEmpty) {
              return;
            }
            if (project == null) {
              await apiClient.createProject(
                name: name.text.trim(),
                gitUrl: gitUrl.text.trim(),
                prefix: prefix.text.trim(),
                defaultBranch: defaultBranch.text.trim().isEmpty
                    ? 'main'
                    : defaultBranch.text.trim(),
              );
            } else {
              await apiClient.updateProject(
                ProjectItem(
                  id: project.id,
                  name: name.text.trim(),
                  gitUrl: gitUrl.text.trim(),
                  defaultBranch: defaultBranch.text.trim().isEmpty
                      ? 'main'
                      : defaultBranch.text.trim(),
                  worktreeNamePrefix: prefix.text.trim(),
                  archived: project.archived,
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
  );
}
