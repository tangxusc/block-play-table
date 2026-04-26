import 'dart:async';

import 'models.dart';

class RealtimeRefreshController {
  RealtimeRefreshController({
    required Stream<DomainEventItem> events,
    required this.reload,
    required this.shouldReload,
    this.onEvent,
    this.debounceDuration = const Duration(milliseconds: 300),
    this.pollInterval = const Duration(seconds: 15),
  }) {
    _subscription = events.listen(
      (event) {
        if (_disposed) {
          return;
        }
        onEvent?.call(event);
        if (shouldReload(event)) {
          scheduleReload();
        }
      },
      onError: (_, __) => reloadNow(),
      onDone: reloadNow,
    );
    _pollTimer = Timer.periodic(pollInterval, (_) => reloadNow());
  }

  final void Function() reload;
  final bool Function(DomainEventItem event) shouldReload;
  final void Function(DomainEventItem event)? onEvent;
  final Duration debounceDuration;
  final Duration pollInterval;

  StreamSubscription<DomainEventItem>? _subscription;
  Timer? _debounceTimer;
  Timer? _pollTimer;
  bool _disposed = false;

  void scheduleReload() {
    if (_disposed) {
      return;
    }
    _debounceTimer?.cancel();
    _debounceTimer = Timer(debounceDuration, reloadNow);
  }

  void reloadNow() {
    if (_disposed) {
      return;
    }
    _debounceTimer?.cancel();
    _debounceTimer = null;
    reload();
  }

  void dispose() {
    _disposed = true;
    _debounceTimer?.cancel();
    _pollTimer?.cancel();
    _subscription?.cancel();
  }
}
