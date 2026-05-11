import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'models.dart';

class PageScaffold extends StatelessWidget {
  const PageScaffold({
    super.key,
    required this.title,
    required this.icon,
    required this.actions,
    required this.child,
  });

  final String title;
  final IconData icon;
  final List<Widget> actions;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Container(
          height: 56,
          padding: const EdgeInsets.symmetric(horizontal: 20),
          decoration: BoxDecoration(
            color: Theme.of(context).colorScheme.surface,
            border: Border(
              bottom: BorderSide(color: Theme.of(context).dividerColor),
            ),
          ),
          child: Row(
            children: [
              Icon(icon, size: 20),
              const SizedBox(width: 10),
              Text(title, style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(width: 16),
              Expanded(
                child: Align(
                  alignment: Alignment.centerRight,
                  child: SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    reverse: true,
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: _spacedActions(actions),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
        Expanded(child: child),
      ],
    );
  }
}

List<Widget> _spacedActions(List<Widget> actions) {
  final out = <Widget>[];
  for (var index = 0; index < actions.length; index++) {
    if (index > 0) {
      out.add(const SizedBox(width: 8));
    }
    out.add(actions[index]);
  }
  return out;
}

class SortOption {
  const SortOption({required this.field, required this.label});

  final String field;
  final String label;
}

class SearchSortToolbar extends StatelessWidget {
  const SearchSortToolbar({
    super.key,
    required this.keyPrefix,
    required this.searchController,
    required this.sort,
    required this.sortOptions,
    required this.onSearchChanged,
    required this.onSortChanged,
    this.extraControls = const [],
  });

  final String keyPrefix;
  final TextEditingController searchController;
  final SortRequest sort;
  final List<SortOption> sortOptions;
  final ValueChanged<String> onSearchChanged;
  final ValueChanged<SortRequest> onSortChanged;
  final List<Widget> extraControls;

  @override
  Widget build(BuildContext context) {
    final selectedField =
        sortOptions.any((option) => option.field == sort.field)
            ? sort.field
            : sortOptions.first.field;
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 10, 16, 10),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surface,
        border: Border(
          bottom: BorderSide(color: Theme.of(context).dividerColor),
        ),
      ),
      child: LayoutBuilder(
        builder: (context, constraints) {
          final compact = constraints.maxWidth < 620;
          final searchWidth = compact ? constraints.maxWidth : 320.0;
          final sortWidth = compact ? constraints.maxWidth - 44 : 220.0;
          return Wrap(
            spacing: 10,
            runSpacing: 10,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              SizedBox(
                width: searchWidth,
                height: 40,
                child: TextField(
                  key: ValueKey('$keyPrefix-search-field'),
                  controller: searchController,
                  onChanged: onSearchChanged,
                  decoration: const InputDecoration(
                    prefixIcon: Icon(Icons.search),
                    hintText: 'Search',
                    contentPadding: EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 10,
                    ),
                  ),
                ),
              ),
              SizedBox(
                width: sortWidth < 160 ? 160 : sortWidth,
                height: 40,
                child: DropdownButtonFormField<String>(
                  key: ValueKey('$keyPrefix-sort-field'),
                  isExpanded: true,
                  value: selectedField,
                  decoration: const InputDecoration(labelText: 'Sort by'),
                  items: sortOptions
                      .map(
                        (option) => DropdownMenuItem(
                          value: option.field,
                          child: Text(
                            option.label,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                      )
                      .toList(),
                  onChanged: (value) {
                    if (value != null) {
                      onSortChanged(sort.copyWith(field: value));
                    }
                  },
                ),
              ),
              IconButton(
                key: ValueKey('$keyPrefix-sort-direction'),
                tooltip: sort.direction == SortRequest.ascending
                    ? 'Sort ascending'
                    : 'Sort descending',
                onPressed: () => onSortChanged(sort.toggledDirection()),
                icon: Icon(
                  sort.direction == SortRequest.ascending
                      ? Icons.arrow_upward
                      : Icons.arrow_downward,
                ),
              ),
              ...extraControls,
            ],
          );
        },
      ),
    );
  }
}

class StatusPill extends StatelessWidget {
  const StatusPill({super.key, required this.value});

  final String value;

  @override
  Widget build(BuildContext context) {
    final color = _statusColor(context, value);
    return Semantics(
      label: value,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        decoration: BoxDecoration(
          color: color.withOpacity(0.12),
          border: Border.all(color: color.withOpacity(0.34)),
          borderRadius: BorderRadius.circular(6),
        ),
        child: Text(
          value,
          overflow: TextOverflow.ellipsis,
          style: Theme.of(context).textTheme.labelSmall?.copyWith(color: color),
        ),
      ),
    );
  }
}

