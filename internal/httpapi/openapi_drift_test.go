package httpapi

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type routeKey struct {
	Method string
	Path   string
}

var httpMethods = map[string]struct{}{
	"get": {}, "post": {}, "put": {}, "patch": {}, "delete": {}, "head": {}, "options": {}, "trace": {},
}

func TestOpenAPIRouteDrift(t *testing.T) {
	repoRoot := repositoryRoot(t)
	registered := registeredRoutes(t, filepath.Join(repoRoot, "internal", "httpapi"))
	documented := documentedRoutes(t, filepath.Join(repoRoot, "docs", "api", "openapi.yaml"))

	missingInOAS := difference(registered, documented)
	missingInRouter := difference(documented, registered)
	if len(missingInOAS) == 0 && len(missingInRouter) == 0 {
		return
	}

	t.Fatalf("OpenAPI route drift detected:\n  registered but not documented: %v\n  documented but not registered: %v", missingInOAS, missingInRouter)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func registeredRoutes(t *testing.T, dir string) map[routeKey]struct{} {
	t.Helper()
	set := make(map[routeKey]struct{})
	fset := token.NewFileSet()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Handle" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			pattern, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			method, path, ok := strings.Cut(pattern, " ")
			if !ok || !strings.HasPrefix(path, "/api/v1/") {
				return true
			}
			set[routeKey{Method: strings.ToUpper(method), Path: normalizePath(path)}] = struct{}{}
			return true
		})
	}
	return set
}

func documentedRoutes(t *testing.T, filename string) map[routeKey]struct{} {
	t.Helper()
	f, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	set := make(map[routeKey]struct{})
	scanner := bufio.NewScanner(f)
	inPaths := false
	currentPath := ""
	for scanner.Scan() {
		line := scanner.Text()
		if line == "paths:" {
			inPaths = true
			continue
		}
		if !inPaths {
			continue
		}
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			currentPath = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if currentPath == "" || !strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "      ") {
			continue
		}
		method := strings.TrimSuffix(strings.TrimSpace(line), ":")
		if _, ok := httpMethods[method]; ok {
			set[routeKey{Method: strings.ToUpper(method), Path: normalizePath(currentPath)}] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return set
}

func normalizePath(path string) string {
	path = strings.TrimPrefix(path, "/api/v1")
	if path == "" {
		return "/"
	}
	return path
}

func difference(left, right map[routeKey]struct{}) []string {
	var result []string
	for route := range left {
		if _, ok := right[route]; !ok {
			result = append(result, fmt.Sprintf("%s %s", route.Method, route.Path))
		}
	}
	sort.Strings(result)
	return result
}
