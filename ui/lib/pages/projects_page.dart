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
  PageRequest _page = const PageRequest();
  final TextEditingController _searchController = TextEditingController();
  SortRequest _sort = const SortRequest(field: 'CREATED_AT');
  late Future<PagedResult<ProjectItem>> _future;
  PagedResult<ProjectItem>? _lastPage;
  RealtimeRefreshController? _realtime;

  @override
  void initState() {
    super.initState();
    _future = _load();
    _realtime = RealtimeRefreshController(
      events: widget.apiClient.subscribeDomainEvents(aggregateType: 'Project'),
      reload: () => _reload(),
      shouldReload: (_) => true,
    );
  }

  @override
  void dispose() {
    _realtime?.dispose();
    _searchController.dispose();
    super.dispose();
  }

  Future<PagedResult<ProjectItem>> _load() async {
    var page = await widget.apiClient.fetchProjectsPage(
      page: _page,
      search: _searchController.text,
      sort: _sort,
    );
    if (page.items.isEmpty && page.totalCount > 0 && _page.offset > 0) {
      final corrected = _page.withOffset(_page.lastOffset(page.totalCount));
      if (corrected.offset != _page.offset) {
        _page = corrected;
        page = await widget.apiClient.fetchProjectsPage(
          page: _page,
          search: _searchController.text,
          sort: _sort,
        );
      }
    }
    _lastPage = page;
    return page;
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

  @override
  Widget build(BuildContext context) {
    return PageScaffold(
      title: 'Projects',
      icon: Icons.folder_copy,
      actions: [
        IconButton(
          tooltip: 'Refresh projects',
          onPressed: () => _reload(),
          icon: const Icon(Icons.refresh),
        ),
        FilledButton.icon(
          onPressed: () => _openProjectDialog(),
          icon: const Icon(Icons.add),
          label: const Text('New project'),
        ),
      ],
      child: Column(
        children: [
          SearchSortToolbar(
            keyPrefix: 'projects',
            searchController: _searchController,
            sort: _sort,
            sortOptions: const [
              SortOption(field: 'CREATED_AT', label: 'Created'),
              SortOption(field: 'UPDATED_AT', label: 'Updated'),
              SortOption(field: 'NAME', label: 'Name'),
              SortOption(field: 'GIT_URL', label: 'Git URL'),
              SortOption(field: 'DEFAULT_BRANCH', label: 'Default branch'),
            ],
            onSearchChanged: _setSearch,
            onSortChanged: _setSort,
          ),
          Expanded(
            child: FutureBuilder<PagedResult<ProjectItem>>(
              future: _future,
              builder: (context, snapshot) {
                final page = snapshot.data ?? _lastPage;
                if (snapshot.connectionState != ConnectionState.done &&
                    page == null) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snapshot.hasError && page == null) {
                  return ErrorView(
                    message: snapshot.error.toString(),
                    onRetry: () => _reload(),
                  );
                }
                final items = page?.items ?? const <ProjectItem>[];
                if (items.isEmpty) {
                  return const EmptyState(
                    icon: Icons.folder_copy_outlined,
                    title: 'No projects',
                    message: 'Create a project before creating tasks.',
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
                              final project = items[index];
                              return Card(
                                child: ListTile(
                                  leading:
                                      const Icon(Icons.folder_copy_outlined),
                                  title: Text(project.name),
                                  subtitle: Text(
                                    '${project.gitUrl}\n${project.defaultBranch}  ${project.worktreeNamePrefix}',
                                    maxLines: 2,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                  trailing: Wrap(
                                    spacing: 8,
                                    crossAxisAlignment:
                                        WrapCrossAlignment.center,
                                    children: [
                                      if (project.archived)
                                        const StatusPill(value: 'ARCHIVED'),
                                      IconButton(
                                        tooltip: 'Edit project',
                                        onPressed: () =>
                                            _openProjectDialog(project),
                                        icon: const Icon(Icons.edit_outlined),
                                      ),
                                      IconButton(
                                        tooltip: 'Archive project',
                                        onPressed: project.archived
                                            ? null
                                            : () => _archiveProject(project),
                                        icon:
                                            const Icon(Icons.archive_outlined),
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
                          totalCount: page?.totalCount ?? 0,
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
          ),
        ],
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
      _reload(firstPage: project == null);
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
