package a2aserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

// ParseExecutionRequest 严格解析 Worker execution v1 A2A Message。
// 参数：message 必须包含一个用户 TextPart 和一个 execution request DataPart，workerID 是目标 Worker。
// 返回：仍保留敏感环境变量原值、仅供当前 Runtime 使用的请求副本。
// 错误：Message 角色、extension、Part 数量、DataPart 或 execution 契约非法时返回错误。
func ParseExecutionRequest(message *a2a.Message, workerID string) (*a2aext.ExecutionRequest, error) {
	if message == nil || message.Role != a2a.MessageRoleUser {
		return nil, fmt.Errorf("execution Message 必须来自用户: %w", a2a.ErrInvalidParams)
	}
	if !slices.Contains(message.Extensions, a2aext.ExtensionURI) {
		return nil, fmt.Errorf("execution Message 缺少 required extension: %w", a2a.ErrExtensionSupportRequired)
	}
	if len(message.Parts) != 2 {
		return nil, fmt.Errorf("execution Message 必须恰好包含 TextPart 和 DataPart: %w", a2a.ErrInvalidParams)
	}
	var textCount, dataCount int
	var request *a2aext.ExecutionRequest
	for _, part := range message.Parts {
		if part == nil {
			return nil, fmt.Errorf("execution Message 包含空 Part: %w", a2a.ErrInvalidParams)
		}
		switch content := part.Content.(type) {
		case a2a.Text:
			textCount++
			if strings.TrimSpace(string(content)) == "" {
				return nil, fmt.Errorf("execution TextPart 不能为空: %w", a2a.ErrInvalidParams)
			}
		case a2a.Data:
			dataCount++
			raw, err := json.Marshal(content.Value)
			if err != nil {
				return nil, fmt.Errorf("编码 execution DataPart: %w", a2a.ErrInvalidParams)
			}
			decoded, err := a2aext.DecodeExecutionRequest(raw)
			if err != nil {
				return nil, fmt.Errorf("解码 execution DataPart: %w", a2a.ErrInvalidParams)
			}
			request = decoded
		default:
			return nil, fmt.Errorf("execution Message 不允许非 Text/Data Part: %w", a2a.ErrInvalidParams)
		}
	}
	if textCount != 1 || dataCount != 1 || request == nil {
		return nil, fmt.Errorf("execution Message Part 类型不唯一: %w", a2a.ErrInvalidParams)
	}
	if err := a2aext.ValidateRequest(request, workerID); err != nil {
		return nil, fmt.Errorf("execution request 非法: %w: %w", a2a.ErrInvalidParams, err)
	}
	if string(message.ID) != request.Command.ID {
		return nil, fmt.Errorf("A2A message.id 必须等于 command.id: %w", a2a.ErrInvalidParams)
	}
	if err := validateMessageOperation(message, request.Command.Operation); err != nil {
		return nil, err
	}
	return request, nil
}

func validateMessageOperation(message *a2a.Message, operation a2aext.Operation) error {
	switch operation {
	case a2aext.OperationStart, a2aext.OperationRetry:
		if message.TaskID != "" || message.ContextID != "" {
			return fmt.Errorf("%s 必须创建新的 A2A Task 和 Context: %w", operation, a2a.ErrInvalidParams)
		}
	case a2aext.OperationContinue:
		if message.TaskID != "" || message.ContextID == "" {
			return fmt.Errorf("CONTINUE 必须创建新 Task 并复用既有 Context: %w", a2a.ErrInvalidParams)
		}
	case a2aext.OperationInteractionResponse:
		if message.TaskID == "" || message.ContextID == "" {
			return fmt.Errorf("交互回复必须引用既有 Task 和 Context: %w", a2a.ErrInvalidParams)
		}
	default:
		return errors.Join(a2a.ErrInvalidParams, errors.New("不支持的 execution operation"))
	}
	return nil
}
