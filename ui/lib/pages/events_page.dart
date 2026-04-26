import 'dart:async';

import 'package:flutter/material.dart';

import '../api_client.dart';
import '../models.dart';
import '../widgets.dart';

class EventsPage extends StatefulWidget {
  const EventsPage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<EventsPage> createState() => _EventsPageState();
}

class _EventsPageState extends State<EventsPage> {
  late Future<List<DomainEventItem>> _future;
  List<DomainEventItem>? _events;
  StreamSubscription<DomainEventItem>? _subscription;
  Timer? _refreshTimer;

  @override
  void initState() {
    super.initState();
    _future = _load();
    _subscription = widget.apiClient.subscribeDomainEvents().listen((event) {
      setState(() {
        final current = _events ?? const <DomainEventItem>[];
        if (current.any((item) => item.eventId == event.eventId)) {
          return;
        }
        _events = [...current, event];
      });
      _scheduleReload();
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    _subscription?.cancel();
    super.dispose();
  }

  Future<List<DomainEventItem>> _load() async {
    final events = await widget.apiClient.fetchEvents();
    _events = events;
    return events;
  }

  void _reload() {
    setState(() {
      _future = _load();
    });
  }

  void _scheduleReload() {
    _refreshTimer?.cancel();
    _refreshTimer = Timer(const Duration(milliseconds: 300), () {
      if (mounted) {
        _reload();
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    return PageScaffold(
      title: 'Events',
      icon: Icons.event_note,
      actions: [
        IconButton(
          tooltip: 'Refresh events',
          onPressed: _reload,
          icon: const Icon(Icons.refresh),
        ),
      ],
      child: FutureBuilder<List<DomainEventItem>>(
        future: _future,
        builder: (context, snapshot) {
          final events = snapshot.data ?? _events;
          if (snapshot.connectionState != ConnectionState.done &&
              events == null) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError && events == null) {
            return ErrorView(
              message: snapshot.error.toString(),
              onRetry: _reload,
            );
          }
          final items = (events ?? const <DomainEventItem>[]).reversed.toList();
          if (items.isEmpty) {
            return const EmptyState(
              icon: Icons.event_note_outlined,
              title: 'No domain events',
              message:
                  'Events will appear here as tasks, workers, and projects change.',
            );
          }
          return Stack(
            children: [
              ListView.separated(
                padding: const EdgeInsets.all(16),
                itemBuilder: (context, index) {
                  final event = items[index];
                  return Card(
                    child: ListTile(
                      leading: const Icon(Icons.event_note_outlined),
                      title: Text(event.eventType),
                      subtitle: Text(
                        '${event.aggregateType} ${event.aggregateId}\n${event.payload}',
                        maxLines: 3,
                        overflow: TextOverflow.ellipsis,
                      ),
                      trailing: SizedBox(
                        width: 190,
                        child: Text(
                          event.occurredAt,
                          textAlign: TextAlign.end,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    ),
                  );
                },
                separatorBuilder: (context, index) => const SizedBox(height: 8),
                itemCount: items.length,
              ),
              if (snapshot.connectionState != ConnectionState.done)
                const Positioned(
                  left: 0,
                  right: 0,
                  top: 0,
                  child: LinearProgressIndicator(minHeight: 2),
                ),
            ],
          );
        },
      ),
    );
  }
}
