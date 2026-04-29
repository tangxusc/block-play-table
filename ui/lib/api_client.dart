import 'package:graphql_flutter/graphql_flutter.dart';

import 'models.dart';

class ApiClient {
  ApiClient(this.endpoint, {String? websocketEndpoint})
      : websocketEndpoint =
            websocketEndpoint ?? _inferWebsocketEndpoint(endpoint),
        _webSocketLink = WebSocketLink(
          websocketEndpoint ?? _inferWebsocketEndpoint(endpoint),
          config: const SocketClientConfig(
            autoReconnect: true,
            inactivityTimeout: Duration(seconds: 45),
          ),
          subProtocol: GraphQLProtocol.graphqlTransportWs,
        ) {
    final httpLink = HttpLink(endpoint);
    _client = GraphQLClient(
      link: Link.split(
        (request) => request.isSubscription,
        _webSocketLink,
        httpLink,
      ),
      cache: GraphQLCache(store: InMemoryStore()),
    );
  }

  factory ApiClient.fromEnvironment() => ApiClient(
        const String.fromEnvironment(
          'MANAGER_GRAPHQL_URL',
          defaultValue: 'http://localhost:8080/graphql',
        ),
        websocketEndpoint: const String.fromEnvironment(
          'MANAGER_GRAPHQL_WS_URL',
          defaultValue: '',
        ).trim().isEmpty
            ? null
            : const String.fromEnvironment('MANAGER_GRAPHQL_WS_URL'),
      );

  final String endpoint;
  final String websocketEndpoint;
  final WebSocketLink _webSocketLink;
  late final GraphQLClient _client;

  Future<BoardData> fetchBoardData(
    String view, {
    PageRequest page = const PageRequest(),
  }) async {
    final id = switch (view) {
      'CALENDAR' => 'calendar',
      'LIST' => 'list',
      _ => null,
    };
    final boardFuture = graphQL(
      r'''
        query Board($id: ID, $page: PageInput) {
          board(id: $id, page: $page) {
            id
            name
            type
            totalCount
            columns {
              id
              title
              status
              tasks {
                id title description status projectId agentType baseBranch
                agentConfig {
                  workMode
                  codex {
                    model reasoningEffort sandboxMode approvalPolicy
                    fullAuto bypassApprovalsAndSandbox
                  }
                  claude { model effort permissionMode }
                }
                workerId worktreePath agentSessionId preCommands postCommands result startDate endDate createdAt updatedAt
              }
            }
            calendarItems {
              id
              date
              status
              task {
                id title description status projectId agentType baseBranch
                agentConfig {
                  workMode
                  codex {
                    model reasoningEffort sandboxMode approvalPolicy
                    fullAuto bypassApprovalsAndSandbox
                  }
                  claude { model effort permissionMode }
                }
                workerId worktreePath agentSessionId preCommands postCommands result startDate endDate createdAt updatedAt
              }
            }
            tasks {
              id title description status projectId agentType baseBranch
              agentConfig {
                workMode
                codex {
                  model reasoningEffort sandboxMode approvalPolicy
                  fullAuto bypassApprovalsAndSandbox
                }
                claude { model effort permissionMode }
              }
              workerId worktreePath agentSessionId preCommands postCommands result startDate endDate createdAt updatedAt
            }
          }
        }
        ''',
      variables: {'id': id, 'page': page.toGraphQLInput()},
    );
    final projectsFuture = fetchProjects();
    final workersFuture = fetchWorkers();
    final boardData = await boardFuture;
    final projects = await projectsFuture;
    final workers = await workersFuture;
    return BoardData.fromJson(
      boardData['board'] as Map<String, dynamic>? ?? const {},
      projects: projects,
      workers: workers,
    );
  }

  Future<List<ProjectItem>> fetchProjects() async {
    final data = await graphQL(r'''
      query Projects {
        projects(filter: { includeArchived: true }) {
          id name gitUrl defaultBranch worktreeNamePrefix archived
        }
      }
      ''');
    return (data['projects'] as List<dynamic>? ?? [])
        .map((item) => ProjectItem.fromJson(item as Map<String, dynamic>))
        .toList();
  }

