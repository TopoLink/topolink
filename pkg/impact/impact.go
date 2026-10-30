// Package impact 把 diff 变更映射到函数与受影响入口（diff 驱动的爆炸半径）。
package impact

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/diff"
	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/index"
)

var (
	// GORM: db.Table("orders") / db.Table(`orders`)
	gormTableRe = regexp.MustCompile("\\.Table\\(\\s*[\"`]([^\"`]+)[\"`]")
	// 常见 SQL: FROM / JOIN / INTO / UPDATE <table>
	sqlTableRe = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE)\s+([a-zA-Z_][\w]*)`)
)

// ChangedTables 从 diff 的新增/删除文本中提取表名（GORM Table(...) 与常见 SQL）。
func ChangedTables(changes []diff.FileChanges) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	for _, c := range changes {
		for _, line := range c.Added {
			for _, t := range extractTables(line) {
				add(t)
			}
		}
		for _, line := range c.Removed {
			for _, t := range extractTables(line) {
				add(t)
			}
		}
	}
	return out
}

func extractTables(line string) []string {
	var out []string
	for _, m := range gormTableRe.FindAllStringSubmatch(line, -1) {
		out = append(out, m[1])
	}
	for _, m := range sqlTableRe.FindAllStringSubmatch(line, -1) {
		out = append(out, m[1])
	}
	return out
}

// ChangedEntries 返回路由/入口注册行被直接改动的入口。
func ChangedEntries(entries []ast.EntryNode, changes []diff.FileChanges) []ast.EntryNode {
	var out []ast.EntryNode
	for _, e := range entries {
		if entryChanged(e, changes) {
			out = append(out, e)
		}
	}
	return out
}

func entryChanged(e ast.EntryNode, changes []diff.FileChanges) bool {
	for _, c := range changes {
		if !fileMatches(e.SourceLoc.File, c.Path) {
			continue
		}
		for _, line := range c.Lines {
			if line == e.SourceLoc.Line {
				return true
			}
		}
	}
	return false
}

// AffectedEntriesByTables 返回使用/读写这些表的入口 ID（走反查索引）。
func AffectedEntriesByTables(idx *index.Index, tables []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tables {
		key := ast.ResourceKey{Kind: ast.ResourceTable, Name: t}
		for _, ref := range idx.Reverse[key] {
			if ref.EntryID != "" && !seen[ref.EntryID] {
				seen[ref.EntryID] = true
				out = append(out, ref.EntryID)
			}
		}
	}
	return out
}

// MergeEntries 合并两组入口 ID 并去重（保持 a 在前）。
func MergeEntries(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range a {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range b {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ChangedFunctions 返回包含 diff 变更行的函数/方法。
func ChangedFunctions(funcs []ast.FuncSummary, changes []diff.FileChanges) []ast.FuncSummary {
	var out []ast.FuncSummary
	for _, f := range funcs {
		if functionChanged(f, changes) {
			out = append(out, f)
		}
	}
	return out
}

func functionChanged(f ast.FuncSummary, changes []diff.FileChanges) bool {
	for _, c := range changes {
		if !fileMatches(f.File, c.Path) {
			continue
		}
		for _, line := range c.Lines {
			if line >= f.StartLine && line <= f.EndLine {
				return true
			}
		}
	}
	return false
}

// fileMatches 判断函数文件（绝对路径）是否对应 diff 里的相对路径。
func fileMatches(funcFile, diffPath string) bool {
	f := filepath.ToSlash(funcFile)
	d := filepath.ToSlash(diffPath)
	return f == d || strings.HasSuffix(f, "/"+d)
}

// AffectedEntries 返回受这些函数影响的入口 ID（去重，保持顺序）。
func AffectedEntries(idx *index.Index, changed []ast.FuncSummary) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range changed {
		for _, id := range idx.FuncEntries[f.Symbol] {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// FilterFindings 只保留归属到给定入口集合的 findings。
func FilterFindings(findings []engine.Finding, entries []string) []engine.Finding {
	set := make(map[string]bool, len(entries))
	for _, id := range entries {
		set[id] = true
	}
	var out []engine.Finding
	for _, f := range findings {
		if set[f.EntryID] {
			out = append(out, f)
		}
	}
	return out
}
