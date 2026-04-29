import 'package:flutter/material.dart';

import '../api_client.dart';
import '../models.dart';
import '../realtime_refresh.dart';
import '../widgets.dart';

class EventsPage extends StatefulWidget {
  const EventsPage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<EventsPage> createState() => _EventsPageState();
}

class _EventsPageState extends State<EventsPage> {
  PageRequest _page = const PageRequest();
  late Future<PagedResult<DomainEventItem>> _future;
  PagedResult<DomainEventItem>? _lastPage;
  RealtimeRefreshController? _realtime;

  @override
  void initState() {
    super.initState();
    _future = _load();
    _realtime = RealtimeRefreshController(
      events: widget.apiClient.subscribeDomainEvents(),
      reload: _reload,
      shouldReload: (_) => true,
      onEvent: (event) {
        if (!mounted) {
          return;
        }
        setState(() {
          final current = _lastPage?.items ?? const <DomainEventItem>[];
          if (current.any((item) => item.eventId == event.eventId)) {
            return;
          }
          if (_page.offset == 0) {
            _lastPage = PagedResult(
              items: [event, ...current].take(_page.limit).toList(),
              totalCount: (_lastPage?.totalCount ?? current.length) + 1,
            );
          }
        });
      },
    );
  }

  @override
  void dispose() {
    _realtime?.dispose();
    super.dispose();
  }

  Future<PagedResult<DomainEventItem>> _load() async {
    var page = await widget.apiClient.fetchEventsPage(page: _page);
    if (page.items.isEmpty && page.totalCount > 0 && _page.offset > 0) {
      final corrected = _page.withOffset(_page.lastOffset(page.totalCount));
      if (corrected.offset != _page.offset) {
        _page = corrected;
        page = await widget.apiClient.fetchEventsPage(page: _page);
      }
    }
    _lastPage = page;
    return page;
  }

  void _reload() {
    if (!mounted) {
      return;
    }
    setState(() {
      _future = _load();
    });
  }

  void _goToPage(PageRequest page) {
    if (!mounted) {
      return;
    }
    setState(() {
      _page = page;
      _future = _load();
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
      child: FutureBuilder<PagedResult<DomainEventItem>>(
        future: _future,
        builder: (context, snapshot) {
          final page = snapshot.data ?? _lastPage;
          if (snapshot.connectionState != ConnectionState.done &&
              page == null) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError && page == null) {
            return ErrorView(
              message: snapshot.error.toString(),
              onRetry: _reload,
            );
          }
          final items = page?.items ?? const <DomainEventItem>[];
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
              Column(
                children: [
                  Expanded(
                    child: ListView.separated(
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
                      separatorBuilder: (context, index) =>
                          const SizedBox(height: 8),
                      itemCount: items.length,
                    ),
                  ),
                  PaginationBar(
                    page: _page,
                    totalCount: page?.totalCount ?? 0,
                    onPageChanged: _goToPage,
                  ),
                ],
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