  Future<PagedResult<ProjectItem>> fetchProjectsPage({
    PageRequest page = const PageRequest(),
  }) async {
    final data = await graphQL(
      r'''
      query ProjectsPage($page: PageInput) {
        projectsConnection(filter: { includeArchived: true }, page: $page) {
          totalCount
          nodes {
            id name gitUrl defaultBranch worktreeNamePrefix archived
          }
        }
      }
      ''',
      variables: {'page': page.toGraphQLInput()},
    );
    final connection =
        data['projectsConnection'] as Map<String, dynamic>? ?? const {};
    return PagedResult(
      items: (connection['nodes'] as List<dynamic>? ?? [])
          .map((item) => ProjectItem.fromJson(item as Map<String, dynamic>))
          .toList(),
      totalCount: connection['totalCount'] as int? ?? 0,
    );
  }

  Future<List<WorkerItem>> fetchWorkers() async {
    final data = await graphQL(r'''
      query Workers {
        workers(filter: { includeDisabled: true }) {
          id name status supportedAgents workDir startupCommand projectBindingMode
          boundProjectIds currentTaskIds lastHeartbeatAt
          agentRuntimeEnv {
            agentType
            vars { key valueMasked description enabled sensitive }
          }
        }
      }
      ''');
    return (data['workers'] as List<dynamic>? ?? [])
        .map((item) => WorkerItem.fromJson(item as Map<String, dynamic>))
        .toList();
  }

  Future<PagedResult<WorkerItem>> fetchWorkersPage({
    PageRequest page = const PageRequest(),
  }) async {
    final data = await graphQL(
      r'''
      query WorkersPage($page: PageInput) {
        workersConnection(filter: { includeDisabled: true }, page: $page) {
          totalCount
          nodes {
            id name status supportedAgents workDir startupCommand projectBindingMode
            boundProjectIds currentTaskIds lastHeartbeatAt
            agentRuntimeEnv {
              agentType
              vars { key valueMasked description enabled sensitive }
            }
          }
        }
      }
      ''',
      variables: {'page': page.toGraphQLInput()},
    );
    final connection =
        data['workersConnection'] as Map<String, dynamic>? ?? const {};
    return PagedResult(
      items: (connection['nodes'] as List<dynamic>? ?? [])
          .map((item) => WorkerItem.fromJson(item as Map<String, dynamic>))
          .toList(),
      totalCount: connection['totalCount'] as int? ?? 0,
    );
  }

  Future<List<DomainEventItem>> fetchEvents() async {
    final data = await graphQL(r'''
      query Events {
        domainEvents {
          eventId eventType aggregateType aggregateId aggregateVersion payload occurredAt
        }
      }
      ''');
    return (data['domainEvents'] as List<dynamic>? ?? [])
        .map((item) => DomainEventItem.fromJson(item as Map<String, dynamic>))
        .toList();
  }

  Future<PagedResult<DomainEventItem>> fetchEventsPage({
    PageRequest page = const PageRequest(),
  }) async {
    final data = await graphQL(
      r'''
      query EventsPage($page: PageInput) {
        domainEventsConnection(page: $page) {
          totalCount
          nodes {
            eventId eventType aggregateType aggregateId aggregateVersion payload occurredAt
          }
        }
      }
      ''',
      variables: {'page': page.toGraphQLInput()},
    );
    final connection =
        data['domainEventsConnection'] as Map<String, dynamic>? ?? const {};
    return PagedResult(
      items: (connection['nodes'] as List<dynamic>? ?? [])
          .map((item) => DomainEventItem.fromJson(item as Map<String, dynamic>))
          .toList(),
      totalCount: connection['totalCount'] as int? ?? 0,
    );
  }

  Future<SettingsData> fetchSettings() async {
    final data = await graphQL(r'''
      query Settings {
        settings {
          id
          workerHeartbeatTimeout
          securityPolicy
        }
      }
      ''');
    return SettingsData.fromJson(
      data['settings'] as Map<String, dynamic>? ?? const {},
    );
  }

