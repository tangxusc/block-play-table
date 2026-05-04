import 'dart:async';

class WorkerTerminalEvent {
  const WorkerTerminalEvent({required this.type, this.data = '', this.code = 0});

  final String type;
  final String data;
  final int code;
}

class WorkerTerminalSocket {
  WorkerTerminalSocket(String url) {
    scheduleMicrotask(() {
      _events.add(
        WorkerTerminalEvent(
          type: 'error',
          data: 'Worker terminal is only available in Flutter Web',
        ),
      );
    });
  }

  final StreamController<WorkerTerminalEvent> _events =
      StreamController<WorkerTerminalEvent>.broadcast();

  Stream<WorkerTerminalEvent> get events => _events.stream;

  void sendInput(String data) {}

  void sendResize(int rows, int cols) {}

  void close() {
    _events.close();
  }
}

WorkerTerminalSocket connectWorkerTerminal(String url) =>
    WorkerTerminalSocket(url);
