import 'models.dart';

const _pendingStatuses = {'CREATED', 'PENDING', 'ASSIGNED', 'STARTING'};
const _runningStatuses = {
  'RUNNING',
  'WAITING_INPUT',
  'WAITING',
  'INTERRUPTING',
};

const _groupDefinitions = [
  _BoardStatusGroupDefinition(
    id: 'pending',
    title: 'Pending',
    status: 'CREATED',
  ),
  _BoardStatusGroupDefinition(
    id: 'running',
    title: 'Running',
    status: 'RUNNING',
  ),
  _BoardStatusGroupDefinition(
    id: 'complete',
    title: 'Complete',
    status: 'COMPLETED',
  ),
];

List<BoardColumnData> buildBoardStatusColumns(List<TaskItem> tasks) {
  final groupedTasks = {
    for (final definition in _groupDefinitions)
      definition.id: <TaskItem>[],
  };

  for (final task in tasks) {
    groupedTasks[_groupIdForTaskStatus(task.status)]!.add(task);
  }

  return [
    for (final definition in _groupDefinitions)
      BoardColumnData(
        id: definition.id,
        title: definition.title,
        status: definition.status,
        tasks: groupedTasks[definition.id]!,
      ),
  ];
}

String _groupIdForTaskStatus(String status) {
  final normalized = status.trim().toUpperCase();
  if (_pendingStatuses.contains(normalized)) {
    return 'pending';
  }
  if (_runningStatuses.contains(normalized)) {
    return 'running';
  }
  return 'complete';
}

class _BoardStatusGroupDefinition {
  const _BoardStatusGroupDefinition({
    required this.id,
    required this.title,
    required this.status,
  });

  final String id;
  final String title;
  final String status;
}
