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

      await tester.tap(find.widgetWithText(FilledButton, 'New task'));
      await tester.pumpAndSettle();

      expect(find.byType(AlertDialog), findsOneWidget);
      expect(find.text('Create task'), findsOneWidget);
    },
  );

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
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Edit project').first);
    await tester.pumpAndSettle();
    expect(find.text('Edit project'), findsOneWidget);
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
}

class FakeApiClient extends ApiClient {
  FakeApiClient() : super('http://manager/graphql');

  final StreamController<DomainEventItem> _events =
      StreamController<DomainEventItem>.broadcast();
  int boardFetches = 0;

  void emit(DomainEventItem event) => _events.add(event);

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
  Stream<DomainEventItem> subscribeDomainEvents({
    String? aggregateId,
    String? aggregateType,
    String? eventType,
  }) => _events.stream;

  @override
  Future<TaskDetailData> fetchTaskDetail(String taskId) async => TaskDetailData(
    task: _task,
    logs: const [],
    conversations: const [],
    events: const [],
  );

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
  setupCommands: const ['make setup'],
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
  targetBranch: 'task/refresh-board',
  preCommands: const [],
  postCommands: const [],
  createdAt: '2026-04-25T00:00:00Z',
  updatedAt: '2026-04-25T00:00:00Z',
);
