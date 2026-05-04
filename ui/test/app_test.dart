import 'dart:async';

import 'package:block_play_table_ui/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('EnvVarItem.fromJson carries public values but not masked secrets', () {
    final public = EnvVarItem.fromJson(const {
      'key': 'ANTHROPIC_BASE_URL',
      'valueMasked': 'https://api.autocode.space',
      'description': null,
      'enabled': true,
      'sensitive': false,
    });
    final secret = EnvVarItem.fromJson(const {
      'key': 'OPENAI_API_KEY',
      'valueMasked': '********',
      'description': null,
      'enabled': true,
      'sensitive': true,
    });

    expect(public.valueInput, 'https://api.autocode.space');
    expect(secret.valueInput, isEmpty);
  });

  test(
    'updateWorker sends public env values and preserves blank secrets',
    () async {
      final apiClient = RecordingApiClient();

      await apiClient.updateWorker(
        WorkerItem(
          id: 'worker-1',
          name: 'Local worker',
          status: 'ONLINE',
          supportedAgents: const ['codex'],
          workDir: '/tmp/worker',
          startupCommand: '',
          projectBindingMode: 'ALL_PROJECTS',
          boundProjectIds: const [],
          currentTaskIds: const [],
          agentRuntimeEnv: const [
            WorkerAgentRuntimeEnvItem(
              agentType: 'codex',
              vars: [
                EnvVarItem(
                  key: 'PUBLIC_EMPTY',
                  valueMasked: '',
                  description: '',
                  enabled: true,
                  sensitive: false,
                ),
                EnvVarItem(
                  key: 'SECRET',
                  valueMasked: '********',
                  description: '',
                  enabled: true,
                  sensitive: true,
                ),
              ],
            ),
          ],
        ),
      );

      final input = apiClient.lastVariables!['input'] as Map<String, dynamic>;
      final agentEnv = input['agentRuntimeEnv'] as List<dynamic>;
      final vars =
          (agentEnv[0] as Map<String, dynamic>)['vars'] as List<dynamic>;
      final public = vars[0] as Map<String, dynamic>;
      final secret = vars[1] as Map<String, dynamic>;

      expect(public, containsPair('value', ''));
      expect(secret.containsKey('value'), isFalse);
    },
  );

  test(
    'fetchBoardData sends project filter with page search and sort',
    () async {
      final apiClient = BoardRecordingApiClient();

      await apiClient.fetchBoardData(
        'KANBAN',
        page: const PageRequest(offset: 20, limit: 20),
        search: 'Needle',
        sort: const SortRequest(
          field: 'TITLE',
          direction: SortRequest.ascending,
        ),
        projectId: 'project-2',
      );

      final variables = apiClient.boardVariables!;
      expect(variables['page'], {'offset': 20, 'limit': 20});
      expect(variables['sort'], {'field': 'TITLE', 'direction': 'ASC'});
      expect(variables['filter'], {
        'includeArchived': true,
        'projectId': 'project-2',
        'search': 'Needle',
      });
    },
  );

  testWidgets(
    'renders workspace navigation without Tasks and opens task dialog',
    (tester) async {
      final apiClient = FakeApiClient();
      await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
      await tester.pumpAndSettle();

      expect(find.text('Block Play Table'), findsNothing);
      expect(find.byType(AppBar), findsNothing);
      expect(find.text('Tasks'), findsNothing);
      expect(find.text('Board'), findsWidgets);
      expect(find.text('Projects'), findsWidgets);
      expect(find.text('Workers'), findsWidgets);
      expect(find.text('Events'), findsWidgets);
      expect(find.byIcon(Icons.view_kanban), findsWidgets);
      expect(find.text('Pending'), findsOneWidget);
      expect(find.text('Running'), findsOneWidget);
      expect(find.text('Complete'), findsOneWidget);

      await tester.tap(find.widgetWithText(FilledButton, 'New task'));
      await tester.pumpAndSettle();

      expect(find.byType(AlertDialog), findsOneWidget);
      expect(find.text('Create task'), findsOneWidget);
      expect(find.text('Target branch'), findsNothing);
      expect(find.textContaining('Start date:'), findsOneWidget);
      expect(find.textContaining('End date:'), findsOneWidget);
      expect(find.text('Codex'), findsNothing);
      expect(find.text('Unassigned'), findsOneWidget);
    },
  );

  testWidgets('kanban columns expand to fill wide screens', (tester) async {
    const surfaceSize = Size(1200, 800);
    _setSurfaceSize(tester, surfaceSize);
    final apiClient = FakeApiClient();

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);

    final pendingRect = tester.getRect(
      find.byKey(const ValueKey('kanban-column-pending')),
    );
    final runningRect = tester.getRect(
      find.byKey(const ValueKey('kanban-column-running')),
    );
    final completeRect = tester.getRect(
      find.byKey(const ValueKey('kanban-column-complete')),
    );

    expect(pendingRect.width, greaterThan(260));
    expect(runningRect.width, closeTo(pendingRect.width, 0.5));
    expect(completeRect.width, closeTo(pendingRect.width, 0.5));
    expect(runningRect.left - pendingRect.right, closeTo(12, 0.5));
    expect(completeRect.left - runningRect.right, closeTo(12, 0.5));
    expect(completeRect.right, closeTo(surfaceSize.width - 16, 1));
  });

  testWidgets('kanban keeps minimum column width on narrow screens', (
    tester,
  ) async {
    const surfaceSize = Size(820, 800);
    _setSurfaceSize(tester, surfaceSize);
    final apiClient = FakeApiClient();

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);

    final pendingRect = tester.getRect(
      find.byKey(const ValueKey('kanban-column-pending')),
    );
    final runningRect = tester.getRect(
      find.byKey(const ValueKey('kanban-column-running')),
    );
    final completeRect = tester.getRect(
      find.byKey(const ValueKey('kanban-column-complete')),
    );

    expect(pendingRect.width, closeTo(260, 0.5));
    expect(runningRect.width, closeTo(260, 0.5));
    expect(completeRect.width, closeTo(260, 0.5));
    expect(completeRect.right, greaterThan(surfaceSize.width));
  });

  testWidgets('kanban columns scroll vertically when task cards overflow', (
    tester,
  ) async {
    const surfaceSize = Size(1200, 800);
    _setSurfaceSize(tester, surfaceSize);
    final overflowTasks = List.generate(
      18,
      (index) => _taskWith(
        id: 'overflow-task-$index',
        title: 'Overflow task $index',
        createdAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
        updatedAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
      ),
    );
    final apiClient = FakeApiClient(tasks: overflowTasks);

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.byType(Scrollbar), findsWidgets);
    expect(find.text('Overflow task 17'), findsOneWidget);
    expect(find.text('Overflow task 0'), findsNothing);

    await tester.drag(find.text('Overflow task 17'), const Offset(0, -2400));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.text('Overflow task 0'), findsOneWidget);
  });

  testWidgets('calendar month view renders ranged task bars', (tester) async {
    const surfaceSize = Size(1200, 800);
    _setSurfaceSize(tester, surfaceSize);
    final apiClient = FakeApiClient(
      tasks: [
        _task,
        _taskWith(
          id: 'task-cross-week',
          title: 'Cross week deployment',
          startDate: '2026-04-30T00:00:00Z',
          endDate: '2026-05-02T00:00:00Z',
        ),
      ],
    );

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Calendar'));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.text('April 2026'), findsOneWidget);
    expect(find.text('Mon'), findsOneWidget);
    expect(find.text('Sun'), findsOneWidget);
    expect(find.text('Refresh board'), findsOneWidget);
    expect(find.text('Cross week deployment'), findsOneWidget);
  });

  testWidgets('calendar switches between day week month and year views', (
    tester,
  ) async {
    const surfaceSize = Size(1200, 800);
    _setSurfaceSize(tester, surfaceSize);
    final apiClient = FakeApiClient();

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Calendar'));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Day'));
    await tester.pumpAndSettle();
    expect(find.text('Apr 25, 2026'), findsOneWidget);
    expect(find.text('Refresh board'), findsOneWidget);

    await tester.tap(find.text('Week'));
    await tester.pumpAndSettle();
    expect(find.text('Apr 20 - 26, 2026'), findsOneWidget);
    expect(find.text('Refresh board'), findsOneWidget);

    await tester.tap(find.text('Year'));
    await tester.pumpAndSettle();
    expect(find.text('2026'), findsOneWidget);
    expect(find.text('April'), findsOneWidget);
    expect(find.text('1 task'), findsOneWidget);

    await tester.tap(find.text('April'));
    await tester.pumpAndSettle();
    expect(find.text('April 2026'), findsOneWidget);
  });

  testWidgets('calendar search filters tasks and can clear results', (
    tester,
  ) async {
    const surfaceSize = Size(1200, 800);
    _setSurfaceSize(tester, surfaceSize);
    final apiClient = FakeApiClient(
      tasks: [
        _task,
        _taskWith(
          id: 'task-backend',
          title: 'Backend migration',
          description: 'GraphQL rollout',
          startDate: '2026-04-27T00:00:00Z',
          endDate: '2026-04-28T00:00:00Z',
        ),
      ],
    );

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Calendar'));
    await tester.pumpAndSettle();

    await tester.enterText(
      find.byKey(const ValueKey('board-search-field')),
      'Backend',
    );
    await tester.pumpAndSettle();

    expect(find.text('April 2026'), findsOneWidget);
    expect(find.text('Backend migration'), findsOneWidget);
    expect(find.text('Refresh board'), findsNothing);

    await tester.enterText(
      find.byKey(const ValueKey('board-search-field')),
      '',
    );
    await tester.pumpAndSettle();

    expect(find.text('Backend migration'), findsOneWidget);
    expect(find.text('Refresh board'), findsOneWidget);
  });

  testWidgets('calendar task bars open task detail dialog', (tester) async {
    const surfaceSize = Size(1200, 800);
    _setSurfaceSize(tester, surfaceSize);
    final apiClient = FakeApiClient();

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Calendar'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Refresh board'));
    await tester.pumpAndSettle();

    expect(find.byType(AlertDialog), findsOneWidget);
    expect(apiClient.detailFetches, 1);
    expect(find.text('Conversation'), findsOneWidget);
  });

  testWidgets('board paginates tasks and resets page when view changes', (
    tester,
  ) async {
    const surfaceSize = Size(1200, 800);
    _setSurfaceSize(tester, surfaceSize);
    final tasks = List.generate(
      21,
      (index) => _taskWith(
        id: 'task-$index',
        title: 'Paged task $index',
        createdAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
        updatedAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
      ),
    );
    final apiClient = FakeApiClient(tasks: tasks);

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    expect(find.text('Showing 1-20 of 21'), findsOneWidget);
    expect(find.text('Paged task 20'), findsOneWidget);
    expect(find.text('Paged task 0'), findsNothing);

    await tester.tap(find.byTooltip('Next page'));
    await tester.pumpAndSettle();

    expect(find.text('Showing 21-21 of 21'), findsOneWidget);
    expect(find.text('Paged task 0'), findsOneWidget);

    await tester.tap(find.text('List'));
    await tester.pumpAndSettle();

    expect(find.text('Showing 1-20 of 21'), findsOneWidget);
    expect(find.text('Paged task 20'), findsOneWidget);
    expect(find.text('Paged task 0'), findsNothing);
  });

  testWidgets(
    'board project filter applies before pagination search and sort',
    (tester) async {
      const surfaceSize = Size(1200, 800);
      _setSurfaceSize(tester, surfaceSize);
      const otherProject = ProjectItem(
        id: 'project-2',
        name: 'Mobile',
        gitUrl: 'git@example.com:mobile.git',
        defaultBranch: 'main',
        worktreeNamePrefix: 'mobile',
        archived: false,
      );
      final targetTasks = List.generate(
        21,
        (index) => _taskWith(
          id: 'target-task-$index',
          title: 'Project filtered task $index',
          projectId: otherProject.id,
          createdAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
          updatedAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
        ),
      );
      final apiClient = FakeApiClient(
        projects: [_project, otherProject],
        tasks: [
          _taskWith(
            id: 'other-project-task',
            title: 'Other project task',
            projectId: _project.id,
            createdAt: '2026-04-25T00:59:00Z',
            updatedAt: '2026-04-25T00:59:00Z',
          ),
          ...targetTasks,
        ],
      );

      await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
      await tester.pumpAndSettle();

      expect(find.text('Other project task'), findsOneWidget);

      await tester.tap(find.byKey(const ValueKey('board-project-filter')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Mobile').last);
      await tester.pumpAndSettle();

      expect(apiClient.lastBoardProjectId, otherProject.id);
      expect(find.text('Showing 1-20 of 21'), findsOneWidget);
      expect(find.text('Project filtered task 20'), findsOneWidget);
      expect(find.text('Other project task'), findsNothing);

      await tester.tap(find.byTooltip('Next page'));
      await tester.pumpAndSettle();

      expect(apiClient.lastBoardProjectId, otherProject.id);
      expect(find.text('Showing 21-21 of 21'), findsOneWidget);
      expect(find.text('Project filtered task 0'), findsOneWidget);

      await tester.enterText(
        find.byKey(const ValueKey('board-search-field')),
        'Project filtered task 20',
      );
      await tester.pumpAndSettle();

      expect(apiClient.lastBoardProjectId, otherProject.id);
      expect(find.text('Showing 1-1 of 1'), findsOneWidget);
      expect(
        find.ancestor(
          of: find.text('Project filtered task 20'),
          matching: find.byType(InkWell),
        ),
        findsOneWidget,
      );
      expect(find.text('Project filtered task 0'), findsNothing);

      await tester.enterText(
        find.byKey(const ValueKey('board-search-field')),
        'Project filtered task',
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('board-sort-direction')));
      await tester.pumpAndSettle();

      expect(apiClient.lastBoardProjectId, otherProject.id);
      expect(apiClient.lastBoardSort?.direction, SortRequest.ascending);
      expect(find.text('Showing 1-20 of 21'), findsOneWidget);
      expect(find.text('Project filtered task 0'), findsOneWidget);
    },
  );

  testWidgets('projects workers and events paginate with shared controls', (
    tester,
  ) async {
    const surfaceSize = Size(1200, 900);
    _setSurfaceSize(tester, surfaceSize);
    final projects = List.generate(
      21,
      (index) => ProjectItem(
        id: 'project-$index',
        name: 'Paged project $index',
        gitUrl: 'git://project-$index',
        defaultBranch: 'main',
        worktreeNamePrefix: 'project-$index',
        archived: false,
        createdAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
        updatedAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
      ),
    );
    final workers = List.generate(
      21,
      (index) => _defaultWorker.copyWith(
        id: 'worker-$index',
        name: 'Paged worker $index',
        createdAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
        updatedAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
      ),
    );
    final events = List.generate(
      21,
      (index) => DomainEventItem(
        eventId: 'event-$index',
        eventType: 'PagedEvent$index',
        aggregateType: 'Task',
        aggregateId: 'task-$index',
        aggregateVersion: 1,
        payload: '{}',
        occurredAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
      ),
    );
    final apiClient = FakeApiClient(
      projects: projects,
      workers: workers,
      events: events,
    );

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Projects').first);
    await tester.pumpAndSettle();
    expect(find.text('Showing 1-20 of 21'), findsOneWidget);
    expect(find.text('Paged project 20'), findsOneWidget);
    expect(find.text('Paged project 0'), findsNothing);
    await tester.tap(find.byTooltip('Next page'));
    await tester.pumpAndSettle();
    expect(find.text('Showing 21-21 of 21'), findsOneWidget);
    expect(find.text('Paged project 0'), findsOneWidget);

    await tester.tap(find.text('Workers').first);
    await tester.pumpAndSettle();
    expect(find.text('Showing 1-20 of 21'), findsOneWidget);
    expect(find.text('Paged worker 20'), findsOneWidget);
    expect(find.text('Paged worker 0'), findsNothing);
    await tester.tap(find.byTooltip('Next page'));
    await tester.pumpAndSettle();
    expect(find.text('Showing 21-21 of 21'), findsOneWidget);
    expect(find.text('Paged worker 0'), findsOneWidget);

    await tester.tap(find.text('Events').first);
    await tester.pumpAndSettle();
    expect(find.text('Showing 1-20 of 21'), findsOneWidget);
    expect(find.text('PagedEvent20'), findsOneWidget);
    expect(find.text('PagedEvent0'), findsNothing);
    await tester.tap(find.byTooltip('Next page'));
    await tester.pumpAndSettle();
    expect(find.text('Showing 21-21 of 21'), findsOneWidget);
    expect(find.text('PagedEvent0'), findsOneWidget);
  });

  testWidgets('resource pages search before pagination', (tester) async {
    const surfaceSize = Size(1200, 900);
    _setSurfaceSize(tester, surfaceSize);
    final tasks = List.generate(
      21,
      (index) => _taskWith(
        id: 'task-search-$index',
        title: index == 20 ? 'Needle task' : 'Hay task $index',
      ),
    );
    final projects = List.generate(
      21,
      (index) => ProjectItem(
        id: 'project-search-$index',
        name: index == 20 ? 'Needle project' : 'Hay project $index',
        gitUrl: 'git://project-$index',
        defaultBranch: 'main',
        worktreeNamePrefix: 'project-$index',
        archived: false,
      ),
    );
    final workers = List.generate(
      21,
      (index) => _defaultWorker.copyWith(
        id: 'worker-search-$index',
        name: index == 20 ? 'Needle worker' : 'Hay worker $index',
      ),
    );
    final events = List.generate(
      21,
      (index) => DomainEventItem(
        eventId: 'event-search-$index',
        eventType: index == 20 ? 'NeedleEvent' : 'HayEvent$index',
        aggregateType: 'Task',
        aggregateId: 'task-$index',
        aggregateVersion: 1,
        payload: '{}',
        occurredAt: '2026-04-25T00:${index.toString().padLeft(2, '0')}:00Z',
      ),
    );
    final apiClient = FakeApiClient(
      tasks: tasks,
      projects: projects,
      workers: workers,
      events: events,
    );

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.enterText(
      find.byKey(const ValueKey('board-search-field')),
      'Needle',
    );
    await tester.pumpAndSettle();
    expect(find.text('Needle task'), findsOneWidget);
    expect(find.text('Hay task 0'), findsNothing);

    await tester.tap(find.text('Projects').first);
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey('projects-search-field')),
      'Needle',
    );
    await tester.pumpAndSettle();
    expect(find.text('Needle project'), findsOneWidget);
    expect(find.text('Hay project 0'), findsNothing);

    await tester.tap(find.text('Workers').first);
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey('workers-search-field')),
      'Needle',
    );
    await tester.pumpAndSettle();
    expect(find.text('Needle worker'), findsOneWidget);
    expect(find.text('Hay worker 0'), findsNothing);

    await tester.tap(find.text('Events').first);
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byKey(const ValueKey('events-search-field')),
      'Needle',
    );
    await tester.pumpAndSettle();
    expect(find.text('NeedleEvent'), findsOneWidget);
    expect(find.text('HayEvent0'), findsNothing);
  });

  testWidgets('projects page switches sort field and direction', (
    tester,
  ) async {
    const surfaceSize = Size(1200, 700);
    _setSurfaceSize(tester, surfaceSize);
    final apiClient = FakeApiClient(
      projects: const [
        ProjectItem(
          id: 'project-alpha',
          name: 'Alpha Project',
          gitUrl: 'git://alpha',
          defaultBranch: 'main',
          worktreeNamePrefix: 'alpha',
          archived: false,
        ),
        ProjectItem(
          id: 'project-zulu',
          name: 'Zulu Project',
          gitUrl: 'git://zulu',
          defaultBranch: 'main',
          worktreeNamePrefix: 'zulu',
          archived: false,
        ),
      ],
    );

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Projects').first);
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('projects-sort-field')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Name').last);
    await tester.pumpAndSettle();

    expect(
      tester.getTopLeft(find.text('Zulu Project')).dy,
      lessThan(tester.getTopLeft(find.text('Alpha Project')).dy),
    );

    await tester.tap(find.byKey(const ValueKey('projects-sort-direction')));
    await tester.pumpAndSettle();

    expect(
      tester.getTopLeft(find.text('Alpha Project')).dy,
      lessThan(tester.getTopLeft(find.text('Zulu Project')).dy),
    );
  });

  testWidgets('create task can save without worker or agent', (tester) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'New task'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find
          .descendant(
            of: find.byType(AlertDialog),
            matching: find.byType(TextField),
          )
          .first,
      'Agentless task',
    );
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, 'Save'));
    await tester.pumpAndSettle();

    expect(apiClient.createdTaskTitle, 'Agentless task');
    expect(apiClient.createdTaskWorkerId, isNull);
    expect(apiClient.createdTaskAgentType, isNull);
    expect(apiClient.createdTaskStartDate, isNotEmpty);
    expect(apiClient.createdTaskEndDate, apiClient.createdTaskStartDate);
  });

  testWidgets('selecting worker enables supported agent selection on create', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'New task'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find
          .descendant(
            of: find.byType(AlertDialog),
            matching: find.byType(TextField),
          )
          .first,
      'Worker task',
    );
    await tester.pump();
    await tester.tap(find.text('Unassigned').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Local worker').last);
    await tester.pumpAndSettle();

    expect(find.text('Codex'), findsOneWidget);
    expect(find.text('Claude'), findsNothing);
    expect(find.text('Agent CLI settings'), findsOneWidget);
    expect(find.text('Work mode'), findsOneWidget);
    expect(find.text('Codex model'), findsOneWidget);
    expect(find.text('Reasoning effort'), findsOneWidget);

    await tester.tap(find.widgetWithText(FilledButton, 'Save'));
    await tester.pumpAndSettle();

    expect(apiClient.createdTaskTitle, 'Worker task');
    expect(apiClient.createdTaskWorkerId, 'worker-1');
    expect(apiClient.createdTaskAgentType, 'codex');
    expect(apiClient.createdTaskAgentConfig, isNotNull);
  });

  testWidgets('assign dialog shows agent config for typed tasks', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Refresh board').first);
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const ValueKey('task-detail-action-assign')));
    await tester.pumpAndSettle();

    expect(find.text('Assign worker'), findsOneWidget);
    expect(find.text('Codex'), findsOneWidget);
    expect(find.text('Agent CLI settings'), findsOneWidget);
    expect(find.text('Codex model'), findsOneWidget);
    expect(find.text('Reasoning effort'), findsOneWidget);
  });

  testWidgets('projects and workers create and edit through dialogs', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Projects').first);
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'New project'));
    await tester.pumpAndSettle();
    expect(find.text('Create project'), findsOneWidget);
    expect(find.text('Setup commands'), findsNothing);
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Edit project').first);
    await tester.pumpAndSettle();
    expect(find.text('Edit project'), findsOneWidget);
    expect(find.text('Setup commands'), findsNothing);
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Workers').first);
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'New worker'));
    await tester.pumpAndSettle();
    expect(find.text('Create worker'), findsOneWidget);
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Edit worker').first);
    await tester.pumpAndSettle();
    expect(find.text('Edit worker'), findsOneWidget);
    expect(find.text('Runtime environment'), findsOneWidget);
  });

  testWidgets('board refreshes when GraphQL subscription emits an event', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    expect(apiClient.boardFetches, 1);

    apiClient.emit(
      DomainEventItem(
        eventId: 'event-task-created',
        eventType: 'TaskCreated',
        aggregateType: 'Task',
        aggregateId: 'task-2',
        aggregateVersion: 1,
        payload: '{}',
        occurredAt: '2026-04-25T00:00:00Z',
      ),
    );
    await tester.pump(const Duration(milliseconds: 350));
    await tester.pumpAndSettle();

    expect(apiClient.boardFetches, greaterThan(1));
  });

  testWidgets('board refreshes when worker delete event arrives', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    expect(apiClient.boardFetches, 1);

    apiClient.emit(
      DomainEventItem(
        eventId: 'event-worker-deleted',
        eventType: 'WorkerDeleted',
        aggregateType: 'Worker',
        aggregateId: 'worker-1',
        aggregateVersion: 3,
        payload: '{}',
        occurredAt: '2026-04-25T00:00:00Z',
      ),
    );
    await tester.pump(const Duration(milliseconds: 350));
    await tester.pumpAndSettle();

    expect(apiClient.boardFetches, greaterThan(1));
  });

  testWidgets('board falls back to reload when subscription errors', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    final initialFetches = apiClient.boardFetches;
    apiClient.emitError(StateError('subscription disconnected'));
    await tester.pump();
    await tester.pumpAndSettle();

    expect(apiClient.boardFetches, greaterThan(initialFetches));

    final afterErrorFetches = apiClient.boardFetches;
    await tester.pump(const Duration(seconds: 15));
    await tester.pumpAndSettle();

    expect(apiClient.boardFetches, greaterThan(afterErrorFetches));
  });

  testWidgets('task detail dialog refreshes on task subscription event', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Refresh board').first);
    await tester.pumpAndSettle();

    expect(find.byType(AlertDialog), findsOneWidget);
    expect(find.text('CREATED'), findsWidgets);
    expect(find.text(_project.name), findsOneWidget);
    expect(find.text(_project.id), findsNothing);
    expect(apiClient.detailFetches, 1);

    apiClient.completeTaskDetail();
    apiClient.emit(
      DomainEventItem(
        eventId: 'event-task-completed',
        eventType: 'TaskCompleted',
        aggregateType: 'Task',
        aggregateId: 'task-1',
        aggregateVersion: 2,
        payload: '{}',
        occurredAt: '2026-04-25T00:00:01Z',
      ),
    );
    await tester.pump(const Duration(milliseconds: 350));
    await tester.pumpAndSettle();

    expect(apiClient.detailFetches, greaterThan(1));
    expect(find.text('COMPLETED'), findsWidgets);
    expect(find.text('done'), findsNothing);
    final floatingRail = find.byKey(
      const ValueKey('task-detail-floating-command-rail'),
    );
    expect(floatingRail, findsOneWidget);
    expect(
      find.byKey(const ValueKey('task-detail-section-conversation')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('task-detail-section-logs')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('task-detail-section-terminal')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('task-detail-section-domain-events')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('task-detail-action-close')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('task-detail-action-assign')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('task-detail-action-start')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('task-detail-action-interrupt')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('task-detail-action-retry')),
      findsOneWidget,
    );
    expect(
      find.byKey(const ValueKey('task-detail-action-archive')),
      findsOneWidget,
    );
    for (final key in const [
      ValueKey('task-detail-section-conversation'),
      ValueKey('task-detail-section-terminal'),
      ValueKey('task-detail-section-logs'),
      ValueKey('task-detail-section-domain-events'),
      ValueKey('task-detail-action-close'),
      ValueKey('task-detail-action-assign'),
      ValueKey('task-detail-action-start'),
      ValueKey('task-detail-action-interrupt'),
      ValueKey('task-detail-action-retry'),
      ValueKey('task-detail-action-archive'),
    ]) {
      expect(
        find.descendant(of: floatingRail, matching: find.byKey(key)),
        findsOneWidget,
      );
    }
    expect(find.byType(TabBar), findsNothing);
    expect(find.byType(TabBarView), findsNothing);
    expect(
      find.textContaining('conversation from subscription'),
      findsOneWidget,
    );
    expect(find.textContaining('done from subscription'), findsNothing);
    expect(find.textContaining('TaskCompleted v2: {}'), findsNothing);

    await tester.tap(
      find.byKey(const ValueKey('task-detail-section-terminal')),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('/terminal/tasks/task-1/ws'), findsOneWidget);
    expect(find.byTooltip('Connect worker terminal'), findsOneWidget);

    await tester.tap(find.byKey(const ValueKey('task-detail-section-logs')));
    await tester.pumpAndSettle();

    expect(find.textContaining('done from subscription'), findsOneWidget);

    await tester.tap(
      find.byKey(const ValueKey('task-detail-section-domain-events')),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('TaskCompleted v2: {}'), findsOneWidget);
  });

  testWidgets('task detail title copies task id to clipboard', (tester) async {
    String? clipboardText;
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(SystemChannels.platform, (
      MethodCall call,
    ) async {
      if (call.method == 'Clipboard.setData') {
        final args = call.arguments as Map<dynamic, dynamic>;
        clipboardText = args['text'] as String?;
      }
      return null;
    });
    addTearDown(() {
      messenger.setMockMethodCallHandler(SystemChannels.platform, null);
    });

    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Refresh board').first);
    await tester.pumpAndSettle();

    await tester.tap(find.byTooltip('Copy task ID'));
    await tester.pumpAndSettle();

    expect(clipboardText, 'task-1');
    expect(find.text('Task ID copied'), findsOneWidget);
  });

  testWidgets('completed task detail sends continuation message', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    apiClient.completeTaskDetail();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Refresh board').first);
    await tester.pumpAndSettle();

    expect(find.text('COMPLETED'), findsWidgets);
    expect(find.byTooltip('Send continuation'), findsOneWidget);

    await tester.enterText(
      find.widgetWithText(TextField, 'Continue conversation'),
      'follow up',
    );
    final sendButton = find.widgetWithIcon(IconButton, Icons.send);
    await tester.ensureVisible(sendButton);
    await tester.tap(sendButton);
    await tester.pumpAndSettle();

    expect(apiClient.continuedTaskId, 'task-1');
    expect(apiClient.continuedMessage, 'follow up');
  });

  testWidgets('task detail opens worker web preview from address input', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Refresh board').first);
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const ValueKey('task-detail-section-web')));
    await tester.pumpAndSettle();

    await tester.enterText(
      find.widgetWithText(TextField, 'Worker web address'),
      'localhost:5173/dashboard?tab=preview',
    );
    await tester.pump();
    await tester.tap(find.byTooltip('Open worker web preview'));
    await tester.pumpAndSettle();

    expect(
      find.byKey(const ValueKey('task-detail-worker-web-preview')),
      findsOneWidget,
    );
    expect(
      find.textContaining(
        '/proxy/web/Local%20worker/localhost/5173/dashboard?tab=preview',
      ),
      findsWidgets,
    );
  });

  testWidgets('task detail terminal is unavailable without worktree', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Refresh board').first);
    await tester.pumpAndSettle();

    await tester.tap(
      find.byKey(const ValueKey('task-detail-section-terminal')),
    );
    await tester.pumpAndSettle();

    expect(find.text('Terminal unavailable'), findsOneWidget);
    expect(find.textContaining('Task worktree is not ready'), findsOneWidget);
    expect(find.byTooltip('Connect worker terminal'), findsNothing);
  });

  testWidgets('task detail terminal shows preflight error before connecting', (
    tester,
  ) async {
    final apiClient = FakeApiClient(
      terminalCheckError: 'terminal cwd does not exist',
    )..completeTaskDetail();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Refresh board').first);
    await tester.pumpAndSettle();

    await tester.tap(
      find.byKey(const ValueKey('task-detail-section-terminal')),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byTooltip('Connect worker terminal'));
    await tester.pumpAndSettle();

    expect(apiClient.terminalCheckCount, 1);
    expect(find.textContaining('terminal cwd does not exist'), findsWidgets);
    expect(find.text('Connected'), findsNothing);
  });

  testWidgets('task detail terminal is unavailable for archived task', (
    tester,
  ) async {
    final archivedTask = TaskItem(
      id: 'task-archived',
      title: 'Archived terminal',
      description: 'Archived task with stale worktree',
      status: 'ARCHIVED',
      projectId: _project.id,
      agentType: 'codex',
      agentConfig: const AgentExecutionConfigItem(),
      baseBranch: 'main',
      preCommands: const [],
      postCommands: const [],
      startDate: '2026-04-25T00:00:00Z',
      endDate: '2026-04-26T00:00:00Z',
      createdAt: '2026-04-25T00:00:00Z',
      updatedAt: '2026-04-25T00:00:01Z',
      workerId: 'worker-1',
      worktreePath: '/tmp/worker/missing-task-worktree',
      agentSessionId: 'session-1',
    );
    final apiClient = FakeApiClient(
      tasks: [archivedTask],
      detailTask: archivedTask,
    );
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Archived terminal').first);
    await tester.pumpAndSettle();

    await tester.tap(
      find.byKey(const ValueKey('task-detail-section-terminal')),
    );
    await tester.pumpAndSettle();

    expect(find.text('Terminal unavailable'), findsOneWidget);
    expect(find.textContaining('Task is archived'), findsOneWidget);
    expect(find.byTooltip('Connect worker terminal'), findsNothing);
    expect(apiClient.terminalCheckCount, 0);
  });

  testWidgets('settings no longer exposes agent runtime env controls', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Settings').first);
    await tester.pumpAndSettle();

    expect(find.text('Agent runtime environment'), findsNothing);
    expect(find.widgetWithText(FilledButton, 'New env var'), findsNothing);
  });

  testWidgets('editing worker saves distinct runtime env per selected agent', (
    tester,
  ) async {
    final apiClient = FakeApiClient(
      worker: _defaultWorker.copyWith(
        supportedAgents: const ['codex', 'claude'],
        agentRuntimeEnv: const [
          WorkerAgentRuntimeEnvItem(
            agentType: 'codex',
            vars: [
              EnvVarItem(
                key: 'CODEX_BASE_URL',
                valueMasked: 'https://codex.example',
                description: '',
                enabled: true,
                sensitive: false,
              ),
            ],
          ),
        ],
      ),
    );
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Workers').first);
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Edit worker').first);
    await tester.pumpAndSettle();

    expect(find.text('Runtime environment'), findsOneWidget);
    expect(find.text('CODEX_BASE_URL'), findsOneWidget);

    await tester.tap(find.widgetWithText(FilledButton, 'New env var').last);
    await tester.pumpAndSettle();
    var dialogFields = find.descendant(
      of: find.byType(AlertDialog).last,
      matching: find.byType(TextField),
    );
    await tester.enterText(dialogFields.at(0), 'BPT_CODEX_ENV');
    await tester.enterText(dialogFields.at(1), 'codex-value');
    await tester.tap(find.widgetWithText(FilledButton, 'Save').last);
    await tester.pumpAndSettle();

    await tester.tap(find.text('Claude').last);
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'New env var').last);
    await tester.pumpAndSettle();
    dialogFields = find.descendant(
      of: find.byType(AlertDialog).last,
      matching: find.byType(TextField),
    );
    await tester.enterText(dialogFields.at(0), 'BPT_CLAUDE_ENV');
    await tester.enterText(dialogFields.at(1), 'claude-value');
    await tester.tap(find.widgetWithText(FilledButton, 'Save').last);
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'Save').last);
    await tester.pumpAndSettle();

    final saved = apiClient.savedWorker!;
    final codexEnv = saved.agentRuntimeEnv.firstWhere(
      (group) => group.agentType == 'codex',
    );
    final claudeEnv = saved.agentRuntimeEnv.firstWhere(
      (group) => group.agentType == 'claude',
    );
    expect(codexEnv.vars.map((item) => item.key), contains('BPT_CODEX_ENV'));
    expect(claudeEnv.vars.single.key, 'BPT_CLAUDE_ENV');
    expect(claudeEnv.vars.single.valueInput, 'claude-value');
  });
}

