package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/diff"
	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/impact"
)

// 跨仓库场景：业务仓调用共享库仓的函数，库函数内部才真正访问数据库。
func TestCrossRepoFinding(t *testing.T) {
	libDir := t.TempDir()
	bizDir := t.TempDir()

	writeLib(t, libDir)
	writeBiz(t, bizDir)

	findings, err := run([]string{libDir, bizDir}, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	entry, ok := findR1(findings)
	if !ok {
		t.Fatalf("want R1 across repos, got %+v", findings)
	}
	if entry != "http:GET:/orders" {
		t.Errorf("entry = %q, want http:GET:/orders", entry)
	}
}

// 库画像场景：先分析库仓生成画像，再只分析业务仓 + 载入画像，验证无需库源码也能溯源。
func TestProfileAcrossRepos(t *testing.T) {
	libDir := t.TempDir()
	bizDir := t.TempDir()
	profilePath := filepath.Join(t.TempDir(), "lib.json")

	writeLib(t, libDir)
	writeBiz(t, bizDir)

	// 1. 只分析库仓，写出画像（库仓无入口，不产生 finding）
	if _, err := run([]string{libDir}, nil, profilePath); err != nil {
		t.Fatal(err)
	}

	// 2. 只分析业务仓 + 载入画像（库源码不在分析范围）
	findings, err := run([]string{bizDir}, []string{profilePath}, "")
	if err != nil {
		t.Fatal(err)
	}

	entry, ok := findR1(findings)
	if !ok {
		t.Fatalf("want R1 via profile, got %+v", findings)
	}
	if entry != "http:GET:/orders" {
		t.Errorf("entry = %q, want http:GET:/orders", entry)
	}
}

func TestAutoDiffRange(t *testing.T) {
	cases := []struct{ event, base, want string }{
		{"pull_request", "main", "origin/main...HEAD"},
		{"push", "main", ""},
		{"pull_request", "", ""},
	}
	for _, c := range cases {
		if got := autoDiffRange(c.event, c.base); got != c.want {
			t.Errorf("autoDiffRange(%q,%q) = %q, want %q", c.event, c.base, got, c.want)
		}
	}
}

func writeLib(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/lib\n\ngo 1.21\n\nrequire gorm.io/gorm v1.25.0\n")
	writeFile(t, filepath.Join(dir, "lib.go"), `package lib

func QueryOrders(db *gorm.DB, id int) {
	var o Order
	db.Table("orders").Where("id = ?", id).First(&o)
}
`)
}

func writeBiz(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/biz\n\ngo 1.21\n\nrequire (\n\tgithub.com/gin-gonic/gin v1.9.1\n\texample.com/lib v0.0.0\n)\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Handler(db *gorm.DB, ids []int) {
	for _, id := range ids {
		lib.QueryOrders(db, id)
	}
}

func setup() {
	r := gin.Default()
	r.GET("/orders", Handler)
}
`)
}

func findR1(findings []engine.Finding) (string, bool) {
	for _, f := range findings {
		if f.Rule == "R1" {
			return f.EntryID, true
		}
	}
	return "", false
}

// 反查影响查询：改 table orders / 改方法 QueryOrders 会影响哪些入口。
func TestImpactQuery(t *testing.T) {
	libDir := t.TempDir()
	bizDir := t.TempDir()
	writeLib(t, libDir)
	writeBiz(t, bizDir)

	idx, err := analyze([]string{libDir, bizDir}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// 表级：orders 被 http:GET:/orders 读
	tableRefs := idx.Reverse[ast.ResourceKey{Kind: ast.ResourceTable, Name: "orders"}]
	var tableHit bool
	for _, r := range tableRefs {
		if r.EntryID == "http:GET:/orders" && r.Op == ast.OpRead {
			tableHit = true
		}
	}
	if !tableHit {
		t.Errorf("want table orders -> http:GET:/orders Read, got %+v", tableRefs)
	}

	// 方法级：QueryOrders 被 http:GET:/orders 使用
	methodRefs := idx.Reverse[ast.ResourceKey{Kind: ast.ResourceMethod, Name: "QueryOrders"}]
	if len(methodRefs) == 0 {
		t.Errorf("want method QueryOrders impact refs, got 0")
	}
}

// diff 模式：只报告改动函数波及到的入口（爆炸半径），含语义与 job summary。
func TestDiffBlastRadiusReport(t *testing.T) {
	libDir := t.TempDir()
	bizDir := t.TempDir()
	writeLib(t, libDir)
	writeBiz(t, bizDir)

	idx, err := analyze([]string{libDir, bizDir}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := run([]string{libDir, bizDir}, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// main.go 第 5 行位于 Handler 内（Handler 是唯一调用 lib.QueryOrders 的函数）。
	changes := []diff.FileChanges{{Path: "main.go", Lines: []int{5}}}
	rep := diffReport{
		changedFuncs:   impact.ChangedFunctions(idx.Funcs, changes),
		changedTables:  impact.ChangedTables(changes),
		changedEntries: impact.ChangedEntries(idx.Entries, changes),
		entries:        entryMap(idx.Entries),
	}
	changedIDs := make([]string, 0, len(rep.changedEntries))
	for _, e := range rep.changedEntries {
		changedIDs = append(changedIDs, e.ID)
	}
	rep.affected = impact.MergeEntries(changedIDs, impact.MergeEntries(
		impact.AffectedEntries(idx, rep.changedFuncs),
		impact.AffectedEntriesByTables(idx, rep.changedTables),
	))
	rep.findings = impact.FilterFindings(findings, rep.affected)

	md := impactMarkdown(rep)
	for _, want := range []string{"Diff Impact", "http:GET:/orders", "query-in-loop"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}

	// github 格式应写入 job summary。
	summary := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	renderDiff(rep, "github")
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "http:GET:/orders") {
		t.Errorf("job summary missing blast radius:\n%s", data)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
