package a2aadapter

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func mergeEnvironment(extra map[string]string) []string {
	result := os.Environ()
	for key, value := range extra {
		result = append(result, key+"="+value)
	}
	return result
}

type agentEventScanner struct {
	scanner *bufio.Scanner
	err     error
}

func newAgentEventScanner(reader io.Reader) *agentEventScanner {
	scanner := bufio.NewScanner(reader)
	// Scanner 还需容纳分隔符；额外空间仅用于判定边界，协议仍严格限制 16 MiB。
	scanner.Buffer(make([]byte, 64*1024), a2aext.MaxAgentEventBytes+64*1024)
	return &agentEventScanner{scanner: scanner}
}

// Scan 推进到下一条不超过协议上限的 CLI 事件。
// 参数：无。
// 返回：成功读取下一条事件时返回 true，输入结束或发生错误时返回 false。
// 错误：超限错误记录在扫描器中，由 Err 返回。
func (s *agentEventScanner) Scan() bool {
	if !s.scanner.Scan() {
		return false
	}
	if len(s.scanner.Bytes()) > a2aext.MaxAgentEventBytes {
		s.err = fmt.Errorf("%w: 原始 CLI 事件为 %d 字节", ErrEventTooLarge, len(s.scanner.Bytes()))
		return false
	}
	return true
}

// Text 返回最近一次成功扫描的 CLI 事件文本。
// 参数：无。
// 返回：当前事件的原始文本。
// 错误：本方法不返回错误；调用方应在 Scan 返回 false 后调用 Err。
func (s *agentEventScanner) Text() string {
	return s.scanner.Text()
}

// Err 返回扫描过程中记录的协议上限或底层读取错误。
// 参数：无。
// 返回：扫描成功或正常结束时返回 nil，否则返回规范化错误。
// 错误：本方法仅返回已发生的扫描错误，不产生新错误。
func (s *agentEventScanner) Err() error {
	if s.err != nil {
		return s.err
	}
	return normalizeAgentScannerError(s.scanner.Err())
}

func normalizeAgentScannerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, bufio.ErrTooLong) || strings.Contains(strings.ToLower(err.Error()), "token too long") {
		return fmt.Errorf("%w: %v", ErrEventTooLarge, err)
	}
	return err
}