  Future<TaskDetailData> fetchTaskDetail(String taskId) async {
    final results = await Future.wait([
      graphQL(
        r'''
        query Task($id: ID!) {
          task(id: $id) {
            id title description status projectId agentType baseBranch
            agentConfig {
              workMode
              codex {
                model reasoningEffort sandboxMode approvalPolicy
                fullAuto bypassApprovalsAndSandbox
              }
              claude { model effort permissionMode }
            }
            workerId worktreePath agentSessionId preCommands postCommands result startDate endDate createdAt updatedAt
          }
        }
        ''',
        variables: {'id': taskId},
      ),
      graphQL(
        r'''
        query TaskLogs($taskId: ID!) {
          taskLogs(taskId: $taskId) { id stream content createdAt }
        }
        ''',
        variables: {'taskId': taskId},
      ),
      graphQL(
        r'''
        query TaskConversations($taskId: ID!) {
          taskConversations(taskId: $taskId) {
            id role content metadata { key value } createdAt
          }
        }
        ''',
        variables: {'taskId': taskId},
      ),
      graphQL(
        r'''
        query TaskInteractions($taskId: ID!) {
          taskInteractions(taskId: $taskId, status: PENDING) {
            id taskId kind status title body rawPayload agentSessionId
            responseDecision responseMessage responsePayload createdAt updatedAt
          }
        }
        ''',
        variables: {'taskId': taskId},
      ),
      graphQL(
        r'''
        query TaskEvents($taskId: ID!) {
          taskEvents(taskId: $taskId) {
            eventId eventType aggregateType aggregateId aggregateVersion payload occurredAt
          }
        }
        ''',
        variables: {'taskId': taskId},
      ),
    ]);
    return TaskDetailData(
      task: TaskItem.fromJson(results[0]['task'] as Map<String, dynamic>),
      logs: (results[1]['taskLogs'] as List<dynamic>? ?? [])
          .map((item) => TaskLogItem.fromJson(item as Map<String, dynamic>))
          .toList(),
      conversations: (results[2]['taskConversations'] as List<dynamic>? ?? [])
          .map(
            (item) => ConversationItem.fromJson(item as Map<String, dynamic>),
          )
          .toList(),
      interactions: (results[3]['taskInteractions'] as List<dynamic>? ?? [])
          .map(
            (item) =>
                TaskInteractionItem.fromJson(item as Map<String, dynamic>),
          )
          .toList(),
      events: (results[4]['taskEvents'] as List<dynamic>? ?? [])
          .map((item) => DomainEventItem.fromJson(item as Map<String, dynamic>))
          .toList(),
    );
  }

  Stream<DomainEventItem> subscribeDomainEvents({
    String? aggregateId,
    String? aggregateType,
    String? eventType,
  }) {
    final filter = <String, dynamic>{
      if (aggregateId != null) 'aggregateId': aggregateId,
      if (aggregateType != null) 'aggregateType': aggregateType,
      if (eventType != null) 'eventType': eventType,
    };
    return _client
        .subscribe(
      SubscriptionOptions(
        document: gql(r'''
              subscription Events($filter: DomainEventFilter) {
                domainEvents(filter: $filter) {
                  eventId eventType aggregateType aggregateId aggregateVersion payload occurredAt
                }
              }
              '''),
        variables: {'filter': filter.isEmpty ? null : filter},
        fetchPolicy: FetchPolicy.noCache,
      ),
    )
        .map((result) {
      if (result.hasException) {
        throw StateError(result.exception.toString());
      }
      final data = result.data;
      if (data == null) {
        throw StateError('subscription yielded no data');
      }
      return DomainEventItem.fromJson(
        data['domainEvents'] as Map<String, dynamic>,
      );
    });
  }

  Future<void> createProject({
    required String name,
    required String gitUrl,
    required String prefix,
    String defaultBranch = 'main',
  }) =>
      graphQL(
        r'''
        mutation CreateProject($input: CreateProjectInput!) {
          createProject(input: $input) { id }
        }
        ''',
        variables: {
          'input': {
            'name': name,
            'gitUrl': gitUrl,
            'defaultBranch': defaultBranch,
            'worktreeNamePrefix': prefix,
          },
        },
      ).then((_) {});

  Future<void> updateProject(ProjectItem project) => graphQL(
        r'''
        mutation UpdateProject($input: UpdateProjectInput!) {
          updateProject(input: $input) { id }
        }
        ''',
        variables: {
          'input': {
            'id': project.id,
            'name': project.name,
            'gitUrl': project.gitUrl,
            'defaultBranch': project.defaultBranch,
            'worktreeNamePrefix': project.worktreeNamePrefix,
          },
        },
      ).then((_) {});

  Future<void> archiveProject(String projectId) => graphQL(
        r'''
        mutation ArchiveProject($id: ID!) {
          archiveProject(id: $id) { id }
        }
        ''',
        variables: {'id': projectId},
      ).then((_) {});

