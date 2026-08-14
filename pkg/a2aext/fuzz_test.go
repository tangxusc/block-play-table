package a2aext

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// FuzzExecutionRequestDataPart 验证任意 execution request JSON 的校验、脱敏和规范 hash 不会分歧。
func FuzzExecutionRequestDataPart(f *testing.F) {
	request := validRequest()
	request.Command.IssuedAt = time.Unix(1, 0).UTC()
	request.Environment.Variables = []EnvironmentVariable{{Key: "TOKEN", Value: "fixed-secret", Sensitive: true}}
	seed, err := json.Marshal(request)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{"kind":"bpt.execution.request","version":"1.0"}`))
	f.Add([]byte(`{"kind":`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxAgentEventBytes+1024 {
			t.Skip()
		}
		var decoded ExecutionRequest
		if json.Unmarshal(raw, &decoded) != nil || ValidateRequest(&decoded, "") != nil {
			return
		}
		sanitized, err := SanitizeRequest(&decoded)
		if err != nil {
			t.Fatalf("合法请求无法脱敏：%v", err)
		}
		hashA, err := CanonicalPayloadHash(&decoded)
		if err != nil {
			t.Fatalf("合法请求无法计算 hash：%v", err)
		}
		hashB, err := CanonicalPayloadHash(&decoded)
		if err != nil || hashA != hashB {
			t.Fatalf("规范 hash 不稳定：%q %q, err=%v", hashA, hashB, err)
		}
		sanitizedRaw, err := json.Marshal(sanitized)
		if err != nil {
			t.Fatalf("脱敏请求无法编码：%v", err)
		}
		for _, variable := range decoded.Environment.Variables {
			if variable.Sensitive && variable.Value != "" && variable.Value != SensitivePlaceholder && bytes.Contains(sanitizedRaw, []byte(variable.Value)) {
				t.Fatalf("脱敏副本仍包含敏感值")
			}
		}
	})
}

// FuzzExecutionEventDataPart 验证任意 execution event JSON 的解析和往返校验保持一致。
func FuzzExecutionEventDataPart(f *testing.F) {
	seed, err := json.Marshal(validExecutionEvent(EventLogChunk, map[string]any{"stream": string(LogStdout), "content": "seed"}))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{"kind":"bpt.execution.event","event":{"sequence":-1}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxAgentEventBytes+1024 {
			t.Skip()
		}
		var event ExecutionEvent
		if json.Unmarshal(raw, &event) != nil || ValidateEvent(&event) != nil {
			return
		}
		roundTrip, err := json.Marshal(&event)
		if err != nil {
			t.Fatalf("合法事件无法编码：%v", err)
		}
		var decoded ExecutionEvent
		if err := json.Unmarshal(roundTrip, &decoded); err != nil {
			t.Fatalf("合法事件无法往返解码：%v", err)
		}
		if err := ValidateEvent(&decoded); err != nil {
			t.Fatalf("合法事件往返后失效：%v", err)
		}
	})
}

// FuzzArtifactMetadataJSON 验证任意 Artifact metadata JSON 不会绕过 execution v1 约束。
func FuzzArtifactMetadataJSON(f *testing.F) {
	metadata := ArtifactMetadata{
		Kind: ArtifactKind, Version: Version, Role: ArtifactLog,
		ExecutionID: "execution", Attempt: 1, Turn: 1,
		EventID: "018f0000-0000-7000-8000-000000000001", Sequence: 1,
		Stream: LogStdout, ChunkIndex: 0, FinalChunk: true, CreatedAt: time.Unix(1, 0).UTC(),
	}
	seed, err := json.Marshal(metadata)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{"kind":"bpt.execution.artifact","role":"unknown"}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxAgentEventBytes+1024 {
			t.Skip()
		}
		var decoded ArtifactMetadata
		if json.Unmarshal(raw, &decoded) != nil || ValidateArtifact(&decoded) != nil {
			return
		}
		roundTrip, err := json.Marshal(decoded)
		if err != nil {
			t.Fatalf("合法 Artifact metadata 无法编码：%v", err)
		}
		var copy ArtifactMetadata
		if err := json.Unmarshal(roundTrip, &copy); err != nil || ValidateArtifact(&copy) != nil {
			t.Fatalf("合法 Artifact metadata 往返后失效：decode=%v validate=%v", err, ValidateArtifact(&copy))
		}
	})
}
