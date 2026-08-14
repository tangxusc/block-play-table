package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type coveragePoint struct {
	Start      string
	Code       string
	TrueCount  int
	FalseCount int
}

func main() {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "用法：gobco_stats <stats.json> <excluded-files.txt> [source-dir]")
		os.Exit(2)
	}
	includeDir := ""
	if len(os.Args) == 4 {
		includeDir = filepath.ToSlash(filepath.Clean(strings.TrimSpace(os.Args[3])))
	}

	excluded, err := readExcludedFiles(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var points []coveragePoint
	if err := json.Unmarshal(data, &points); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	type aggregate struct {
		filename     string
		trueCovered  bool
		falseCovered bool
	}
	aggregates := make(map[string]aggregate)
	for _, point := range points {
		filename := sourceFile(point.Start)
		if includeDir != "" && filepath.ToSlash(filepath.Dir(filename)) != includeDir {
			continue
		}
		if _, skip := excluded[filename]; skip {
			continue
		}
		key := point.Start + "\x00" + point.Code
		current := aggregates[key]
		current.filename = filename
		current.trueCovered = current.trueCovered || point.TrueCount > 0
		current.falseCovered = current.falseCovered || point.FalseCount > 0
		aggregates[key] = current
	}
	covered := 0
	total := 0
	for _, point := range aggregates {
		total += 2
		if point.trueCovered {
			covered++
		}
		if point.falseCovered {
			covered++
		}
	}
	fmt.Printf("%d %d\n", covered, total)
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
		name := filepath.ToSlash(strings.TrimSpace(scanner.Text()))
		if name != "" {
			result[name] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("扫描生成文件清单：%w", err)
	}
	return result, nil
}

func sourceFile(position string) string {
	lastColon := strings.LastIndex(position, ":")
	if lastColon < 0 {
		return filepath.ToSlash(position)
	}
	secondColon := strings.LastIndex(position[:lastColon], ":")
	if secondColon < 0 {
		return filepath.ToSlash(position)
	}
	return filepath.ToSlash(position[:secondColon])
}
