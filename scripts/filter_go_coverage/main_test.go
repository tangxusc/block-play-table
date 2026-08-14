package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/cover"
)

func TestFilterCoverageProfileExcludesOnlyGeneratedFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	input := filepath.Join(directory, "raw.out")
	output := filepath.Join(directory, "filtered.out")
	excluded := filepath.Join(directory, "generated.txt")
	writeTestFile(t, input, strings.Join([]string{
		"mode: set",
		"example.test/pkg/generated.go:1.1,2.1 1 1",
		"example.test/pkg/handwritten.go:3.1,4.1 2 1",
		"",
	}, "\n"))
	writeTestFile(t, excluded, "./example.test/pkg/generated.go\n")

	if err := filterCoverageProfile(input, output, excluded); err != nil {
		t.Fatalf("过滤覆盖率 profile：%v", err)
	}
	profiles, err := cover.ParseProfiles(output)
	if err != nil {
		t.Fatalf("解析过滤结果：%v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("过滤结果文件数 = %d，期望 1", len(profiles))
	}
	if profiles[0].FileName != "example.test/pkg/handwritten.go" {
		t.Fatalf("保留文件 = %q，期望手写文件", profiles[0].FileName)
	}
	if profiles[0].Blocks[0].NumStmt != 2 || profiles[0].Blocks[0].Count != 1 {
		t.Fatalf("手写文件覆盖数据被改变：%+v", profiles[0].Blocks[0])
	}
}

func TestFilterCoverageProfileRejectsEmptyProfile(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	input := filepath.Join(directory, "raw.out")
	output := filepath.Join(directory, "filtered.out")
	excluded := filepath.Join(directory, "generated.txt")
	writeTestFile(t, input, "mode: set\n")
	writeTestFile(t, excluded, "")

	err := filterCoverageProfile(input, output, excluded)
	if err == nil || !strings.Contains(err.Error(), "不包含源码记录") {
		t.Fatalf("空 profile 错误 = %v", err)
	}
}

func TestFilterCoverageProfileMergesDuplicatePackageRuns(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	input := filepath.Join(directory, "raw.out")
	output := filepath.Join(directory, "filtered.out")
	excluded := filepath.Join(directory, "generated.txt")
	writeTestFile(t, input, strings.Join([]string{
		"mode: set",
		"example.test/pkg/file.go:3.1,4.1 2 0",
		"example.test/pkg/file.go:3.1,4.1 2 1",
		"example.test/pkg/file.go:5.1,6.1 1 0",
		"",
	}, "\n"))
	writeTestFile(t, excluded, "")

	if err := filterCoverageProfile(input, output, excluded); err != nil {
		t.Fatalf("合并覆盖率 profile：%v", err)
	}
	profiles, err := cover.ParseProfiles(output)
	if err != nil {
		t.Fatalf("解析合并结果：%v", err)
	}
	if len(profiles) != 1 || len(profiles[0].Blocks) != 2 {
		t.Fatalf("重复块未合并：%+v", profiles)
	}
	if profiles[0].Blocks[0].Count != 1 || profiles[0].Blocks[0].NumStmt != 2 {
		t.Fatalf("合并后的命中块错误：%+v", profiles[0].Blocks[0])
	}
}

func TestFilterCoverageProfileRejectsConflictingDuplicateBlocks(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	input := filepath.Join(directory, "raw.out")
	output := filepath.Join(directory, "filtered.out")
	excluded := filepath.Join(directory, "generated.txt")
	writeTestFile(t, input, strings.Join([]string{
		"mode: count",
		"example.test/pkg/file.go:3.1,4.1 1 1",
		"example.test/pkg/file.go:3.1,4.1 2 1",
		"",
	}, "\n"))
	writeTestFile(t, excluded, "")

	err := filterCoverageProfile(input, output, excluded)
	if err == nil || (!strings.Contains(err.Error(), "语句数冲突") && !strings.Contains(err.Error(), "inconsistent NumStmt")) {
		t.Fatalf("冲突块应失败：%v", err)
	}
}

func TestNormalizeSourcePath(t *testing.T) {
	t.Parallel()
	if got := normalizeSourcePath(" ./example.test/pkg/../pkg/file.go \n"); got != "example.test/pkg/file.go" {
		t.Fatalf("归一化路径 = %q", got)
	}
}

func writeTestFile(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试文件 %s：%v", filename, err)
	}
}
