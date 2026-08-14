package a2aserver

import (
	"bytes"
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	sdka2asrv "github.com/a2aproject/a2a-go/v2/a2asrv"
)

// FuzzJSONRPCSSETransport 使用官方 SDK handler 验证畸形 JSON-RPC/SSE 请求的有界响应。
func FuzzJSONRPCSSETransport(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":"seed","method":"tasks/get","params":{"id":"missing"}}`), false)
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"message/stream","params":{}}`), true)
	f.Add([]byte(`{"jsonrpc":`), false)
	f.Add(bytes.Repeat([]byte{'{'}, 64*1024), false)
	handler := sdka2asrv.NewJSONRPCHandler(fuzzA2ARequestHandler{})
	f.Fuzz(func(t *testing.T, raw []byte, acceptSSE bool) {
		if len(raw) > 64*1024 {
			t.Skip()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		request := httptest.NewRequest(http.MethodPost, "http://worker.local/a2a", bytes.NewReader(raw)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		if acceptSSE {
			request.Header.Set("Accept", "text/event-stream")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code < 100 || response.Code > 599 {
			t.Fatalf("非法 HTTP 状态码：%d", response.Code)
		}
		if response.Body.Len() > 4*1024*1024 {
			t.Fatalf("小请求产生过大响应：%d", response.Body.Len())
		}
	})
}

type fuzzA2ARequestHandler struct{}

func (fuzzA2ARequestHandler) GetTask(context.Context, *a2a.GetTaskRequest) (*a2a.Task, error) {
	return nil, a2a.ErrTaskNotFound
}

func (fuzzA2ARequestHandler) ListTasks(context.Context, *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return &a2a.ListTasksResponse{}, nil
}

func (fuzzA2ARequestHandler) CancelTask(context.Context, *a2a.CancelTaskRequest) (*a2a.Task, error) {
	return nil, a2a.ErrTaskNotFound
}

func (fuzzA2ARequestHandler) SendMessage(context.Context, *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	return nil, a2a.ErrInvalidParams
}

func (fuzzA2ARequestHandler) SubscribeToTask(context.Context, *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	return fuzzErrorSequence(a2a.ErrTaskNotFound)
}

func (fuzzA2ARequestHandler) SendStreamingMessage(context.Context, *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return fuzzErrorSequence(a2a.ErrInvalidParams)
}

func (fuzzA2ARequestHandler) GetTaskPushConfig(context.Context, *a2a.GetTaskPushConfigRequest) (*a2a.PushConfig, error) {
	return nil, a2a.ErrPushNotificationNotSupported
}

func (fuzzA2ARequestHandler) ListTaskPushConfigs(context.Context, *a2a.ListTaskPushConfigRequest) (*a2a.ListTaskPushConfigResponse, error) {
	return nil, a2a.ErrPushNotificationNotSupported
}

func (fuzzA2ARequestHandler) CreateTaskPushConfig(context.Context, *a2a.PushConfig) (*a2a.PushConfig, error) {
	return nil, a2a.ErrPushNotificationNotSupported
}

func (fuzzA2ARequestHandler) DeleteTaskPushConfig(context.Context, *a2a.DeleteTaskPushConfigRequest) error {
	return a2a.ErrPushNotificationNotSupported
}

func (fuzzA2ARequestHandler) GetExtendedAgentCard(context.Context, *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	return nil, a2a.ErrMethodNotFound
}

func fuzzErrorSequence(err error) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(nil, err)
	}
}
