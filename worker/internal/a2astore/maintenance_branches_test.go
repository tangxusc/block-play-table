package a2astore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tangxusc/block-play-table/pkg/a2aext"
)

func TestCompactBeforeRejectsZeroCutoff(t *testing.T) {
	storage := newTestStore(t)
	if _, err := storage.CompactBefore(context.Background(), time.Time{}); err == nil {
		t.Fatal("零截止时间未失败")
	}
}

func TestTaskRetentionTurnHandlesEmptyMultipleAndArtifactOnlyTasks(t *testing.T) {
	if _, ok, err := taskRetentionTurn(nil); err != nil || ok {
		t.Fatalf("nil task = ok=%v err=%v", ok, err)
	}
	if _, ok, err := taskRetentionTurn(&a2a.Task{}); err != nil || ok {
		t.Fatalf("empty task = ok=%v err=%v", ok, err)
	}
	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	artifact := testArtifact(t, "manifest", a2aext.ArtifactManifest, 1, now, false)
	turn, ok, err := taskRetentionTurn(&a2a.Task{Artifacts: []*a2a.Artifact{nil, {Metadata: nil}, artifact}})
	if err != nil || !ok || turn.executionID != "execution" || turn.turn != 1 {
		t.Fatalf("artifact-only turn = %+v, ok=%v err=%v", turn, ok, err)
	}
	other := testArtifact(t, "other", a2aext.ArtifactResult, 2, now, true)
	other.Metadata[a2aext.ExtensionURI].(map[string]any)["turn"] = float64(2)
	if _, _, err := taskRetentionTurn(&a2a.Task{Artifacts: []*a2a.Artifact{artifact, other}}); !errors.Is(err, ErrProtocolConflict) {
		t.Fatalf("多个 turn 错误 = %v", err)
	}
}

func TestCompactTaskSkipsUnknownArtifactsAndKeepsLatestStableRole(t *testing.T) {
	now := time.Date(2026, 8, 14, 22, 30, 0, 0, time.UTC)
	oldManifest := testArtifact(t, "old-manifest", a2aext.ArtifactManifest, 1, now, false)
	newManifest := testArtifact(t, "new-manifest", a2aext.ArtifactManifest, 2, now, true)
	equalManifest := testArtifact(t, "equal-manifest", a2aext.ArtifactManifest, 2, now, false)
	result := testArtifact(t, "result", a2aext.ArtifactResult, 3, now, false)
	logArtifact := testArtifact(t, "log", a2aext.ArtifactLog, 4, now, false)
	task := &a2a.Task{
		History:  []*a2a.Message{a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("message"))},
		Metadata: map[string]any{"temporary": true},
		Artifacts: []*a2a.Artifact{
			nil,
			{ID: "metadata-nil"},
			{ID: "unknown", Metadata: map[string]any{"kind": "other"}},
			oldManifest, newManifest, equalManifest, result, logArtifact,
		},
	}
	if err := compactTask(task); err != nil {
		t.Fatal(err)
	}
	if task.History != nil || task.Metadata != nil || len(task.Artifacts) != 2 {
		t.Fatalf("压缩结果 = %+v", task)
	}
	if task.Artifacts[0].ID != equalManifest.ID || task.Artifacts[1].ID != result.ID {
		t.Fatalf("保留 Artifact = %+v", task.Artifacts)
	}
	for _, artifact := range task.Artifacts {
		metadata, _, ok, err := decodeArtifactMetadata(artifact)
		if err != nil || !ok || !metadata.Compacted {
			t.Fatalf("压缩 metadata = %+v, ok=%v err=%v", metadata, ok, err)
		}
	}
}

func TestDecodeArtifactMetadataHandlesAbsentForeignAndMalformedValues(t *testing.T) {
	for _, artifact := range []*a2a.Artifact{nil, {}, {Metadata: map[string]any{}}, {Metadata: map[string]any{"kind": "foreign"}}} {
		if _, _, ok, err := decodeArtifactMetadata(artifact); err != nil || ok {
			t.Fatalf("无扩展 metadata = ok=%v err=%v", ok, err)
		}
	}
	if _, ok, err := decodeArtifactMetadataValue(map[string]any{"kind": 1}); err != nil || ok {
		t.Fatalf("非字符串 kind = ok=%v err=%v", ok, err)
	}
	if _, ok, err := decodeArtifactMetadataValue(map[string]any{"kind": a2aext.ArtifactKind}); err == nil || ok {
		t.Fatalf("不完整 metadata = ok=%v err=%v", ok, err)
	}
	if _, _, err := decodeArtifactMetadataValue(map[string]any{"bad": func() {}}); err == nil {
		t.Fatal("不可编码 metadata 未失败")
	}
	artifact := &a2a.Artifact{Metadata: map[string]any{a2aext.ExtensionURI: map[string]any{"kind": a2aext.ArtifactKind}}}
	if _, nested, ok, err := decodeArtifactMetadata(artifact); err == nil || ok || !nested {
		t.Fatalf("畸形嵌套 metadata = nested=%v ok=%v err=%v", nested, ok, err)
	}
}
