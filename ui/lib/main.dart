import 'package:flutter/material.dart';

import 'api_client.dart';
import 'app.dart';

export 'api_client.dart';
export 'app.dart';
export 'models.dart';

void main() {
  runApp(BlockPlayTableApp(apiClient: ApiClient.fromEnvironment()));
}
