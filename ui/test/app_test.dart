import 'dart:async';

import 'package:block_play_table_ui/main.dart';
import 'package:flutter/material.dart';
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

  test('updateWorker sends public env values and preserves blank secrets',
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
    final vars = (agentEnv[0] as Map<String, dynamic>)['vars'] as List<dynamic>;
    final public = vars[0] as Map<String, dynamic>;
    final secret = vars[1] as Map<String, dynamic>;

    expect(public, containsPair('value', ''));
    expect(secret.containsKey('value'), isFalse);
  });

  testWidgets(
    'renders workspace navigation without Tasks and opens task dialog',
    (tester) async {
      final apiClient = FakeApiClient();
      await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
      await tester.pumpAndSettle();

      expect(find.text('Block Play Table'), findsOneWidget);
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

    final pendingRect =
        tester.getRect(find.byKey(const ValueKey('kanban-column-pending')));
    final runningRect =
        tester.getRect(find.byKey(const ValueKey('kanban-column-running')));
    final completeRect =
        tester.getRect(find.byKey(const ValueKey('kanban-column-complete')));

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

    final pendingRect =
        tester.getRect(find.byKey(const ValueKey('kanban-column-pending')));
    final runningRect =
        tester.getRect(find.byKey(const ValueKey('kanban-column-running')));
    final completeRect =
        tester.getRect(find.byKey(const ValueKey('kanban-column-complete')));

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
      ),
    );
    final apiClient = FakeApiClient(tasks: overflowTasks);

    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.byType(Scrollbar), findsWidgets);
    expect(find.text('Overflow task 0'), findsOneWidget);
    expect(find.text('Overflow task 17'), findsNothing);

    await tester.drag(find.text('Overflow task 0'), const Offset(0, -1200));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.text('Overflow task 17'), findsOneWidget);
  });

  testWidgets('create task can save without worker or agent', (tester) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'New task'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, 'Agentless task');
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, 'Save'));
    await tester.pumpAndSettle();

    expect(apiClient.createdTaskTitle, 'Agentless task');
    expect(apiClient.createdTaskWorkerId, isNull);
    expect(apiClient.createdTaskAgentType, isNull);
  });

  testWidgets('selecting worker enables supported agent selection on create', (
    tester,
  ) async {
    final apiClient = FakeApiClient();
    await tester.pumpWidget(BlockPlayTableApp(apiClient: apiClient));
    await tester.pumpAndSettle();

    await tester.tap(find.widgetWithText(FilledButton, 'New task'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, 'Worker task');
    await tester.pump();
    await tester.tap(find.text('Unassigned').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Local worker').last);
    await tester.pumpAndSettle();

    expect(find.text('Codex'), findsOneWidget);
    expect(find.text('Claude'), findsNothing);

    await tester.tap(find.widgetWithText(FilledButton, 'Save'));
    await tester.pumpAndSettle();

    expect(apiClient.createdTaskTitle, 'Worker task');
    expect(apiClient.createdTaskWorkerId, 'worker-1');
    expect(apiClient.createdTaskAgentType, 'codex');
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
    expect(find.text('Logs'), findsOneWidget);
    expect(find.text('Conversation'), findsOneWidget);
    expect(find.text('Domain events'), findsOneWidget);
    final tabLabels = tester
        .widgetList<Tab>(find.byType(Tab))
        .map((tab) => tab.text)
        .toList();
    expect(tabLabels, ['Conversation', 'Logs', 'Domain events']);
    expect(
        find.textContaining('conversation from subscription'), findsOneWidget);
    expect(find.textContaining('done from subscription'), findsNothing);
    expect(find.textContaining('TaskCompleted v2: {}'), findsNothing);

    await tester.tap(find.text('Logs'));
    await tester.pumpAndSettle();

    expect(find.textContaining('done from subscription'), findsOneWidget);

    await tester.tap(find.text('Domain events'));
    await tester.pumpAndSettle();

    expect(find.textContaining('TaskCompleted v2: {}'), findsOneWidget);
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
  })
      : _settings = settings ?? const SettingsData(),
        _worker = worker ?? _defaultWorker,
        _tasks = tasks ?? [_task],
        super('http://manager/graphql');

  final StreamController<DomainEventItem> _events =
      StreamController<DomainEventItem>.broadcast();
  SettingsData _settings;
  WorkerItem _worker;
  final List<TaskItem> _tasks;
  int boardFetches = 0;
  int detailFetches = 0;
  bool _completedDetail = false;
  String? createdTaskTitle;
  String? createdTaskWorkerId;
  String? createdTaskAgentType;
  String? continuedTaskId;
  String? continuedMessage;
  SettingsData? savedSettings;
  WorkerItem? savedWorker;

  void emit(DomainEventItem event) => _events.add(event);

  void emitError(Object error) => _events.addError(error);

  void completeTaskDetail() {
    _completedDetail = true;
  }

  @override
  Future<BoardData> fetchBoardData(String view) async {
    boardFetches++;
    return BoardData(
      id: 'default',
      name: 'Default Board',
      type: view,
      tasks: _tasks,
      columns: [
        BoardColumnData(
          id: 'CREATED',
          title: 'Pending',
          status: 'CREATED',
          tasks: _tasks,
        ),
      ],
      calendarItems: _tasks
          .map(
            (task) => BoardCalendarItemData(
              id: task.id!,
              task: task,
              date: task.createdAt,
              status: task.status,
            ),
          )
          .toList(),
      projects: [_project],
      workers: [_worker],
    );
  }

  @override
  Future<List<ProjectItem>> fetchProjects() async => [_project];

  @override
  Future<List<WorkerItem>> fetchWorkers() async => [_worker];

  @override
  Future<List<DomainEventItem>> fetchEvents() async => const [];

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
    _worker = worker;
  }

  @override
  Future<void> createTask({
    required String title,
    String description = '',
    required String projectId,
    String? workerId,
    String? agentType,
    String baseBranch = 'main',
    List<String> preCommands = const [],
    List<String> postCommands = const [],
  }) async {
    createdTaskTitle = title;
    createdTaskWorkerId = workerId;
    createdTaskAgentType = agentType;
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
  }) =>
      _events.stream;

  @override
  Future<TaskDetailData> fetchTaskDetail(String taskId) async {
    detailFetches++;
    final task = _completedDetail ? _completedTask : _task;
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
              ),
            ]
          : const [],
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
  void dispose() {
    _events.close();
    super.dispose();
  }
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
);

final _task = TaskItem(
  id: 'task-1',
  title: 'Refresh board',
  description: 'Keep board up to date',
  status: 'CREATED',
  projectId: _project.id,
  agentType: 'codex',
  baseBranch: 'main',
  preCommands: const [],
  postCommands: const [],
  createdAt: '2026-04-25T00:00:00Z',
  updatedAt: '2026-04-25T00:00:00Z',
);

TaskItem _taskWith({
  required String id,
  required String title,
}) =>
    TaskItem(
      id: id,
      title: title,
      description: _task.description,
      status: _task.status,
      projectId: _task.projectId,
      agentType: _task.agentType,
      baseBranch: _task.baseBranch,
      preCommands: _task.preCommands,
      postCommands: _task.postCommands,
      createdAt: _task.createdAt,
      updatedAt: _task.updatedAt,
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
  baseBranch: 'main',
  preCommands: const [],
  postCommands: const [],
  result: 'done',
  agentSessionId: 'session-1',
  createdAt: '2026-04-25T00:00:00Z',
  updatedAt: '2026-04-25T00:00:01Z',
);