void _setSurfaceSize(WidgetTester tester, Size size) {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

class FakeApiClient extends ApiClient {
  FakeApiClient({
    SettingsData? settings,
    WorkerItem? worker,
    List<TaskItem>? tasks,
    List<ProjectItem>? projects,
    List<WorkerItem>? workers,
    List<DomainEventItem>? events,
    TaskItem? detailTask,
    String? terminalCheckError,
  }) : _settings = settings ?? const SettingsData(),
       _tasks = tasks ?? [_task],
       _projects = projects ?? [_project],
       _workers = workers ?? [worker ?? _defaultWorker],
       _domainEvents = events ?? const [],
       _detailTask = detailTask,
       _terminalCheckError = terminalCheckError,
       super('http://manager/graphql');

  final StreamController<DomainEventItem> _events =
      StreamController<DomainEventItem>.broadcast();
  SettingsData _settings;
  final List<TaskItem> _tasks;
  final List<ProjectItem> _projects;
  List<WorkerItem> _workers;
  final List<DomainEventItem> _domainEvents;
  final TaskItem? _detailTask;
  final String? _terminalCheckError;
  int boardFetches = 0;
  int detailFetches = 0;
  int terminalCheckCount = 0;
  bool _completedDetail = false;
  String? createdTaskTitle;
  String? createdTaskWorkerId;
  String? createdTaskAgentType;
  AgentExecutionConfigItem? createdTaskAgentConfig;
  String? createdTaskStartDate;
  String? createdTaskEndDate;
  String? continuedTaskId;
  String? continuedMessage;
  SettingsData? savedSettings;
  WorkerItem? savedWorker;
  String? lastBoardProjectId;
  SortRequest? lastBoardSort;

  void emit(DomainEventItem event) => _events.add(event);

  void emitError(Object error) => _events.addError(error);

  void completeTaskDetail() {
    _completedDetail = true;
  }

  @override
  Future<BoardData> fetchBoardData(
    String view, {
    PageRequest page = const PageRequest(),
    String search = '',
    SortRequest sort = const SortRequest(field: 'CREATED_AT'),
    String projectId = '',
  }) async {
    boardFetches++;
    lastBoardProjectId = projectId;
    lastBoardSort = sort;
    final filteredByProject = projectId.isEmpty
        ? List<TaskItem>.from(_tasks)
        : _tasks.where((task) => task.projectId == projectId).toList();
    final tasks = _sortTasks(_filterTasks(filteredByProject, search), sort);
    final pageTasks = page.slice(tasks);
    return BoardData(
      id: 'default',
      name: 'Default Board',
      type: view,
      totalCount: tasks.length,
      tasks: pageTasks,
      columns: [
        BoardColumnData(
          id: 'CREATED',
          title: 'Pending',
          status: 'CREATED',
          tasks: pageTasks,
        ),
      ],
      calendarItems: pageTasks
          .map(
            (task) => BoardCalendarItemData(
              id: task.id!,
              task: task,
              date: task.createdAt,
              status: task.status,
            ),
          )
          .toList(),
      projects: _projects,
      workers: _workers,
    );
  }

  @override
  Future<List<ProjectItem>> fetchProjects() async => _projects;

  @override
  Future<PagedResult<ProjectItem>> fetchProjectsPage({
    PageRequest page = const PageRequest(),
    String search = '',
    SortRequest sort = const SortRequest(field: 'CREATED_AT'),
  }) async => PagedResult(
    items: page.slice(_sortProjects(_filterProjects(_projects, search), sort)),
    totalCount: _filterProjects(_projects, search).length,
  );

  @override
  Future<List<WorkerItem>> fetchWorkers() async => _workers;

  @override
  Future<PagedResult<WorkerItem>> fetchWorkersPage({
    PageRequest page = const PageRequest(),
    String search = '',
    SortRequest sort = const SortRequest(field: 'CREATED_AT'),
  }) async => PagedResult(
    items: page.slice(_sortWorkers(_filterWorkers(_workers, search), sort)),
    totalCount: _filterWorkers(_workers, search).length,
  );

  @override
  Future<List<DomainEventItem>> fetchEvents() async => _domainEvents;

  @override
  Future<PagedResult<DomainEventItem>> fetchEventsPage({
    PageRequest page = const PageRequest(),
    String search = '',
    SortRequest sort = const SortRequest(field: 'OCCURRED_AT'),
  }) async => PagedResult(
    items: page.slice(_sortEvents(_filterEvents(_domainEvents, search), sort)),
    totalCount: _filterEvents(_domainEvents, search).length,
  );

  @override
  Future<SettingsData> fetchSettings() async => _settings;

  @override
  Future<void> updateSettings(SettingsData settings) async {
    savedSettings = settings;
    _settings = settings;
  }

  @override
  Future<void> updateWorker(WorkerItem worker) async {
    savedWorker = worker;
    _workers = [
      for (final existing in _workers)
        if (existing.id == worker.id) worker else existing,
    ];
    if (!_workers.any((existing) => existing.id == worker.id)) {
      _workers = [worker];
    }
  }

  @override
  Future<void> createTask({
    required String title,
    String description = '',
    required String projectId,
    String? workerId,
    String? agentType,
    AgentExecutionConfigItem? agentConfig,
    String baseBranch = 'main',
    List<String> preCommands = const [],
    List<String> postCommands = const [],
    String? startDate,
    String? endDate,
  }) async {
    createdTaskTitle = title;
    createdTaskWorkerId = workerId;
    createdTaskAgentType = agentType;
    createdTaskAgentConfig = agentConfig;
    createdTaskStartDate = startDate;
    createdTaskEndDate = endDate;
  }

  @override
  Future<void> continueTask(String taskId, String message) async {
    continuedTaskId = taskId;
    continuedMessage = message;
  }

  @override
  Stream<DomainEventItem> subscribeDomainEvents({
    String? aggregateId,
    String? aggregateType,
    String? eventType,
  }) => _events.stream;

  @override
  Future<TaskDetailData> fetchTaskDetail(String taskId) async {
    detailFetches++;
    final task = _detailTask ?? (_completedDetail ? _completedTask : _task);
    return TaskDetailData(
      task: task,
      logs: _completedDetail
          ? const [
              TaskLogItem(stream: 'stdout', content: 'done from subscription'),
            ]
          : const [],
      conversations: _completedDetail
          ? const [
              ConversationItem(
                role: 'assistant',
                content: 'conversation from subscription',
                metadata: {},
              ),
            ]
          : const [],
      interactions: const [],
      events: _completedDetail
          ? const [
              DomainEventItem(
                eventId: 'event-task-completed',
                eventType: 'TaskCompleted',
                aggregateType: 'Task',
                aggregateId: 'task-1',
                aggregateVersion: 2,
                payload: '{}',
                occurredAt: '2026-04-25T00:00:01Z',
              ),
            ]
          : const [],
    );
  }

  @override
  Future<void> checkWorkerTerminal(String taskId) async {
    terminalCheckCount++;
    final error = _terminalCheckError;
    if (error != null) {
      throw StateError(error);
    }
  }

  @override
  void dispose() {
    _events.close();
    super.dispose();
  }
}

List<TaskItem> _filterTasks(List<TaskItem> tasks, String search) {
  final query = search.trim().toLowerCase();
  if (query.isEmpty) {
    return List<TaskItem>.from(tasks);
  }
  return tasks
      .where(
        (task) => _containsQuery(query, [
          task.id ?? '',
          task.title,
          task.description,
          task.status,
          task.projectId,
          task.workerId ?? '',
          task.agentType,
          task.baseBranch,
          task.worktreePath ?? '',
          task.agentSessionId ?? '',
          task.result ?? '',
        ]),
      )
      .toList();
}

List<ProjectItem> _filterProjects(List<ProjectItem> projects, String search) {
  final query = search.trim().toLowerCase();
  if (query.isEmpty) {
    return List<ProjectItem>.from(projects);
  }
  return projects
      .where(
        (project) => _containsQuery(query, [
          project.id,
          project.name,
          project.gitUrl,
          project.defaultBranch,
          project.worktreeNamePrefix,
        ]),
      )
      .toList();
}

List<WorkerItem> _filterWorkers(List<WorkerItem> workers, String search) {
  final query = search.trim().toLowerCase();
  if (query.isEmpty) {
    return List<WorkerItem>.from(workers);
  }
  return workers
      .where(
        (worker) => _containsQuery(query, [
          worker.id,
          worker.name,
          worker.status,
          ...worker.supportedAgents,
          worker.workDir,
          worker.startupCommand,
          worker.projectBindingMode,
          ...worker.boundProjectIds,
          ...worker.currentTaskIds,
        ]),
      )
      .toList();
}

List<DomainEventItem> _filterEvents(
  List<DomainEventItem> events,
  String search,
) {
  final query = search.trim().toLowerCase();
  if (query.isEmpty) {
    return List<DomainEventItem>.from(events);
  }
  return events
      .where(
        (event) => _containsQuery(query, [
          event.eventId,
          event.eventType,
          event.aggregateType,
          event.aggregateId,
          event.payload,
        ]),
      )
      .toList();
}

bool _containsQuery(String query, List<String> fields) =>
    fields.any((field) => field.toLowerCase().contains(query));

List<TaskItem> _sortTasks(List<TaskItem> tasks, SortRequest sort) {
  final out = List<TaskItem>.from(tasks);
  out.sort((a, b) {
    final cmp = switch (sort.field) {
      'UPDATED_AT' => a.updatedAt.compareTo(b.updatedAt),
      'TITLE' => a.title.toLowerCase().compareTo(b.title.toLowerCase()),
      'STATUS' => a.status.compareTo(b.status),
      'START_DATE' => a.startDate.compareTo(b.startDate),
      'END_DATE' => a.endDate.compareTo(b.endDate),
      _ => a.createdAt.compareTo(b.createdAt),
    };
    if (cmp != 0) {
      return _directed(cmp, sort.direction);
    }
    return _fallback(a.createdAt, b.createdAt, a.id ?? '', b.id ?? '');
  });
  return out;
}

List<ProjectItem> _sortProjects(List<ProjectItem> projects, SortRequest sort) {
  final out = List<ProjectItem>.from(projects);
  out.sort((a, b) {
    final cmp = switch (sort.field) {
      'UPDATED_AT' => a.updatedAt.compareTo(b.updatedAt),
      'NAME' => a.name.toLowerCase().compareTo(b.name.toLowerCase()),
      'GIT_URL' => a.gitUrl.toLowerCase().compareTo(b.gitUrl.toLowerCase()),
      'DEFAULT_BRANCH' => a.defaultBranch.toLowerCase().compareTo(
        b.defaultBranch.toLowerCase(),
      ),
      _ => a.createdAt.compareTo(b.createdAt),
    };
    if (cmp != 0) {
      return _directed(cmp, sort.direction);
    }
    return _fallback(a.createdAt, b.createdAt, a.id, b.id);
  });
  return out;
}

List<WorkerItem> _sortWorkers(List<WorkerItem> workers, SortRequest sort) {
  final out = List<WorkerItem>.from(workers);
  out.sort((a, b) {
    final cmp = switch (sort.field) {
      'UPDATED_AT' => a.updatedAt.compareTo(b.updatedAt),
      'NAME' => a.name.toLowerCase().compareTo(b.name.toLowerCase()),
      'STATUS' => a.status.compareTo(b.status),
      'LAST_HEARTBEAT_AT' => (a.lastHeartbeatAt ?? '').compareTo(
        b.lastHeartbeatAt ?? '',
      ),
      _ => a.createdAt.compareTo(b.createdAt),
    };
    if (cmp != 0) {
      return _directed(cmp, sort.direction);
    }
    return _fallback(a.createdAt, b.createdAt, a.id, b.id);
  });
  return out;
}

List<DomainEventItem> _sortEvents(
  List<DomainEventItem> events,
  SortRequest sort,
) {
  final out = List<DomainEventItem>.from(events);
  out.sort((a, b) {
    final cmp = switch (sort.field) {
      'EVENT_TYPE' => a.eventType.toLowerCase().compareTo(
        b.eventType.toLowerCase(),
      ),
      'AGGREGATE_TYPE' => a.aggregateType.toLowerCase().compareTo(
        b.aggregateType.toLowerCase(),
      ),
      'AGGREGATE_ID' => a.aggregateId.toLowerCase().compareTo(
        b.aggregateId.toLowerCase(),
      ),
      _ => a.occurredAt.compareTo(b.occurredAt),
    };
    if (cmp != 0) {
      return _directed(cmp, sort.direction);
    }
    return _fallback(a.occurredAt, b.occurredAt, a.eventId, b.eventId);
  });
  return out;
}

int _directed(int cmp, String direction) =>
    direction == SortRequest.ascending ? cmp : -cmp;

int _fallback(String aCreated, String bCreated, String aId, String bId) {
  final created = bCreated.compareTo(aCreated);
  if (created != 0) {
    return created;
  }
  return bId.compareTo(aId);
}

class RecordingApiClient extends ApiClient {
  RecordingApiClient() : super('http://manager/graphql');

  Map<String, dynamic>? lastVariables;

  @override
  Future<Map<String, dynamic>> graphQL(
    String query, {
    Map<String, dynamic>? variables,
  }) async {
    lastVariables = variables;
    return <String, dynamic>{};
  }
}

class BoardRecordingApiClient extends ApiClient {
  BoardRecordingApiClient() : super('http://manager/graphql');

  Map<String, dynamic>? boardVariables;

  @override
  Future<Map<String, dynamic>> graphQL(
    String query, {
    Map<String, dynamic>? variables,
  }) async {
    if (query.contains('query Board')) {
      boardVariables = variables;
      return {
        'board': {
          'id': 'default',
          'name': 'Default Board',
          'type': 'KANBAN',
          'totalCount': 0,
          'columns': [],
          'calendarItems': [],
          'tasks': [],
        },
      };
    }
    if (query.contains('query Projects')) {
      return {'projects': []};
    }
    if (query.contains('query Workers')) {
      return {'workers': []};
    }
    return const <String, dynamic>{};
  }
}

final _project = ProjectItem(
  id: 'project-1',
  name: 'Platform',
  gitUrl: 'git@example.com:platform.git',
  defaultBranch: 'main',
  worktreeNamePrefix: 'platform',
  archived: false,
);

final _defaultWorker = WorkerItem(
  id: 'worker-1',
  name: 'Local worker',
  status: 'ONLINE',
  supportedAgents: const ['codex'],
  workDir: '/tmp/worker',
  startupCommand: '',
  projectBindingMode: 'ALL_PROJECTS',
  boundProjectIds: const [],
  currentTaskIds: const [],
  capabilities: const {
    'terminal_enabled': 'true',
    'terminal_host': '127.0.0.1',
    'terminal_port': '42001',
  },
);

final _task = TaskItem(
  id: 'task-1',
  title: 'Refresh board',
  description: 'Keep board up to date',
  status: 'CREATED',
  projectId: _project.id,
  agentType: 'codex',
  agentConfig: const AgentExecutionConfigItem(),
  baseBranch: 'main',
  preCommands: const [],
  postCommands: const [],
  startDate: '2026-04-25T00:00:00Z',
  endDate: '2026-04-26T00:00:00Z',
  createdAt: '2026-04-25T00:00:00Z',
  updatedAt: '2026-04-25T00:00:00Z',
  workerId: 'worker-1',
);

TaskItem _taskWith({
  required String id,
  required String title,
  String? description,
  String? status,
  String? startDate,
  String? endDate,
  String? createdAt,
  String? updatedAt,
  String? projectId,
}) => TaskItem(
  id: id,
  title: title,
  description: description ?? _task.description,
  status: status ?? _task.status,
  projectId: projectId ?? _task.projectId,
  agentType: _task.agentType,
  agentConfig: _task.agentConfig,
  baseBranch: _task.baseBranch,
  preCommands: _task.preCommands,
  postCommands: _task.postCommands,
  startDate: startDate ?? _task.startDate,
  endDate: endDate ?? _task.endDate,
  createdAt: createdAt ?? _task.createdAt,
  updatedAt: updatedAt ?? _task.updatedAt,
  workerId: _task.workerId,
  worktreePath: _task.worktreePath,
  agentSessionId: _task.agentSessionId,
  result: _task.result,
);

final _completedTask = TaskItem(
  id: 'task-1',
  title: 'Refresh board',
  description: 'Keep board up to date',
  status: 'COMPLETED',
  projectId: _project.id,
  agentType: 'codex',
  agentConfig: const AgentExecutionConfigItem(),
  baseBranch: 'main',
  preCommands: const [],
  postCommands: const [],
  startDate: '2026-04-25T00:00:00Z',
  endDate: '2026-04-26T00:00:00Z',
  result: 'done',
  worktreePath: '/tmp/worker/task-worktree',
  workerId: 'worker-1',
  agentSessionId: 'session-1',
  createdAt: '2026-04-25T00:00:00Z',
  updatedAt: '2026-04-25T00:00:01Z',
);
