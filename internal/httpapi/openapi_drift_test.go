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

var ignoredRoutes = map[routeKey]struct{}{
	{Method: "GET", Path: "/health"}:  {},
	{Method: "GET", Path: "/livez"}:   {},
	{Method: "GET", Path: "/readyz"}:  {},
	{Method: "GET", Path: "/metrics"}: {},
}

func TestOpenAPIRouteDrift(t *testing.T) {
	repoRoot := repositoryRoot(t)
	registered := registeredRoutes(t, filepath.Join(repoRoot, "internal"))
	documented := documentedRoutes(t, filepath.Join(repoRoot, "docs", "api", "openapi.yaml"))

	// Удаляем игнорируемые маршруты
	for route := range ignoredRoutes {
		delete(registered, route)
	}

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

func registeredRoutes(t *testing.T, rootDir string) map[routeKey]struct{} {
	t.Helper()
	set := make(map[routeKey]struct{})
	fset := token.NewFileSet()

	err := filepath.WalkDir(rootDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" || entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		collectRegisteredRoutes(t, fset, path, set)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func collectRegisteredRoutes(t *testing.T, fset *token.FileSet, filename string, set map[routeKey]struct{}) {
	t.Helper()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		return
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if route, ok := routeFromHandleCall(node); ok {
			set[route] = struct{}{}
		}
		return true
	})
}

func routeFromHandleCall(node ast.Node) (routeKey, bool) {
	call, ok := node.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return routeKey{}, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return routeKey{}, false
	}
	// Поддерживаем как Handle, так и HandleFunc
	if sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc" {
		return routeKey{}, false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return routeKey{}, false
	}
	pattern, err := strconv.Unquote(lit.Value)
	if err != nil {
		return routeKey{}, false
	}
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return routeKey{}, false
	}
	return routeKey{Method: strings.ToUpper(method), Path: normalizePath(path)}, true
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
			fullPath := "/api/v1" + currentPath
			set[routeKey{Method: strings.ToUpper(method), Path: normalizePath(fullPath)}] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return set
}

func normalizePath(path string) string {
	if strings.HasPrefix(path, "/api/v1") {
		path = strings.TrimPrefix(path, "/api/v1")
	}
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
