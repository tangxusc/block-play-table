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
  List<EnvVarItem> _vars = const [];
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
    _vars = List<EnvVarItem>.from(settings.agentRuntimeEnvVars);
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
          onPressed: _settings == null ? null : () => _openEnvDialog(),
          icon: const Icon(Icons.add),
          label: const Text('New env var'),
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
                  const SizedBox(height: 24),
                  Text(
                    'Agent runtime environment',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const SizedBox(height: 12),
                  if (_vars.isEmpty)
                    const EmptyState(
                      icon: Icons.key_outlined,
                      title: 'No environment variables',
                    )
                  else
                    ..._vars.map(
                      (item) => Padding(
                        padding: const EdgeInsets.only(bottom: 8),
                        child: Card(
                          child: ListTile(
                            leading: Icon(
                              item.enabled
                                  ? Icons.toggle_on_outlined
                                  : Icons.toggle_off_outlined,
                            ),
                            title: Text(item.key),
                            subtitle: Text(
                              '${item.valueMasked}  ${item.description}',
                            ),
                            trailing: Wrap(
                              spacing: 8,
                              crossAxisAlignment: WrapCrossAlignment.center,
                              children: [
                                if (item.sensitive)
                                  const Icon(Icons.visibility_off_outlined),
                                IconButton(
                                  tooltip: 'Edit env var',
                                  onPressed: () => _openEnvDialog(item),
                                  icon: const Icon(Icons.edit_outlined),
                                ),
                                IconButton(
                                  tooltip: 'Remove env var',
                                  onPressed: () => _removeEnvVar(item),
                                  icon: const Icon(Icons.delete_outline),
                                ),
                              ],
                            ),
                          ),
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

  Future<void> _openEnvDialog([EnvVarItem? item]) async {
    final result = await showEnvVarDialog(context, item: item);
    if (result == null) {
      return;
    }
    setState(() {
      _vars = [
        ..._vars.where((existing) => existing.key != result.key),
        result,
      ];
    });
    await _save();
  }

  Future<void> _removeEnvVar(EnvVarItem item) async {
    final confirmed = await confirmAction(
      context,
      title: 'Remove environment variable',
      message: 'Remove "${item.key}"?',
      confirmLabel: 'Remove',
    );
    if (!confirmed) {
      return;
    }
    setState(() {
      _vars = _vars.where((existing) => existing.key != item.key).toList();
    });
    await _save();
  }

  Future<void> _save() async {
    final settings = _settings;
    if (settings == null) {
      return;
    }
    await widget.apiClient.updateSettings(
      SettingsData(
        agentRuntimeEnvVars: _vars,
        workerHeartbeatTimeout:
            _heartbeat.text.trim().isEmpty ? '90s' : _heartbeat.text.trim(),
        securityPolicy: settings.securityPolicy,
      ),
    );
    _reload();
  }
}

Future<EnvVarItem?> showEnvVarDialog(BuildContext context, {EnvVarItem? item}) {
  final key = TextEditingController(text: item?.key ?? '');
  final value = TextEditingController();
  final description = TextEditingController(text: item?.description ?? '');
  var enabled = item?.enabled ?? true;
  var sensitive = item?.sensitive ?? true;
  return showDialog<EnvVarItem>(
    context: context,
    builder: (context) => StatefulBuilder(
      builder: (context, setState) => AlertDialog(
        title: Text(item == null ? 'Create env var' : 'Edit env var'),
        content: SizedBox(
          width: 520,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: key,
                decoration: const InputDecoration(labelText: 'Key'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: value,
                decoration: InputDecoration(
                  labelText: item == null ? 'Value' : 'New value',
                  helperText: item == null
                      ? null
                      : 'Leave blank to keep current value.',
                ),
                obscureText: sensitive,
              ),
              const SizedBox(height: 12),
              TextField(
                controller: description,
                decoration: const InputDecoration(labelText: 'Description'),
              ),
              const SizedBox(height: 12),
              Wrap(
                spacing: 8,
                children: [
                  FilterChip(
                    label: const Text('Enabled'),
                    selected: enabled,
                    onSelected: (value) => setState(() => enabled = value),
                  ),
                  FilterChip(
                    label: const Text('Sensitive'),
                    selected: sensitive,
                    onSelected: (value) => setState(() => sensitive = value),
                  ),
                ],
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () {
              if (key.text.trim().isEmpty) {
                return;
              }
              Navigator.of(context).pop(
                EnvVarItem(
                  key: key.text.trim(),
                  valueMasked: sensitive ? '********' : value.text.trim(),
                  description: description.text.trim(),
                  enabled: enabled,
                  sensitive: sensitive,
                  valueInput: value.text,
                ),
              );
            },
            child: const Text('Save'),
          ),
        ],
      ),
    ),
  );
}
