import 'package:flutter/material.dart';

import '../api_client.dart';
import '../models.dart';
import '../realtime_refresh.dart';
import '../widgets.dart';

class SettingsPage extends StatefulWidget {
  const SettingsPage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends State<SettingsPage> {
  late Future<SettingsData> _future;
  SettingsData? _settings;
  final _heartbeat = TextEditingController();
  RealtimeRefreshController? _realtime;

  @override
  void initState() {
    super.initState();
    _future = _load();
    _realtime = RealtimeRefreshController(
      events: widget.apiClient.subscribeDomainEvents(aggregateType: 'Settings'),
      reload: _reload,
      shouldReload: (_) => true,
    );
  }

  @override
  void dispose() {
    _realtime?.dispose();
    _heartbeat.dispose();
    super.dispose();
  }

  Future<SettingsData> _load() async {
    final settings = await widget.apiClient.fetchSettings();
    _settings = settings;
    _heartbeat.text = settings.workerHeartbeatTimeout;
    return settings;
  }

  void _reload() {
    if (!mounted) {
      return;
    }
    setState(() {
      _future = _load();
    });
  }

  @override
  Widget build(BuildContext context) {
    return PageScaffold(
      title: 'Settings',
      icon: Icons.settings,
      actions: [
        IconButton(
          tooltip: 'Refresh settings',
          onPressed: _reload,
          icon: const Icon(Icons.refresh),
        ),
        FilledButton.icon(
          onPressed: _settings == null ? null : _save,
          icon: const Icon(Icons.save_outlined),
          label: const Text('Save settings'),
        ),
      ],
      child: FutureBuilder<SettingsData>(
        future: _future,
        builder: (context, snapshot) {
          final settings = snapshot.data ?? _settings;
          if (snapshot.connectionState != ConnectionState.done &&
              settings == null) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError && settings == null) {
            return ErrorView(
              message: snapshot.error.toString(),
              onRetry: _reload,
            );
          }
          return Stack(
            children: [
              ListView(
                padding: const EdgeInsets.all(16),
                children: [
                  Text(
                    'Trusted mode',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const SizedBox(height: 8),
                  Text(
                    'Authentication and authorization are intentionally disabled in this build. Deploy Manager, UI, and Workers only on a trusted network.',
                    style: Theme.of(context).textTheme.bodyMedium,
                  ),
                  const SizedBox(height: 18),
                  SizedBox(
                    width: 260,
                    child: TextField(
                      controller: _heartbeat,
                      decoration: const InputDecoration(
                        labelText: 'Worker heartbeat timeout',
                      ),
                    ),
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

  Future<void> _save() async {
    final settings = _settings;
    if (settings == null) {
      return;
    }
    await widget.apiClient.updateSettings(
      SettingsData(
        workerHeartbeatTimeout:
            _heartbeat.text.trim().isEmpty ? '90s' : _heartbeat.text.trim(),
        securityPolicy: settings.securityPolicy,
      ),
    );
    _reload();
  }
}