  Future<void> createTask({
    required String title,
    String description = '',
    required String projectId,
    String? workerId,
    String? agentType,
    AgentExecutionConfigItem? agentConfig,
    String baseBranch = 'main',
    List<String> preCommands = const [],
    List<String> postCommands = const [],
    String? startDate,
    String? endDate,
  }) {
    final input = {
      'title': title,
      'description': description,
      'projectId': projectId,
      if ((workerId ?? '').isNotEmpty) 'workerId': workerId,
      if ((agentType ?? '').isNotEmpty) 'agentType': agentType,
      if ((agentType ?? '').isNotEmpty &&
          agentConfig != null &&
          !agentConfig.isEmpty)
        'agentConfig': agentConfig.toGraphQLInput(agentType!),
      'baseBranch': baseBranch,
      'preCommands': preCommands,
      'postCommands': postCommands,
      if ((startDate ?? '').isNotEmpty) 'startDate': startDate,
      if ((endDate ?? '').isNotEmpty) 'endDate': endDate,
    };
    return graphQL(
      r'''
        mutation CreateTask($input: CreateTaskInput!) {
          createTask(input: $input) { id }
        }
        ''',
      variables: {'input': input},
    ).then((_) {});
  }

  Future<void> updateTask(TaskItem task) => graphQL(
        r'''
        mutation UpdateTask($input: UpdateTaskInput!) {
          updateTask(input: $input) { id }
        }
        ''',
        variables: {
          'input': {
            'id': task.id,
            'title': task.title,
            'description': task.description,
            'projectId': task.projectId,
            'agentType': task.agentType.isEmpty ? null : task.agentType,
            'baseBranch': task.baseBranch,
            'preCommands': task.preCommands,
            'postCommands': task.postCommands,
            'startDate': task.startDate,
            'endDate': task.endDate,
          },
        },
      ).then((_) {});

  Future<void> assignWorker(
    String taskId,
    String workerId, {
    String? agentType,
    AgentExecutionConfigItem? agentConfig,
  }) {
    final selectedAgent = agentType ?? '';
    final input = {
      'taskId': taskId,
      'workerId': workerId,
      if (selectedAgent.isNotEmpty) 'agentType': selectedAgent,
      if (agentConfig != null)
        'agentConfig': agentConfig.toGraphQLInput(selectedAgent),
    };
    return graphQL(
      r'''
        mutation AssignWorker($input: AssignWorkerInput!) {
          assignWorker(input: $input) { id }
        }
        ''',
      variables: {'input': input},
    ).then((_) {});
  }

  Future<void> startTask(String taskId) => graphQL(
        r'''
        mutation StartTask($taskId: ID!) {
          startTask(taskId: $taskId) { id }
        }
        ''',
        variables: {'taskId': taskId},
      ).then((_) {});

  Future<void> interruptTask(String taskId) => graphQL(
        r'''
        mutation InterruptTask($taskId: ID!) {
          interruptTask(taskId: $taskId) { id }
        }
        ''',
        variables: {'taskId': taskId},
      ).then((_) {});

  Future<void> archiveTask(String taskId) => graphQL(
        r'''
        mutation ArchiveTask($taskId: ID!) {
          archiveTask(taskId: $taskId) { id }
        }
        ''',
        variables: {'taskId': taskId},
      ).then((_) {});

  Future<void> retryTask(String taskId) => graphQL(
        r'''
        mutation RetryTask($taskId: ID!) {
          retryTask(taskId: $taskId) { id }
        }
        ''',
        variables: {'taskId': taskId},
      ).then((_) {});

  Future<void> continueTask(String taskId, String message) => graphQL(
        r'''
        mutation ContinueTask($input: ContinueTaskInput!) {
          continueTask(input: $input) { id }
        }
        ''',
        variables: {
          'input': {'taskId': taskId, 'message': message},
        },
      ).then((_) {});

  Future<void> respondTaskInteraction({
    required String interactionId,
    String? decision,
    String message = '',
    String payload = '',
  }) {
    final input = {
      'interactionId': interactionId,
      if ((decision ?? '').isNotEmpty) 'decision': decision,
      if (message.isNotEmpty) 'message': message,
      if (payload.isNotEmpty) 'payload': payload,
    };
    return graphQL(
      r'''
        mutation RespondTaskInteraction($input: RespondTaskInteractionInput!) {
          respondTaskInteraction(input: $input) { id status }
        }
        ''',
      variables: {'input': input},
    ).then((_) {});
  }

