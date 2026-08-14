package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/cover"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "用法：filter_go_coverage <原始-profile> <过滤后-profile> <生成文件清单>")
		os.Exit(2)
	}
	if err := filterCoverageProfile(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func filterCoverageProfile(inputFilename, outputFilename, excludedFilename string) error {
	excluded, err := readExcludedFiles(excludedFilename)
	if err != nil {
		return err
	}
	profiles, err := cover.ParseProfiles(inputFilename)
	if err != nil {
		return fmt.Errorf("解析 Go 覆盖率 profile：%w", err)
	}
	if len(profiles) == 0 {
		return fmt.Errorf("Go 覆盖率 profile 不包含源码记录")
	}

	directory := filepath.Dir(outputFilename)
	temporary, err := os.CreateTemp(directory, ".coverage-filter-*.out")
	if err != nil {
		return fmt.Errorf("创建临时覆盖率 profile：%w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	writer := bufio.NewWriter(temporary)
	if _, err := fmt.Fprintf(writer, "mode: %s\n", profiles[0].Mode); err != nil {
		temporary.Close()
		return fmt.Errorf("写入覆盖率模式：%w", err)
	}
	merged := make(map[string]map[coverageBlockKey]cover.ProfileBlock)
	for _, profile := range profiles {
		filename := normalizeSourcePath(profile.FileName)
		if _, skip := excluded[filename]; skip {
			continue
		}
		if merged[filename] == nil {
			merged[filename] = make(map[coverageBlockKey]cover.ProfileBlock)
		}
		for _, block := range profile.Blocks {
			key := coverageBlockKey{
				startLine: block.StartLine, startCol: block.StartCol,
				endLine: block.EndLine, endCol: block.EndCol,
			}
			existing, found := merged[filename][key]
			if found && existing.NumStmt != block.NumStmt {
				temporary.Close()
				return fmt.Errorf("合并 Go 覆盖率块 %s:%d.%d 时语句数冲突", filename, block.StartLine, block.StartCol)
			}
			if !found {
				existing = block
				existing.Count = 0
			}
			if profiles[0].Mode == "set" {
				if block.Count > existing.Count {
					existing.Count = block.Count
				}
			} else {
				existing.Count += block.Count
			}
			merged[filename][key] = existing
		}
	}
	if err := writeMergedProfiles(writer, merged); err != nil {
		temporary.Close()
		return err
	}
	if err := writer.Flush(); err != nil {
		temporary.Close()
		return fmt.Errorf("刷新覆盖率 profile：%w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭覆盖率 profile：%w", err)
	}
	if err := os.Rename(temporaryName, outputFilename); err != nil {
		return fmt.Errorf("保存过滤后的覆盖率 profile：%w", err)
	}
	return nil
}

type coverageBlockKey struct {
	startLine int
	startCol  int
	endLine   int
	endCol    int
}

func writeMergedProfiles(writer *bufio.Writer, profiles map[string]map[coverageBlockKey]cover.ProfileBlock) error {
	filenames := make([]string, 0, len(profiles))
	for filename := range profiles {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)
	for _, filename := range filenames {
		blocks := make([]cover.ProfileBlock, 0, len(profiles[filename]))
		for _, block := range profiles[filename] {
			blocks = append(blocks, block)
		}
		sort.Slice(blocks, func(i, j int) bool {
			left, right := blocks[i], blocks[j]
			if left.StartLine != right.StartLine {
				return left.StartLine < right.StartLine
			}
			if left.StartCol != right.StartCol {
				return left.StartCol < right.StartCol
			}
			if left.EndLine != right.EndLine {
				return left.EndLine < right.EndLine
			}
			return left.EndCol < right.EndCol
		})
		for _, block := range blocks {
			if _, err := fmt.Fprintf(writer, "%s:%d.%d,%d.%d %d %d\n",
				filename, block.StartLine, block.StartCol, block.EndLine, block.EndCol, block.NumStmt, block.Count,
			); err != nil {
				return fmt.Errorf("写入覆盖率记录：%w", err)
			}
		}
	}
	return nil
}

func readExcludedFiles(filename string) (map[string]struct{}, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("读取生成文件清单：%w", err)
	}
	defer file.Close()

	result := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name := normalizeSourcePath(scanner.Text())
		if name != "" {
			result[name] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("扫描生成文件清单：%w", err)
	}
	return result, nil
}

func normalizeSourcePath(filename string) string {
	name := filepath.ToSlash(filepath.Clean(strings.TrimSpace(filename)))
	return strings.TrimPrefix(name, "./")
}
