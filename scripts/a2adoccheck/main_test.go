package a2adoccheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var chineseText = regexp.MustCompile(`\p{Han}`)

var explicitA2AProductionFiles = map[string]struct{}{
	"manager/internal/app/reconciler.go": {},
}

// TestA2APublicDocumentation 检查本次 A2A 生产 API 的中文文档契约。
func TestA2APublicDocumentation(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var failures []string
	err = filepath.WalkDir(repositoryRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".gopath" || entry.Name() == ".tools" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !isA2AProductionFile(repositoryRoot, path) {
			return nil
		}
		fileFailures, err := checkFileDocumentation(path)
		if err != nil {
			return err
		}
		failures = append(failures, fileFailures...)
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 A2A Go 文件：%v", err)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		t.Fatalf("A2A 公共 API 文档不完整：\n%s", strings.Join(failures, "\n"))
	}
}

func isA2AProductionFile(root, path string) bool {
	if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	relative = filepath.ToSlash(relative)
	if _, ok := explicitA2AProductionFiles[relative]; ok {
		return true
	}
	parts := strings.Split(relative, "/")
	for _, part := range parts[:len(parts)-1] {
		if strings.HasPrefix(strings.ToLower(part), "a2a") {
			return true
		}
	}
	return strings.HasPrefix(strings.ToLower(filepath.Base(path)), "a2a")
}

func checkFileDocumentation(filename string) ([]string, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var failures []string
	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			if value.Name.IsExported() && receiverIsPublic(value.Recv) {
				failures = append(failures, validateFunctionDoc(fileSet, filename, value)...)
			}
		case *ast.GenDecl:
			if value.Tok != token.TYPE {
				continue
			}
			for _, specification := range value.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok || !typeSpec.Name.IsExported() {
					continue
				}
				doc := typeSpec.Doc
				if doc == nil {
					doc = value.Doc
				}
				text := documentationText(doc)
				if !strings.HasPrefix(text, typeSpec.Name.Name) || !chineseText.MatchString(text) {
					position := fileSet.Position(typeSpec.Pos())
					failures = append(failures, formatFailure(filename, position.Line, typeSpec.Name.Name, "导出类型缺少以名称开头的中文职责说明"))
				}
			}
		}
	}
	return failures, nil
}

func receiverIsPublic(receiver *ast.FieldList) bool {
	if receiver == nil || len(receiver.List) == 0 {
		return true
	}
	typeExpression := receiver.List[0].Type
	if pointer, ok := typeExpression.(*ast.StarExpr); ok {
		typeExpression = pointer.X
	}
	identifier, ok := typeExpression.(*ast.Ident)
	return ok && identifier.IsExported()
}

func validateFunctionDoc(fileSet *token.FileSet, filename string, declaration *ast.FuncDecl) []string {
	text := documentationText(declaration.Doc)
	position := fileSet.Position(declaration.Pos())
	var failures []string
	if !strings.HasPrefix(text, declaration.Name.Name) || !chineseText.MatchString(text) {
		failures = append(failures, formatFailure(filename, position.Line, declaration.Name.Name, "缺少以函数名开头的中文功能简述"))
	}
	for _, section := range []string{"参数：", "返回：", "错误："} {
		if !strings.Contains(text, section) {
			failures = append(failures, formatFailure(filename, position.Line, declaration.Name.Name, "缺少 "+section+" 文档段"))
		}
	}
	return failures
}

func documentationText(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	return strings.TrimSpace(group.Text())
}

func formatFailure(filename string, line int, name, reason string) string {
	return filename + ":" + strconv.Itoa(line) + ": " + name + "：" + reason
}