  Future<void> createWorker({
    String id = '',
    required String name,
    required List<String> supportedAgents,
    required String workDir,
    String startupCommand = '',
    String projectBindingMode = 'ALL_PROJECTS',
    List<String> boundProjectIds = const [],
    List<WorkerAgentRuntimeEnvItem> agentRuntimeEnv = const [],
  }) =>
      graphQL(
        r'''
        mutation CreateWorker($input: CreateWorkerInput!) {
          createWorker(input: $input) { id }
        }
        ''',
        variables: {
          'input': {
            if (id.isNotEmpty) 'id': id,
            'name': name,
            'supportedAgents': supportedAgents,
            'workDir': workDir,
            'startupCommand': startupCommand,
            'projectBindingMode': projectBindingMode,
            'boundProjectIds': boundProjectIds,
            'agentRuntimeEnv': _agentRuntimeEnvInput(agentRuntimeEnv),
          },
        },
      ).then((_) {});

  Future<void> updateWorker(WorkerItem worker) => graphQL(
        r'''
        mutation UpdateWorker($input: UpdateWorkerInput!) {
          updateWorker(input: $input) { id }
        }
        ''',
        variables: {
          'input': {
            'id': worker.id,
            'name': worker.name,
            'supportedAgents': worker.supportedAgents,
            'workDir': worker.workDir,
            'startupCommand': worker.startupCommand,
            'projectBindingMode': worker.projectBindingMode,
            'boundProjectIds': worker.boundProjectIds,
            'agentRuntimeEnv': _agentRuntimeEnvInput(worker.agentRuntimeEnv),
          },
        },
      ).then((_) {});

  Future<void> enableWorker(String workerId) => graphQL(
        r'''
        mutation EnableWorker($id: ID!) {
          enableWorker(id: $id) { id }
        }
        ''',
        variables: {'id': workerId},
      ).then((_) {});

  Future<void> disableWorker(String workerId) => graphQL(
        r'''
        mutation DisableWorker($id: ID!) {
          disableWorker(id: $id) { id }
        }
        ''',
        variables: {'id': workerId},
      ).then((_) {});

  Future<void> deleteWorker(String workerId) => graphQL(
        r'''
        mutation DeleteWorker($id: ID!) {
          deleteWorker(id: $id)
        }
        ''',
        variables: {'id': workerId},
      ).then((_) {});

  Future<void> updateSettings(SettingsData settings) => graphQL(
        r'''
        mutation UpdateSettings($timeout: String!) {
          updateWorkerHeartbeatTimeout(timeout: $timeout) { id }
        }
        ''',
        variables: {'timeout': settings.workerHeartbeatTimeout},
      ).then((_) {});

  Future<Map<String, dynamic>> graphQL(
    String query, {
    Map<String, dynamic>? variables,
  }) async {
    final result = await _client.query(
      QueryOptions(
        document: gql(query),
        variables: variables ?? const {},
        fetchPolicy: FetchPolicy.networkOnly,
      ),
    );
    if (result.hasException) {
      throw StateError(result.exception.toString());
    }
    return result.data ?? <String, dynamic>{};
  }

  void dispose() {
    _webSocketLink.dispose();
  }

  static String _inferWebsocketEndpoint(String httpEndpoint) {
    final uri = Uri.parse(httpEndpoint);
    final scheme = uri.scheme == 'https' ? 'wss' : 'ws';
    final path = uri.path.endsWith('/graphql')
        ? uri.path.replaceFirst(RegExp(r'/graphql$'), '/subscriptions')
        : '/subscriptions';
    return uri.replace(scheme: scheme, path: path).toString();
  }

  static List<Map<String, dynamic>> _agentRuntimeEnvInput(
    List<WorkerAgentRuntimeEnvItem> groups,
  ) =>
      groups
          .map(
            (group) => {
              'agentType': group.agentType,
              'vars': group.vars
                  .map(
                    (item) => {
                      'key': item.key,
                      if (!item.sensitive || item.valueInput.isNotEmpty)
                        'value': item.valueInput,
                      'description': item.description,
                      'enabled': item.enabled,
                      'sensitive': item.sensitive,
                    },
                  )
                  .toList(),
            },
          )
          .toList();
}
