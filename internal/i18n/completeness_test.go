package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSourceStringsTranslated walks the Go sources and verifies that every
// i18n.T("literal") used by the interface has an English and a Spanish
// translation. Portuguese is the source language and needs no catalog.
func TestSourceStringsTranslated(t *testing.T) {
	if err := CatalogError(); err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string][]string{}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".agent-teams", "bin", "temp", "vendor", "docs", "node_modules":
				if rel != "." {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "T" {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || ident.Name != "i18n" || len(call.Args) == 0 {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			source, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				return true
			}
			keys[source] = append(keys[source], rel)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk sources: %v", err)
	}

	var missingEN, missingES []string
	for source, files := range keys {
		if !HasTranslationFor(EN, source) {
			missingEN = append(missingEN, source+" ("+strings.Join(unique(files), ", ")+")")
		}
		if !HasTranslationFor(ES, source) {
			missingES = append(missingES, source+" ("+strings.Join(unique(files), ", ")+")")
		}
	}
	if len(missingEN) > 0 {
		t.Errorf("missing English translations (%d):\n%s", len(missingEN), strings.Join(missingEN, "\n"))
	}
	if len(missingES) > 0 {
		t.Errorf("missing Spanish translations (%d):\n%s", len(missingES), strings.Join(missingES, "\n"))
	}
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
