package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topolink/topolink/pkg/ast"
)

func TestBuildUnits(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "handler.go")
	src := "package h\n\nfunc GetOrders() {\n\treturn\n}\n\nfunc Auth() {}\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := []ast.EntryNode{{
		ID:         "http:GET:/orders",
		Kind:       ast.EntryHTTP,
		Name:       "/orders",
		Handler:    ast.Symbol{Name: "GetOrders"},
		Middleware: []string{"Auth()"},
		SourceLoc:  ast.Loc{File: file, Line: 3},
	}}
	funcs := []ast.FuncSummary{
		{Symbol: ast.Symbol{Name: "GetOrders"}, File: file, StartLine: 3, EndLine: 5},
		{Symbol: ast.Symbol{Name: "Auth"}, File: file, StartLine: 7, EndLine: 7},
	}
	ops := []ast.DBOp{{Resource: "orders", Loc: ast.Loc{File: file, Line: 3}}}

	units := BuildUnits(entries, funcs, ops)
	byID := make(map[string]Unit, len(units))
	for _, u := range units {
		byID[u.ID] = u
	}

	for _, id := range []string{"entry:http:GET:/orders", "middleware:Auth", "table:orders"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("missing unit %q in %+v", id, units)
		}
	}

	// 入口单元应包含 handler 源码，用于内容指纹与 AI 输入。
	if !strings.Contains(byID["entry:http:GET:/orders"].Source, "GetOrders") {
		t.Errorf("entry source missing handler body: %q", byID["entry:http:GET:/orders"].Source)
	}
	if byID["middleware:Auth"].Loc.Line != 7 {
		t.Errorf("middleware loc = %+v, want line 7", byID["middleware:Auth"].Loc)
	}
	if byID["table:orders"].Kind != "table" {
		t.Errorf("table kind = %q", byID["table:orders"].Kind)
	}
}

func TestFingerprintChangesWithSource(t *testing.T) {
	a := Fingerprint("entry", "entry:x", "v1")
	b := Fingerprint("entry", "entry:x", "v2")
	if a == b {
		t.Fatalf("fingerprint should change with source: %s", a)
	}
	if a != Fingerprint("entry", "entry:x", "v1") {
		t.Fatalf("fingerprint should be deterministic")
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "semantic.json")
	idx := NewIndex()
	idx.Units["entry:x"] = Cached{
		Kind:        "entry",
		Fingerprint: "sha256:abc",
		Annotation:  Annotation{Text: "order query API"},
	}

	if err := Save(path, idx); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOrNew(path)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := got.Units["entry:x"]
	if !ok || c.Annotation.Text != "order query API" || c.Fingerprint != "sha256:abc" {
		t.Fatalf("roundtrip mismatch: %+v", got.Units)
	}
}

func TestLoadOrNewMissing(t *testing.T) {
	idx, err := LoadOrNew(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Units) != 0 {
		t.Fatalf("want empty index, got %+v", idx.Units)
	}
}
