import 'package:block_play_table_ui/board_status_groups.dart';
import 'package:block_play_table_ui/models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('groups task statuses into fixed board columns', () {
    final columns = buildBoardStatusColumns([
      _task('created', 'CREATED'),
      _task('pending', 'PENDING'),
      _task('assigned', 'ASSIGNED'),
      _task('starting', 'STARTING'),
      _task('running', 'RUNNING'),
      _task('waiting-input', 'WAITING_INPUT'),
      _task('waiting', 'WAITING'),
      _task('interrupting', 'INTERRUPTING'),
      _task('failed', 'FAILED'),
      _task('completed', 'COMPLETED'),
      _task('interrupted', 'INTERRUPTED'),
      _task('archived', 'ARCHIVED'),
      _task('unknown', 'BLOCKED'),
    ]);

    expect(columns.map((column) => column.id), [
      'pending',
      'running',
      'complete',
    ]);
    expect(columns.map((column) => column.title), [
      'Pending',
      'Running',
      'Complete',
    ]);
    expect(columns.map((column) => column.status), [
      'CREATED',
      'RUNNING',
      'COMPLETED',
    ]);
    expect(columns[0].tasks.map((task) => task.id), [
      'created',
      'pending',
      'assigned',
      'starting',
    ]);
    expect(columns[1].tasks.map((task) => task.id), [
      'running',
      'waiting-input',
      'waiting',
      'interrupting',
    ]);
    expect(columns[2].tasks.map((task) => task.id), [
      'failed',
      'completed',
      'interrupted',
      'archived',
      'unknown',
    ]);
  });
}

TaskItem _task(String id, String status) => TaskItem(
      id: id,
      title: 'Task $id',
      description: '',
      status: status,
      projectId: 'project-1',
      agentType: 'codex',
      baseBranch: 'main',
      targetBranch: 'task/$id',
      preCommands: const [],
      postCommands: const [],
      createdAt: '2026-04-25T00:00:00Z',
      updatedAt: '2026-04-25T00:00:00Z',
    );
