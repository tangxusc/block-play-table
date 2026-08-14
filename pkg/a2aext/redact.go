package a2aext

import (
	"sort"
	"strings"
	"sync"
)

// Redactor 对普通文本和跨分块日志执行敏感值脱敏。
type Redactor struct {
	mu      sync.Mutex
	secrets []string
	pending string
}

// NewRedactor 创建可跨日志分块识别敏感值的脱敏器。
// 参数：secrets 为仅保存在内存中的敏感原值列表。
// 返回：可并发安全使用的脱敏器。
// 错误：本函数不返回错误；空值会被忽略。
func NewRedactor(secrets []string) *Redactor {
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret == "" || secret == SensitivePlaceholder {
			continue
		}
		if _, ok := seen[secret]; ok {
			continue
		}
		seen[secret] = struct{}{}
		clean = append(clean, secret)
	}
	sortSecrets(clean)
	return &Redactor{secrets: clean}
}

// Redact 返回一次性脱敏后的文本。
// 参数：value 为待脱敏文本。
// 返回：所有已知敏感值替换为固定占位符后的文本。
// 错误：本函数不返回错误。
func (r *Redactor) Redact(value string) string {
	if r == nil || len(r.secrets) == 0 {
		return value
	}
	replacements := make([]string, 0, len(r.secrets)*2)
	for _, secret := range r.secrets {
		replacements = append(replacements, secret, SensitivePlaceholder)
	}
	return strings.NewReplacer(replacements...).Replace(value)
}

// RedactChunk 对连续日志分块脱敏并保留可能跨块的敏感前缀。
// 参数：chunk 为当前原始分块，final 表示此后不会再有分块。
// 返回：当前可安全输出的脱敏文本；非 final 调用可能暂存尾部。
// 错误：本函数不返回错误。
func (r *Redactor) RedactChunk(chunk string, final bool) string {
	if r == nil || len(r.secrets) == 0 {
		return chunk
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending += chunk
	var out strings.Builder
	for r.pending != "" {
		waitForLonger := false
		for _, secret := range r.secrets {
			if len(r.pending) < len(secret) && strings.HasPrefix(secret, r.pending) {
				waitForLonger = true
				break
			}
		}
		if waitForLonger && !final {
			break
		}
		matched := ""
		for _, secret := range r.secrets {
			if strings.HasPrefix(r.pending, secret) {
				matched = secret
				break
			}
		}
		if matched != "" {
			out.WriteString(SensitivePlaceholder)
			r.pending = r.pending[len(matched):]
			continue
		}
		out.WriteByte(r.pending[0])
		r.pending = r.pending[1:]
	}
	return out.String()
}

func sortSecrets(values []string) {
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) == len(values[j]) {
			return values[i] < values[j]
		}
		return len(values[i]) > len(values[j])
	})
}
