import 'dart:async';
import 'dart:convert';
import 'dart:html' as html;

class WorkerTerminalEvent {
  const WorkerTerminalEvent({required this.type, this.data = '', this.code = 0});

  final String type;
  final String data;
  final int code;
}

class WorkerTerminalSocket {
  WorkerTerminalSocket(String url) : _socket = html.WebSocket(url) {
    _socket.onMessage.listen((event) {
      final raw = event.data;
      if (raw is! String) {
        return;
      }
      final decoded = jsonDecode(raw);
      if (decoded is! Map<String, dynamic>) {
        return;
      }
      _events.add(
        WorkerTerminalEvent(
          type: decoded['type'] as String? ?? '',
          data: decoded['data'] as String? ?? '',
          code: decoded['code'] as int? ?? 0,
        ),
      );
    });
    _socket.onError.listen((_) {
      _events.add(
        const WorkerTerminalEvent(
          type: 'error',
          data: 'Worker terminal connection failed',
        ),
      );
    });
    _socket.onClose.listen((_) {
      _events.add(const WorkerTerminalEvent(type: 'exit'));
      _events.close();
    });
  }

  final html.WebSocket _socket;
  final StreamController<WorkerTerminalEvent> _events =
      StreamController<WorkerTerminalEvent>.broadcast();

  Stream<WorkerTerminalEvent> get events => _events.stream;

  void sendInput(String data) {
    _send({'type': 'input', 'data': data});
  }

  void sendResize(int rows, int cols) {
    _send({'type': 'resize', 'rows': rows, 'cols': cols});
  }

  void close() {
    if (_socket.readyState == html.WebSocket.OPEN) {
      _send({'type': 'close'});
    }
    _socket.close();
  }

  void _send(Map<String, Object?> message) {
    if (_socket.readyState != html.WebSocket.OPEN) {
      return;
    }
    _socket.send(jsonEncode(message));
  }
}

WorkerTerminalSocket connectWorkerTerminal(String url) =>
    WorkerTerminalSocket(url);
