package a2aserver

import (
	"context"
	"errors"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestParseExecutionRequestRejectsMalformedMessageShapes(t *testing.T) {
	request := serverTestRequest(a2aext.OperationStart)
	valid := func() *a2a.Message { return serverTestMessage(serverTestRequest(a2aext.OperationStart)) }
	tests := []struct {
		name    string
		message *a2a.Message
		mutate  func(*a2a.Message)
	}{
		{name: "nil"},
		{name: "wrong role", message: valid(), mutate: func(message *a2a.Message) { message.Role = a2a.MessageRoleAgent }},
		{name: "missing extension", message: valid(), mutate: func(message *a2a.Message) { message.Extensions = nil }},
		{name: "wrong part count", message: valid(), mutate: func(message *a2a.Message) { message.Parts = message.Parts[:1] }},
		{name: "nil part", message: valid(), mutate: func(message *a2a.Message) { message.Parts[0] = nil }},
		{name: "blank text", message: valid(), mutate: func(message *a2a.Message) { message.Parts[0] = a2a.NewTextPart(" ") }},
		{name: "unsupported part", message: valid(), mutate: func(message *a2a.Message) { message.Parts[0] = a2a.NewRawPart([]byte("raw")) }},
		{name: "duplicate text", message: valid(), mutate: func(message *a2a.Message) { message.Parts[1] = a2a.NewTextPart("again") }},
		{name: "duplicate data", message: valid(), mutate: func(message *a2a.Message) { message.Parts[0] = a2a.NewDataPart(request) }},
		{name: "unencodable data", message: valid(), mutate: func(message *a2a.Message) {
			message.Parts[1] = a2a.NewDataPart(map[string]any{"bad": func() {}})
		}},
		{name: "invalid contract", message: valid(), mutate: func(message *a2a.Message) {
			bad := serverTestRequest(a2aext.OperationStart)
			bad.Task.Title = ""
			message.Parts[1] = a2a.NewDataPart(bad)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.mutate != nil {
				test.mutate(test.message)
			}
			if _, err := ParseExecutionRequest(test.message, "worker-1"); err == nil {
				t.Fatal("畸形 Message 未被拒绝")
			}
		})
	}
}

func TestValidateMessageOperationCoversInteractionAndUnknownBranches(t *testing.T) {
	request := serverTestRequest(a2aext.OperationInteractionResponse)
	request.Worktree.Mode = a2aext.WorktreeResume
	request.Resume = &a2aext.Resume{AgentSessionID: "session", WorktreePath: "/tmp/worktree"}
	request.Interaction = &a2aext.Interaction{ID: "interaction", Decision: a2aext.DecisionApprove}
	message := serverTestMessage(request)
	message.TaskID = "task"
	message.ContextID = "context"
	if _, err := ParseExecutionRequest(message, "worker-1"); err != nil {
		t.Fatalf("合法交互回复被拒绝: %v", err)
	}
	message.TaskID = ""
	if _, err := ParseExecutionRequest(message, "worker-1"); err == nil {
		t.Fatal("缺少 taskId 的交互回复未被拒绝")
	}
	if err := validateMessageOperation(&a2a.Message{}, a2aext.Operation("OTHER")); err == nil {
		t.Fatal("未知 operation 未被拒绝")
	}
}

func TestServerRejectsInvalidLifecycleConfiguration(t *testing.T) {
	var nilServer *Server
	if err := nilServer.Start(context.Background()); err == nil || nilServer.Close() != nil {
		t.Fatal("nil Server 生命周期结果错误")
	}
	if _, _, ok := nilServer.Address(); ok || nilServer.Capabilities() == nil {
		t.Fatal("nil Server 地址/能力结果错误")
	}

	defaults := New(Config{})
	if defaults.config.Host != "127.0.0.1" || defaults.config.Logger == nil || len(defaults.config.AgentTypes) != 2 {
		t.Fatalf("Server 默认配置错误: %+v", defaults.config)
	}
	if err := defaults.Start(nil); err == nil {
		t.Fatal("nil context 未被拒绝")
	}
	if err := defaults.Start(context.Background()); err == nil {
		t.Fatal("空 Worker ID 未被拒绝")
	}
	missingDependencies := New(Config{WorkerID: "worker"})
	if err := missingDependencies.Start(context.Background()); err == nil {
		t.Fatal("缺少 executor/store 未被拒绝")
	}

	started, executor, _ := startIntegrationServer(t, "")
	if err := started.Start(context.Background()); err == nil {
		t.Fatal("重复启动未被拒绝")
	}
	if err := (&Server{}).Close(); err != nil {
		t.Fatalf("未启动 Server Close 返回错误: %v", err)
	}
	if capabilities := (&Server{}).Capabilities(); len(capabilities) != 0 {
		t.Fatalf("未启动 Server 能力不为空: %+v", capabilities)
	}

	base := started.config
	base.AgentExecutor = executor
	base.TaskStore = executor.store
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"invalid agent", func(config *Config) { config.AgentTypes = []a2aext.AgentType{"other"} }},
		{"duplicate agent", func(config *Config) { config.AgentTypes = []a2aext.AgentType{a2aext.AgentCodex, a2aext.AgentCodex} }},
		{"non loopback", func(config *Config) { config.Host = "0.0.0.0" }},
		{"invalid port", func(config *Config) { config.Port = 65536 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := base
			config.Port = 0
			test.mutate(&config)
			server := New(config)
			if err := server.Start(context.Background()); err == nil {
				_ = server.Close()
				t.Fatal("非法 Server 配置未被拒绝")
			}
		})
	}
}

func TestIdempotentHandlerRejectsNilRequestAndPreservesNilCause(t *testing.T) {
	handler := &idempotentHandler{}
	if _, _, err := handler.prepare(context.Background(), nil); !errors.Is(err, a2a.ErrInvalidParams) {
		t.Fatalf("nil request 错误 = %v", err)
	}
	if handler.abandonUnbound(context.Background(), nil, nil) != nil {
		t.Fatal("nil execution/cause 应保持 nil")
	}
}

func TestBearerInterceptorRejectsMissingIdentityAndWrongTokenShapes(t *testing.T) {
	interceptor := &bearerInterceptor{token: "secret", principal: "manager@worker"}
	if _, _, err := interceptor.Before(context.Background(), nil, nil); !errors.Is(err, a2a.ErrUnauthenticated) {
		t.Fatalf("nil CallContext 错误=%v", err)
	}
	_, emptyPrincipalCtx := a2asrv.NewCallContext(context.Background(), a2asrv.NewServiceParams(nil))
	if _, _, err := (&bearerInterceptor{}).Before(context.Background(), emptyPrincipalCtx, nil); !errors.Is(err, a2a.ErrUnauthenticated) {
		t.Fatalf("空 principal 错误=%v", err)
	}
	for _, authorization := range []string{"short", "Bearer xxxxxx"} {
		_, callCtx := a2asrv.NewCallContext(context.Background(), a2asrv.NewServiceParams(map[string][]string{"authorization": {authorization}}))
		if _, _, err := interceptor.Before(context.Background(), callCtx, nil); !errors.Is(err, a2a.ErrUnauthenticated) {
			t.Fatalf("错误 token %q 错误=%v", authorization, err)
		}
	}
}
