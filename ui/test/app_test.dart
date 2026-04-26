import 'dart:async';

import 'package:block_play_table_ui/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
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
    expect(find.textContaining('done from subscription'), findsOneWidget);
  });
}

class FakeApiClient extends ApiClient {
  FakeApiClient() : super('http://manager/graphql');

  final StreamController<DomainEventItem> _events =
      StreamController<DomainEventItem>.broadcast();
  int boardFetches = 0;
  int detailFetches = 0;
  bool _completedDetail = false;
  String? createdTaskTitle;
  String? createdTaskWorkerId;
  String? createdTaskAgentType;

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
      tasks: [_task],
      columns: [
        BoardColumnData(
          id: 'CREATED',
          title: 'Pending',
          status: 'CREATED',
          tasks: [_task],
        ),
      ],
      calendarItems: [
        BoardCalendarItemData(
          id: _task.id!,
          task: _task,
          date: _task.createdAt,
          status: _task.status,
        ),
      ],
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
  Future<SettingsData> fetchSettings() async =>
      SettingsData(agentRuntimeEnvVars: const []);

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
      conversations: const [],
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

final _project = ProjectItem(
  id: 'project-1',
  name: 'Platform',
  gitUrl: 'git@example.com:platform.git',
  defaultBranch: 'main',
  worktreeNamePrefix: 'platform',
  archived: false,
);

final _worker = WorkerItem(
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
  createdAt: '2026-04-25T00:00:00Z',
  updatedAt: '2026-04-25T00:00:01Z',
);
