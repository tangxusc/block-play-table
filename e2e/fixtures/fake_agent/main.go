// Package main 提供端到端测试使用的确定性 Codex 与 Claude CLI 进程。
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	approvalMarker  = "BPT_REQUIRE_APPROVAL"
	holdMarker      = "BPT_HOLD"
	fileMarker      = "BPT_CLAUDE_FILE_APPROVAL"
	planMarker      = "BPT_CLAUDE_PLAN_B64="
	resultMarker    = "BPT_RESULT_B64="
	writeMarker     = "BPT_WRITE_FILE_B64="
	expectEnvMarker = "BPT_EXPECT_ENV_B64="
)

func main() {
	name := strings.ToLower(filepath.Base(os.Args[0]))
	if hasArgument("--version") {
		fmt.Println("block-play-table fake agent 1.0")
		return
	}
	if strings.Contains(name, "claude") {
		runClaude()
		return
	}
	runCodex()
}

func runCodex() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	sessionID := fmt.Sprintf("fake-codex-%d", os.Getpid())
	turnID := fmt.Sprintf("fake-turn-%d", os.Getpid())
	result := "fake codex completed"
	waitingApproval := false
	resumed := false
	for scanner.Scan() {
		var message map[string]any
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			continue
		}
		method, _ := message["method"].(string)
		id := message["id"]
		switch method {
		case "initialize":
			writeJSON(encoder, map[string]any{"id": id, "result": map[string]any{
				"userAgent": "block-play-table-fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "test",
			}})
		case "thread/start":
			writeJSON(encoder, map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": sessionID}}})
		case "thread/resume":
			resumed = true
			if resumed := findString(message["params"], "threadId", "id"); resumed != "" {
				sessionID = resumed
			}
			writeJSON(encoder, map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": sessionID}}})
		case "turn/start":
			raw := string(scanner.Bytes())
			fallback := "fake codex completed"
			if resumed {
				fallback = "fake codex continued"
			}
			result = markedResult(raw, fallback)
			if mismatch := markedEnvironmentMismatch(raw); mismatch != "" {
				result = mismatch
			}
			applyMarkedWrite(raw)
			writeJSON(encoder, map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": turnID}}})
			fmt.Fprintln(os.Stderr, "fake codex turn started")
			if strings.Contains(raw, holdMarker) {
				continue
			}
			if strings.Contains(raw, approvalMarker) {
				waitingApproval = true
				writeJSON(encoder, map[string]any{
					"id": 9001, "method": "item/commandExecution/requestApproval",
					"params": map[string]any{
						"threadId": sessionID, "turnId": turnID, "itemId": "fake-command-approval",
						"reason": "端到端测试审批", "command": "printf approved", "cwd": ".",
					},
				})
				continue
			}
			emitCodexCompletion(encoder, sessionID, turnID, result)
			return
		default:
			if waitingApproval && numericID(id) == 9001 {
				waitingApproval = false
				emitCodexCompletion(encoder, sessionID, turnID, result)
				return
			}
		}
	}
}

func emitCodexCompletion(encoder *json.Encoder, sessionID, turnID, result string) {
	writeJSON(encoder, map[string]any{
		"method": "item/completed",
		"params": map[string]any{
			"threadId": sessionID, "turnId": turnID,
			"item": map[string]any{"type": "agentMessage", "id": "fake-agent-message", "text": result},
		},
	})
	writeJSON(encoder, map[string]any{
		"method": "turn/completed",
		"params": map[string]any{
			"threadId": sessionID, "turn": map[string]any{"id": turnID, "status": "completed"},
		},
	})
}

func runClaude() {
	args := os.Args[1:]
	prompt := ""
	if len(args) > 0 {
		prompt = args[len(args)-1]
	}
	sessionID := argumentValue("--resume")
	if sessionID == "" {
		sessionID = argumentValue("--session-id")
	}
	if sessionID == "" {
		sessionID = fmt.Sprintf("fake-claude-%d", os.Getpid())
	}
	result := markedResult(prompt, "fake claude completed")
	applyMarkedWrite(prompt)
	encoder := json.NewEncoder(os.Stdout)
	writeJSON(encoder, map[string]any{"type": "system", "subtype": "init", "session_id": sessionID, "tools": []string{"Bash", "Write"}})
	if strings.Contains(prompt, approvalMarker) && argumentValue("--resume") == "" {
		toolName := "Bash"
		toolInput := map[string]any{"command": "printf approved"}
		if strings.Contains(prompt, fileMarker) {
			toolName = "Edit"
			toolInput = map[string]any{
				"file_path": "README.md", "old_string": "old", "new_string": "new",
			}
		}
		if encodedPlan := markerValue(prompt, planMarker); encodedPlan != "" {
			if plan, err := base64.StdEncoding.DecodeString(encodedPlan); err == nil {
				toolName = "ExitPlanMode"
				toolInput = map[string]any{"plan": string(plan), "planFilePath": ".claude/plans/e2e-plan.md"}
			}
		}
		writeJSON(encoder, map[string]any{
			"type": "result", "session_id": sessionID, "result": "等待端到端测试审批",
			"permission_denials": []map[string]any{{
				"tool_name": toolName, "tool_use_id": "fake-claude-permission", "tool_input": toolInput,
			}},
		})
		return
	}
	writeJSON(encoder, map[string]any{
		"type": "assistant", "session_id": sessionID,
		"message": map[string]any{"content": []map[string]any{{"type": "text", "text": result}}},
	})
	writeJSON(encoder, map[string]any{"type": "result", "session_id": sessionID, "result": result, "permission_denials": []any{}})
}

func hasArgument(want string) bool {
	for _, value := range os.Args[1:] {
		if value == want {
			return true
		}
	}
	return false
}

func argumentValue(name string) string {
	for index, value := range os.Args[1:] {
		if value == name && index+2 <= len(os.Args[1:]) {
			return os.Args[index+2]
		}
	}
	return ""
}

func findString(value any, keys ...string) string {
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}
	var walk func(any) string
	walk = func(current any) string {
		switch item := current.(type) {
		case map[string]any:
			for key, nested := range item {
				if _, ok := keySet[key]; ok {
					if text, ok := nested.(string); ok && text != "" {
						return text
					}
				}
			}
			for _, nested := range item {
				if found := walk(nested); found != "" {
					return found
				}
			}
		case []any:
			for _, nested := range item {
				if found := walk(nested); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return walk(value)
}

func markedResult(input, fallback string) string {
	encoded := markerValue(input, resultMarker)
	if encoded == "" {
		return fallback
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return fallback
	}
	return string(decoded)
}

func applyMarkedWrite(input string) {
	value := markerValue(input, writeMarker)
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return
	}
	path, pathErr := base64.StdEncoding.DecodeString(parts[0])
	content, contentErr := base64.StdEncoding.DecodeString(parts[1])
	if pathErr != nil || contentErr != nil || len(path) == 0 || filepath.IsAbs(string(path)) {
		return
	}
	clean := filepath.Clean(string(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return
	}
	if directory := filepath.Dir(clean); directory != "." {
		_ = os.MkdirAll(directory, 0o755)
	}
	_ = os.WriteFile(clean, content, 0o644)
}

func markedEnvironmentMismatch(input string) string {
	encoded := markerValue(input, expectEnvMarker)
	if encoded == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "fake agent environment marker is invalid"
	}
	expected := map[string]string{}
	if err := json.Unmarshal(decoded, &expected); err != nil {
		return "fake agent environment marker is invalid"
	}
	keys := make([]string, 0, len(expected))
	for key := range expected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if actual := os.Getenv(key); actual != expected[key] {
			return fmt.Sprintf("fake agent environment mismatch for %s: %q", key, actual)
		}
	}
	return ""
}

func markerValue(input, marker string) string {
	start := strings.Index(input, marker)
	if start < 0 {
		return ""
	}
	value := input[start+len(marker):]
	end := len(value)
	for index, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '+' && character != '/' && character != '=' && character != ':' {
			end = index
			break
		}
	}
	return value[:end]
}

func numericID(value any) int {
	switch id := value.(type) {
	case float64:
		return int(id)
	case int:
		return id
	case json.Number:
		parsed, _ := id.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func writeJSON(encoder *json.Encoder, value any) {
	if err := encoder.Encode(value); err != nil {
		os.Exit(1)
	}
}
