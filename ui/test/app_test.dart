import 'package:block_play_table_ui/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('renders primary navigation and empty task surface', (tester) async {
    await tester.pumpWidget(BlockPlayTableApp(apiClient: FakeApiClient()));
    await tester.pumpAndSettle();

    expect(find.text('Block Play Table'), findsOneWidget);
    expect(find.text('Tasks'), findsWidgets);
    expect(find.byIcon(Icons.view_kanban), findsOneWidget);
    expect(find.byIcon(Icons.folder_copy), findsOneWidget);
  });
}

class FakeApiClient extends ApiClient {
  FakeApiClient() : super('http://manager/graphql');

  @override
  Future<DashboardData> dashboard() async => DashboardData.empty();
}
