import 'package:flutter/material.dart';

import 'api_client.dart';
import 'pages/board_page.dart';
import 'pages/events_page.dart';
import 'pages/projects_page.dart';
import 'pages/settings_page.dart';
import 'pages/workers_page.dart';

class BlockPlayTableApp extends StatefulWidget {
  const BlockPlayTableApp({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<BlockPlayTableApp> createState() => _BlockPlayTableAppState();
}

class _BlockPlayTableAppState extends State<BlockPlayTableApp> {
  @override
  void dispose() {
    widget.apiClient.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Block Play Table',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xff2d6a6a),
          brightness: Brightness.light,
        ),
        useMaterial3: true,
        visualDensity: VisualDensity.compact,
        inputDecorationTheme: const InputDecorationTheme(
          border: OutlineInputBorder(),
          isDense: true,
        ),
        navigationRailTheme: const NavigationRailThemeData(
          minWidth: 78,
          groupAlignment: -0.82,
        ),
        cardTheme: CardThemeData(
          elevation: 0,
          margin: EdgeInsets.zero,
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
        ),
      ),
      home: HomePage(apiClient: widget.apiClient),
    );
  }
}

class HomePage extends StatefulWidget {
  const HomePage({super.key, required this.apiClient});

  final ApiClient apiClient;

  @override
  State<HomePage> createState() => _HomePageState();
}

class _HomePageState extends State<HomePage> {
  int _selectedIndex = 0;

  @override
  Widget build(BuildContext context) {
    final pages = [
      BoardPage(apiClient: widget.apiClient),
      ProjectsPage(apiClient: widget.apiClient),
      WorkersPage(apiClient: widget.apiClient),
      EventsPage(apiClient: widget.apiClient),
      SettingsPage(apiClient: widget.apiClient),
    ];
    return Scaffold(
      body: Row(
        children: [
          NavigationRail(
            selectedIndex: _selectedIndex,
            onDestinationSelected: (index) =>
                setState(() => _selectedIndex = index),
            labelType: NavigationRailLabelType.all,
            destinations: const [
              NavigationRailDestination(
                icon: Icon(Icons.view_kanban_outlined),
                selectedIcon: Icon(Icons.view_kanban),
                label: Text('Board'),
              ),
              NavigationRailDestination(
                icon: Icon(Icons.folder_copy_outlined),
                selectedIcon: Icon(Icons.folder_copy),
                label: Text('Projects'),
              ),
              NavigationRailDestination(
                icon: Icon(Icons.memory_outlined),
                selectedIcon: Icon(Icons.memory),
                label: Text('Workers'),
              ),
              NavigationRailDestination(
                icon: Icon(Icons.event_note_outlined),
                selectedIcon: Icon(Icons.event_note),
                label: Text('Events'),
              ),
              NavigationRailDestination(
                icon: Icon(Icons.settings_outlined),
                selectedIcon: Icon(Icons.settings),
                label: Text('Settings'),
              ),
            ],
          ),
          const VerticalDivider(width: 1),
          Expanded(
            child: IndexedStack(index: _selectedIndex, children: pages),
          ),
        ],
      ),
    );
  }
}