class EmptyState extends StatelessWidget {
  const EmptyState({
    super.key,
    required this.icon,
    required this.title,
    this.message = '',
  });

  final IconData icon;
  final String title;
  final String message;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 360),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 34, color: Theme.of(context).colorScheme.outline),
            const SizedBox(height: 12),
            Text(title, style: Theme.of(context).textTheme.titleMedium),
            if (message.isNotEmpty) ...[
              const SizedBox(height: 6),
              Text(
                message,
                textAlign: TextAlign.center,
                style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class ErrorView extends StatelessWidget {
  const ErrorView({super.key, required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 520),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.error_outline,
              size: 34,
              color: Theme.of(context).colorScheme.error,
            ),
            const SizedBox(height: 12),
            Text(message, textAlign: TextAlign.center),
            const SizedBox(height: 12),
            FilledButton.icon(
              onPressed: onRetry,
              icon: const Icon(Icons.refresh),
              label: const Text('Retry'),
            ),
          ],
        ),
      ),
    );
  }
}

class PaginationBar extends StatelessWidget {
  const PaginationBar({
    super.key,
    required this.page,
    required this.totalCount,
    required this.onPageChanged,
  });

  final PageRequest page;
  final int totalCount;
  final ValueChanged<PageRequest> onPageChanged;

  @override
  Widget build(BuildContext context) {
    final lastOffset = page.lastOffset(totalCount);
    var offset = page.offset;
    if (offset < 0) {
      offset = 0;
    }
    if (offset > lastOffset) {
      offset = lastOffset;
    }
    final canGoBack = totalCount > 0 && offset > 0;
    final canGoForward = totalCount > 0 && offset < lastOffset;
    final start = totalCount == 0 ? 0 : offset + 1;
    final end = totalCount == 0 ? 0 : math.min(offset + page.limit, totalCount);

    return Container(
      height: 52,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surface,
        border: Border(top: BorderSide(color: Theme.of(context).dividerColor)),
      ),
      child: Row(
        children: [
          Text(
            totalCount == 0
                ? 'No results'
                : 'Showing $start-$end of $totalCount',
          ),
          const Spacer(),
          IconButton(
            tooltip: 'First page',
            onPressed:
                canGoBack ? () => onPageChanged(page.withOffset(0)) : null,
            icon: const Icon(Icons.first_page),
          ),
          IconButton(
            tooltip: 'Previous page',
            onPressed: canGoBack
                ? () => onPageChanged(page.withOffset(offset - page.limit))
                : null,
            icon: const Icon(Icons.chevron_left),
          ),
          IconButton(
            tooltip: 'Next page',
            onPressed: canGoForward
                ? () => onPageChanged(page.withOffset(offset + page.limit))
                : null,
            icon: const Icon(Icons.chevron_right),
          ),
          IconButton(
            tooltip: 'Last page',
            onPressed: canGoForward
                ? () => onPageChanged(page.withOffset(lastOffset))
                : null,
            icon: const Icon(Icons.last_page),
          ),
        ],
      ),
    );
  }
}

class DetailText extends StatelessWidget {
  const DetailText({super.key, required this.icon, required this.text});

  final IconData icon;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      label: text,
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 16, color: Theme.of(context).colorScheme.outline),
          const SizedBox(width: 6),
          ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 460),
            child: Text(text, overflow: TextOverflow.ellipsis),
          ),
        ],
      ),
    );
  }
}

Future<bool> confirmAction(
  BuildContext context, {
  required String title,
  required String message,
  String confirmLabel = 'Confirm',
}) async {
  final result = await showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: Text(title),
      content: Text(message),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(true),
          child: Text(confirmLabel),
        ),
      ],
    ),
  );
  return result ?? false;
}

Color _statusColor(BuildContext context, String status) {
  final scheme = Theme.of(context).colorScheme;
  return switch (status) {
    'ONLINE' || 'COMPLETED' || 'RUNNING' => const Color(0xff13795b),
    'CREATED' || 'REGISTERED' || 'ASSIGNED' => const Color(0xff476a99),
    'STARTING' || 'WAITING_INPUT' || 'INTERRUPTING' => const Color(0xff9a6700),
    'FAILED' || 'ERROR' => scheme.error,
    'ARCHIVED' ||
    'DISABLED' ||
    'OFFLINE' ||
    'INTERRUPTED' =>
      scheme.onSurfaceVariant,
    _ => scheme.primary,
  };
}
