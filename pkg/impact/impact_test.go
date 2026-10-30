package impact

import (
	"testing"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/diff"
	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/index"
	"github.com/topolink/topolink/pkg/llm"
)

func TestChangedFunctionsAndAffectedEntries(t *testing.T) {
	funcs := []ast.FuncSummary{
		{Symbol: ast.Symbol{Name: "Handler"}, File: "/repo/handler.go", StartLine: 5, EndLine: 20},
		{Symbol: ast.Symbol{Name: "FindByID"}, File: "/repo/repo.go", StartLine: 8, EndLine: 12},
		{Symbol: ast.Symbol{Name: "Other"}, File: "/repo/other.go", StartLine: 1, EndLine: 10},
	}
	changes := []diff.FileChanges{
		{Path: "repo.go", Lines: []int{9}}, // 命中 FindByID
	}

	changed := ChangedFunctions(funcs, changes)
	if len(changed) != 1 || changed[0].Symbol.Name != "FindByID" {
		t.Fatalf("changed = %+v, want [FindByID]", changed)
	}

	idx := index.New()
	idx.FuncEntries[ast.Symbol{Name: "FindByID"}] = []string{"http:GET:/orders"}

	entries := AffectedEntries(idx, changed)
	if len(entries) != 1 || entries[0] != "http:GET:/orders" {
		t.Fatalf("affected = %v, want [http:GET:/orders]", entries)
	}
}

func TestFilterFindings(t *testing.T) {
	findings := []engine.Finding{
		{Rule: "R1", Level: llm.RiskMedium, EntryID: "http:GET:/orders"},
		{Rule: "R5", Level: llm.RiskFatal, EntryID: "http:DELETE:/orders"},
	}
	got := FilterFindings(findings, []string{"http:GET:/orders"})
	if len(got) != 1 || got[0].Rule != "R1" {
		t.Fatalf("filtered = %+v, want only R1 on http:GET:/orders", got)
	}
}

func TestChangedTables(t *testing.T) {
	changes := []diff.FileChanges{
		{
			Path:    "repo.go",
			Added:   []string{`	r.db.Table("orders").First(&o)`},
			Removed: []string{`	db.Exec("DELETE FROM orders_old")`},
		},
		{
			Path:  "other.go",
			Added: []string{`	tx.Exec("UPDATE accounts SET balance = 0")`},
		},
	}

	got := ChangedTables(changes)
	want := map[string]bool{"orders": true, "orders_old": true, "accounts": true}
	if len(got) != len(want) {
		t.Fatalf("ChangedTables = %v, want %d tables", got, len(want))
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected table %q (got %v)", g, got)
		}
	}
}

func TestChangedEntries(t *testing.T) {
	entries := []ast.EntryNode{
		{ID: "http:GET:/orders", SourceLoc: ast.Loc{File: "/repo/router.go", Line: 6}},
		{ID: "http:POST:/orders", SourceLoc: ast.Loc{File: "/repo/router.go", Line: 7}},
	}
	changes := []diff.FileChanges{
		{Path: "router.go", Lines: []int{6}},
	}
	got := ChangedEntries(entries, changes)
	if len(got) != 1 || got[0].ID != "http:GET:/orders" {
		t.Fatalf("changedEntries = %+v, want [http:GET:/orders]", got)
	}
}

func TestAffectedEntriesByTablesAndMerge(t *testing.T) {
	idx := index.New()
	idx.Reverse[ast.ResourceKey{Kind: ast.ResourceTable, Name: "orders"}] = []ast.Ref{
		{EntryID: "http:GET:/orders"},
	}
	idx.Reverse[ast.ResourceKey{Kind: ast.ResourceTable, Name: "accounts"}] = []ast.Ref{
		{EntryID: "http:POST:/transfer"},
	}

	byTable := AffectedEntriesByTables(idx, []string{"orders", "accounts"})
	if len(byTable) != 2 {
		t.Fatalf("byTable = %v, want 2 entries", byTable)
	}

	merged := MergeEntries([]string{"http:GET:/orders"}, byTable)
	if len(merged) != 2 {
		t.Fatalf("merged = %v, want 2 after dedup", merged)
	}
}
