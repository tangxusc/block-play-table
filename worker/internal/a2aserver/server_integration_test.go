package a2aserver

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
	"github.com/tangxusc/block-play-table/worker/internal/a2astore"
)

func TestServerPublishesCardAndRunsAuthenticatedJSONRPCStream(t *testing.T) {
	server, executor, baseURL := startIntegrationServer(t, "worker-token")
	card, err := agentcard.NewResolver(http.DefaultClient).Resolve(t.Context(), baseURL)
	if err != nil {
		t.Fatalf("读取 Agent Card: %v", err)
	}
	if len(card.Skills) != 2 || card.Skills[0].ID != "block-play-table-codex" || card.Skills[1].ID != "block-play-table-claude" {
		t.Fatalf("Agent Card skills = %+v", card.Skills)
	}
	if !card.Capabilities.Streaming || len(card.Capabilities.Extensions) != 1 || card.Capabilities.Extensions[0].URI != a2aext.ExtensionURI || !card.Capabilities.Extensions[0].Required {
		t.Fatalf("Agent Card capabilities = %+v", card.Capabilities)
	}
	if len(card.SecuritySchemes) != 1 || len(card.SecurityRequirements) != 1 {
		t.Fatalf("Agent Card security = schemes:%+v requirements:%+v", card.SecuritySchemes, card.SecurityRequirements)
	}

	httpClient := &http.Client{Transport: bearerRoundTripper{token: "worker-token", extension: true}}
	client, err := a2aclient.NewFromCard(t.Context(), card, a2aclient.WithJSONRPCTransport(httpClient))
	if err != nil {
		t.Fatalf("创建官方 A2A Client: %v", err)
	}
	request := integrationExecutionRequest(a2aext.OperationStart)
	message := integrationExecutionMessage(request)
	var states []a2a.TaskState
	for event, streamErr := range client.SendStreamingMessage(t.Context(), &a2a.SendMessageRequest{Message: message}) {
		if streamErr != nil {
			t.Fatalf("SendStreamingMessage: %v", streamErr)
		}
		switch value := event.(type) {
		case *a2a.Task:
			states = append(states, value.Status.State)
		case *a2a.TaskStatusUpdateEvent:
			states = append(states, value.Status.State)
		}
	}
	if !containsTaskState(states, a2a.TaskStateSubmitted) || !containsTaskState(states, a2a.TaskStateWorking) || !containsTaskState(states, a2a.TaskStateCompleted) {
		t.Fatalf("SSE states = %v", states)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("AgentExecutor calls = %d, want 1", executor.calls.Load())
	}
	if executor.principal.Load() != "manager@worker-integration" {
		t.Fatalf("AgentExecutor principal = %q", executor.principal.Load())
	}

	result, err := client.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: integrationExecutionMessage(request)})
	if err != nil {
		t.Fatalf("重复 command 应返回幂等快照: %v", err)
	}
	if task, ok := result.(*a2a.Task); !ok || task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("重复 command result = %#v", result)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("重复 command 启动了第二个执行，calls = %d", executor.calls.Load())
	}
	if KeepAliveInterval != 15*time.Second {
		t.Fatalf("SSE keepalive = %s", KeepAliveInterval)
	}
	if capabilities := server.Capabilities(); capabilities["a2a_endpoint_path"] != EndpointPath || capabilities["a2a_extension"] != a2aext.ExtensionURI {
		t.Fatalf("Server capabilities = %+v", capabilities)
	}
}

func TestServerRejectsMissingBearerAndRequiredExtension(t *testing.T) {
	_, executor, baseURL := startIntegrationServer(t, "worker-token")
	card, err := agentcard.NewResolver(http.DefaultClient).Resolve(t.Context(), baseURL)
	if err != nil {
		t.Fatal(err)
	}
	unauthenticated, err := a2aclient.NewFromCard(t.Context(), card, a2aclient.WithJSONRPCTransport(http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	_, err = unauthenticated.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: integrationExecutionMessage(integrationExecutionRequest(a2aext.OperationStart))})
	if !errors.Is(err, a2a.ErrUnauthenticated) {
		t.Fatalf("未携带 Bearer 的错误 = %v", err)
	}

	authenticated, err := a2aclient.NewFromCard(t.Context(), card, a2aclient.WithJSONRPCTransport(&http.Client{Transport: bearerRoundTripper{token: "worker-token"}}))
	if err != nil {
		t.Fatal(err)
	}
	message := integrationExecutionMessage(integrationExecutionRequest(a2aext.OperationStart))
	message.Extensions = nil
	_, err = authenticated.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: message})
	if !errors.Is(err, a2a.ErrExtensionSupportRequired) {
		t.Fatalf("缺少 required extension 的错误 = %v", err)
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("非法请求不应启动 AgentExecutor，calls = %d", executor.calls.Load())
	}
}

