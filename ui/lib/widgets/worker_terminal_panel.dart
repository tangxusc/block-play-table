import 'dart:async';

import 'package:flutter/material.dart';
import 'package:xterm/xterm.dart';

import '../worker_terminal_socket.dart';
import '../widgets.dart';

class WorkerTerminalPanel extends StatefulWidget {
  const WorkerTerminalPanel({
    super.key,
    required this.terminalUrl,
    required this.checkTerminal,
    required this.unavailableReason,
  });

  final String terminalUrl;
  final Future<void> Function() checkTerminal;
  final String unavailableReason;

  @override
  State<WorkerTerminalPanel> createState() => _WorkerTerminalPanelState();
}

class _WorkerTerminalPanelState extends State<WorkerTerminalPanel> {
  late final Terminal _terminal = Terminal(
    maxLines: 2000,
    onOutput: _sendInput,
    onResize: (width, height, pixelWidth, pixelHeight) {
      _socket?.sendResize(height, width);
    },
  );
  WorkerTerminalSocket? _socket;
  StreamSubscription<WorkerTerminalEvent>? _subscription;
  String _status = 'Disconnected';
  String _error = '';
  bool _connecting = false;

  @override
  void didUpdateWidget(covariant WorkerTerminalPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.terminalUrl != widget.terminalUrl ||
        oldWidget.unavailableReason != widget.unavailableReason) {
      _disconnect();
    }
  }

  @override
  void dispose() {
    _disconnect(notify: false);
    super.dispose();
  }

  Future<void> _connect() async {
    final reason = widget.unavailableReason;
    if (reason.isNotEmpty || _socket != null || _connecting) {
      return;
    }
    setState(() {
      _connecting = true;
      _status = 'Checking';
      _error = '';
    });
    try {
      await widget.checkTerminal();
      if (!mounted) {
        return;
      }
      _terminal.write('\r\nConnecting worker terminal...\r\n');
      final socket = connectWorkerTerminal(widget.terminalUrl);
      _socket = socket;
      _subscription = socket.events.listen(_handleEvent);
      setState(() {
        _connecting = false;
        _status = 'Connected';
        _error = '';
      });
    } catch (error) {
      if (!mounted) {
        return;
      }
      final message = _terminalErrorMessage(error);
      _terminal.write('\r\n[terminal error] $message\r\n');
      setState(() {
        _connecting = false;
        _status = 'Disconnected';
        _error = message;
      });
    }
  }

  void _disconnect({bool notify = true}) {
    _subscription?.cancel();
    _subscription = null;
    _socket?.close();
    _socket = null;
    _connecting = false;
    if (mounted && notify) {
      setState(() {
        _status = 'Disconnected';
      });
    } else {
      _status = 'Disconnected';
    }
  }

  void _sendInput(String data) {
    _socket?.sendInput(data);
  }

  void _handleEvent(WorkerTerminalEvent event) {
    if (!mounted) {
      return;
    }
    switch (event.type) {
      case 'output':
        _terminal.write(event.data);
        break;
      case 'exit':
        _terminal.write('\r\n[terminal exited]\r\n');
        _subscription?.cancel();
        _subscription = null;
        _socket = null;
        setState(() {
          _connecting = false;
          _status = 'Disconnected';
        });
        break;
      case 'error':
        _terminal.write('\r\n[terminal error] ${event.data}\r\n');
        setState(() {
          _connecting = false;
          _error = event.data;
          _status = 'Disconnected';
        });
        break;
      default:
        break;
    }
  }

  @override
  Widget build(BuildContext context) {
    final reason = widget.unavailableReason;
    return Padding(
      padding: const EdgeInsets.only(top: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              StatusPill(value: _status),
              const SizedBox(width: 10),
              Expanded(
                child: SelectableText(
                  widget.terminalUrl,
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ),
              const SizedBox(width: 8),
              if (reason.isEmpty)
                IconButton.filled(
                  tooltip: _connecting
                      ? 'Connecting worker terminal'
                      : _socket == null
                          ? 'Connect worker terminal'
                          : 'Disconnect worker terminal',
                  onPressed: _connecting
                      ? null
                      : (_socket == null ? _connect : _disconnect),
                  icon: Icon(_socket == null ? Icons.power : Icons.power_off),
                ),
            ],
          ),
          if (reason.isNotEmpty) ...[
            const SizedBox(height: 16),
            _TerminalUnavailable(reason: reason),
          ] else ...[
            if (_error.isNotEmpty) ...[
              const SizedBox(height: 8),
              Text(
                _error,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ],
            const SizedBox(height: 12),
            Expanded(
              child: DecoratedBox(
                decoration: BoxDecoration(
                  color: const Color(0xff111318),
                  borderRadius: BorderRadius.circular(6),
                  border: Border.all(color: Theme.of(context).dividerColor),
                ),
                child: TerminalView(
                  _terminal,
                  autofocus: true,
                  padding: const EdgeInsets.all(10),
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

String _terminalErrorMessage(Object error) {
  final text = error.toString();
  return text
      .replaceFirst(RegExp(r'^Bad state:\s*'), '')
      .replaceFirst(RegExp(r'^Exception:\s*'), '')
      .trim();
}

class _TerminalUnavailable extends StatelessWidget {
  const _TerminalUnavailable({required this.reason});

  final String reason;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return DecoratedBox(
      decoration: BoxDecoration(
        color: scheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: Theme.of(context).dividerColor),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              'Terminal unavailable',
              style: Theme.of(context).textTheme.titleMedium,
            ),
            const SizedBox(height: 6),
            Text(reason),
          ],
        ),
      ),
    );
  }
}
