package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/diff"
	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/report"
	"github.com/topolink/topolink/pkg/semantic"
)

// autoDiffRange 在 GitHub PR 事件下推断 diff 范围；非 PR 返回空（回退全仓扫描）。
func autoDiffRange(event, baseRef string) string {
	if event == "pull_request" && baseRef != "" {
		return "origin/" + baseRef + "...HEAD"
	}
	return ""
}

// loadDiff 读取 diff 文件或运行 git diff。
func loadDiff(diffRange, diffFile string, repos []string) ([]diff.FileChanges, error) {
	if diffFile != "" {
		f, err := os.Open(diffFile)
		if err != nil {
			return nil, fmt.Errorf("open diff %s: %w", diffFile, err)
		}
		defer f.Close()
		return diff.Parse(f)
	}

	repo := "."
	if len(repos) > 0 {
		repo = repos[0]
	}
	out, err := exec.Command("git", "-C", repo, "diff", "--unified=0", diffRange).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", diffRange, err)
	}
	return diff.Parse(bytes.NewReader(out))
}

// diffReport 是 diff 影响报告的数据。
type diffReport struct {
	changedFuncs   []ast.FuncSummary
	changedTables  []string
	changedEntries []ast.EntryNode
	affected       []string
	findings       []engine.Finding
	annotations    map[string]semantic.Annotation
	entries        map[string]ast.EntryNode
}

func entryMap(entries []ast.EntryNode) map[string]ast.EntryNode {
	m := make(map[string]ast.EntryNode, len(entries))
	for _, e := range entries {
		m[e.ID] = e
	}
	return m
}

// renderDiff 输出 diff 影响报告。
func renderDiff(rep diffReport, format string) {
	switch format {
	case "github":
		if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
			if err := appendFile(path, impactMarkdown(rep)); err != nil {
				log.Printf("warning: write job summary: %v", err)
			}
		}
		for _, a := range report.Annotations(rep.findings) {
			fmt.Println(a)
		}
		printImpactText(rep)
	case "markdown":
		fmt.Print(impactMarkdown(rep))
	case "json":
		data, err := diffJSON(rep)
		if err != nil {
			log.Printf("warning: render json: %v", err)
			printImpactText(rep)
			return
		}
		fmt.Println(string(data))
	default:
		printImpactText(rep)
	}
}

func diffJSON(rep diffReport) ([]byte, error) {
	payload := struct {
		ChangedFunctions []string             `json:"changed_functions"`
		ChangedTables    []string             `json:"changed_tables"`
		ChangedEntries   []string             `json:"changed_entries"`
		AffectedEntries  []string             `json:"affected_entries"`
		Findings         []report.JSONFinding `json:"findings"`
	}{
		ChangedFunctions: changedFuncNames(rep.changedFuncs),
		ChangedTables:    rep.changedTables,
		ChangedEntries:   changedEntryIDs(rep.changedEntries),
		AffectedEntries:  rep.affected,
		Findings:         report.ToJSONFindings(rep.findings),
	}
	return json.MarshalIndent(payload, "", "  ")
}

func changedFuncNames(funcs []ast.FuncSummary) []string {
	out := make([]string, 0, len(funcs))
	for _, f := range funcs {
		out = append(out, f.Symbol.Name)
	}
	return out
}

func changedEntryIDs(entries []ast.EntryNode) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

func printImpactText(rep diffReport) {
	log.Printf("diff impact: %d function(s), %d table(s), %d route registration(s) changed; %d affected entry(ies)",
		len(rep.changedFuncs), len(rep.changedTables), len(rep.changedEntries), len(rep.affected))
	for _, f := range rep.changedFuncs {
		log.Printf("  changed function: %s (%s:%d)", f.Symbol.Name, f.File, f.StartLine)
	}
	for _, t := range rep.changedTables {
		line := "  changed table: " + t
		if a, ok := rep.annotations["table:"+t]; ok && a.Text != "" {
			line += " — " + a.Text
		}
		log.Print(line)
	}
	for _, e := range rep.changedEntries {
		log.Printf("  changed entry: %s", e.ID)
	}
	for _, e := range rep.affected {
		line := "  affected entry: " + e
		if a, ok := rep.annotations["entry:"+e]; ok && a.Text != "" {
			line += " — " + a.Text
		}
		log.Print(line)
	}
	printFindings(rep.findings)
}

func impactMarkdown(rep diffReport) string {
	var b strings.Builder
	b.WriteString("## Topolink — Diff Impact\n\n")

	if len(rep.changedFuncs) == 0 && len(rep.changedTables) == 0 && len(rep.changedEntries) == 0 {
		b.WriteString("No changed functions, tables, or route registrations detected in this diff.\n\n")
	} else {
		if len(rep.changedFuncs) > 0 {
			b.WriteString("**Changed functions**\n\n")
			for _, f := range rep.changedFuncs {
				fmt.Fprintf(&b, "- `%s` (%s:%d)\n", f.Symbol.Name, f.File, f.StartLine)
			}
			b.WriteString("\n")
		}
		if len(rep.changedTables) > 0 {
			b.WriteString("**Changed tables**\n\n")
			for _, t := range rep.changedTables {
				fmt.Fprintf(&b, "- `%s`", t)
				if a, ok := rep.annotations["table:"+t]; ok && a.Text != "" {
					fmt.Fprintf(&b, " — %s", a.Text)
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
		if len(rep.changedEntries) > 0 {
			b.WriteString("**Changed route registrations**\n\n")
			for _, e := range rep.changedEntries {
				fmt.Fprintf(&b, "- `%s`\n", e.ID)
			}
			b.WriteString("\n")
		}
		b.WriteString("**Affected entry points**\n\n")
		if len(rep.affected) == 0 {
			b.WriteString("- (none)\n")
		}
		for _, e := range rep.affected {
			fmt.Fprintf(&b, "- `%s`", e)
			if a, ok := rep.annotations["entry:"+e]; ok && a.Text != "" {
				fmt.Fprintf(&b, " — %s", a.Text)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")

		// 受影响入口的防护语义（来自中间件标注）
		var prot strings.Builder
		for _, id := range rep.affected {
			e, ok := rep.entries[id]
			if !ok || len(e.Middleware) == 0 {
				continue
			}
			header := "- `" + id + "`\n"
			var lines []string
			for _, mw := range e.Middleware {
				name := ast.MiddlewareName(mw)
				a, ok := rep.annotations["middleware:"+name]
				if !ok || a.Text == "" {
					continue
				}
				lines = append(lines, fmt.Sprintf("  - `%s`: %s\n", name, a.Text))
				for _, ef := range a.Effects {
					lines = append(lines, fmt.Sprintf("    - if %s → %s (%s)\n", ef.Condition, ef.Action, ef.Business))
				}
			}
			if len(lines) > 0 {
				prot.WriteString(header)
				for _, l := range lines {
					prot.WriteString(l)
				}
			}
		}
		if prot.Len() > 0 {
			b.WriteString("**Protection of affected entries**\n\n")
			b.WriteString(prot.String())
			b.WriteString("\n")
		}
	}

	b.WriteString(report.Markdown(rep.findings))
	return b.String()
}