func TestServerTrustedLocalModeStillSetsAuthenticatedPrincipal(t *testing.T) {
	server, executor, baseURL := startIntegrationServer(t, "")
	card, err := agentcard.NewResolver(http.DefaultClient).Resolve(t.Context(), baseURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(card.SecuritySchemes) != 0 || len(card.SecurityRequirements) != 0 {
		t.Fatalf("trusted-local Agent Card 不应声明 Bearer: %+v", card.SecuritySchemes)
	}
	client, err := a2aclient.NewFromCard(t.Context(), card, a2aclient.WithJSONRPCTransport(&http.Client{Transport: bearerRoundTripper{extension: true}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: integrationExecutionMessage(integrationExecutionRequest(a2aext.OperationStart))}); err != nil {
		t.Fatalf("trusted-local A2A 请求: %v", err)
	}
	if executor.principal.Load() != "manager@worker-integration" {
		t.Fatalf("trusted-local principal = %q", executor.principal.Load())
	}
	if _, _, started := server.Address(); !started {
		t.Fatal("Server Address 未报告已启动")
	}
}

func TestValidateLoopbackHostRejectsExternallyReachableAddresses(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "localhost"} {
		if err := validateLoopbackHost(host); err != nil {
			t.Fatalf("loopback %q 被拒绝: %v", host, err)
		}
	}
	for _, host := range []string{"", "0.0.0.0", "192.0.2.1", "worker.example"} {
		if err := validateLoopbackHost(host); err == nil {
			t.Fatalf("非 loopback %q 未被拒绝", host)
		}
	}
}

type integrationExecutor struct {
	store     *a2astore.Store
	workerID  string
	calls     atomic.Int32
	principal atomic.Value
}

func (e *integrationExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		e.calls.Add(1)
		if execCtx.User != nil {
			e.principal.Store(execCtx.User.Name)
		}
		request, err := ParseExecutionRequest(execCtx.Message, e.workerID)
		if err != nil {
			yield(nil, err)
			return
		}
		if _, err := e.store.BindCommand(ctx, request, string(execCtx.TaskID), execCtx.ContextID); err != nil {
			yield(nil, err)
			return
		}
		if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
			return
		}
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil)
	}
}

func (e *integrationExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

type bearerRoundTripper struct {
	token     string
	extension bool
}

func (t bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	if t.token != "" {
		cloned.Header.Set("Authorization", "Bearer "+t.token)
	}
	if t.extension {
		cloned.Header.Set(a2a.SvcParamExtensions, a2aext.ExtensionURI)
	}
	return http.DefaultTransport.RoundTrip(cloned)
}

func startIntegrationServer(t *testing.T, token string) (*Server, *integrationExecutor, string) {
	t.Helper()
	store, err := a2astore.Open(t.Context(), a2astore.Config{
		Path: filepath.Join(t.TempDir(), "a2a.db"), Authenticator: a2asrv.NewTaskStoreAuthenticator(),
	})
	if err != nil {
		t.Fatalf("打开 A2A Store: %v", err)
	}
	executor := &integrationExecutor{store: store, workerID: "worker-integration"}
	server := New(Config{
		Host: "127.0.0.1", Port: 0, WorkerID: executor.workerID, WorkerToken: token,
		AgentTypes: []a2aext.AgentType{a2aext.AgentCodex, a2aext.AgentClaude}, AgentExecutor: executor, TaskStore: store,
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := server.Start(ctx); err != nil {
		cancel()
		_ = store.Close()
		t.Fatalf("启动 A2A Server: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		_ = store.Close()
	})
	host, port, ok := server.Address()
	if !ok {
		t.Fatal("A2A Server 未返回监听地址")
	}
	return server, executor, "http://" + host + ":" + strconv.Itoa(port)
}

func integrationExecutionMessage(request *a2aext.ExecutionRequest) *a2a.Message {
	message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("执行集成测试"), a2a.NewDataPart(request))
	message.ID = request.Command.ID
	message.Extensions = []string{a2aext.ExtensionURI}
	return message
}

func integrationExecutionRequest(operation a2aext.Operation) *a2aext.ExecutionRequest {
	request := &a2aext.ExecutionRequest{
		Kind: a2aext.RequestKind, Version: a2aext.Version,
		Command:     a2aext.Command{ID: "command-integration", Operation: operation, IssuedAt: time.Now().UTC()},
		Scope:       a2aext.RequestScope{LocalTaskID: "task-integration", ExecutionID: "execution-integration", Attempt: 1, Turn: 1, ExpectedWorkerID: "worker-integration"},
		Task:        a2aext.TaskSpec{Title: "集成测试", Description: "验证 A2A HTTP/SSE", BaseBranch: "main"},
		Agent:       a2aext.AgentSpec{Type: a2aext.AgentCodex, WorkMode: a2aext.WorkModeImplement},
		Project:     a2aext.ProjectSpec{ID: "project-integration", GitURL: "file:///tmp/repository", DefaultBranch: "main", WorktreeNamePrefix: "integration"},
		Worktree:    a2aext.WorktreeSpec{Mode: a2aext.WorktreeCreate},
		Commands:    a2aext.Commands{Pre: []string{}, Post: []string{}},
		Environment: a2aext.Environment{Variables: []a2aext.EnvironmentVariable{}},
	}
	return request
}

func containsTaskState(states []a2a.TaskState, expected a2a.TaskState) bool {
	for _, state := range states {
		if state == expected {
			return true
		}
	}
	return false
}
