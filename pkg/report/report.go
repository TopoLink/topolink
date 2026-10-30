// Package report 把 findings 渲染成适合 CI 的格式（Markdown / GitHub Annotations）。
package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/llm"
)

// rank 返回等级排序权重（越小越严重）：S0=0, S1=1, S2=2, S3=3。
func rank(l llm.RiskLevel) int {
	switch l {
	case llm.RiskFatal:
		return 0
	case llm.RiskHigh:
		return 1
	case llm.RiskMedium:
		return 2
	default:
		return 3
	}
}

func sorted(findings []engine.Finding) []engine.Finding {
	out := append([]engine.Finding(nil), findings...)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].Level) < rank(out[j].Level) })
	return out
}

// Markdown 生成 Markdown 报告（用于 PR 评论 / job summary）。
func Markdown(findings []engine.Finding) string {
	var b strings.Builder
	b.WriteString("## Topolink — Code Blast Radius Report\n\n")

	if len(findings) == 0 {
		b.WriteString("No L1 findings. ✅\n")
		return b.String()
	}

	counts := map[llm.RiskLevel]int{}
	for _, f := range findings {
		counts[f.Level]++
	}
	fmt.Fprintf(&b, "**%d finding(s)** — S0:%d · S1:%d · S2:%d · S3:%d\n\n",
		len(findings), counts[llm.RiskFatal], counts[llm.RiskHigh], counts[llm.RiskMedium], counts[llm.RiskLow])

	b.WriteString("| Rule | Severity | Entry | Location | Summary |\n")
	b.WriteString("|------|----------|-------|----------|---------|\n")
	for _, f := range sorted(findings) {
		loc := "—"
		if f.Loc.File != "" {
			loc = fmt.Sprintf("`%s:%d`", f.Loc.File, f.Loc.Line)
		}
		fmt.Fprintf(&b, "| `%s` | **%s** | `%s` | %s | %s |\n", engine.RuleName(f.Rule), f.Level.Label(), f.EntryID, loc, f.Summary)
	}
	return b.String()
}

// Annotations 生成 GitHub Actions workflow 命令（::error / ::warning）。
// S0/S1 用 error，S2/S3 用 warning；带 file/line 便于在 PR 变更视图定位。
func Annotations(findings []engine.Finding) []string {
	var out []string
	for _, f := range sorted(findings) {
		cmd := "warning"
		if rank(f.Level) <= 1 {
			cmd = "error"
		}
		loc := ""
		if f.Loc.File != "" {
			loc = fmt.Sprintf(" file=%s,line=%d", f.Loc.File, f.Loc.Line)
		}
		out = append(out, fmt.Sprintf("::%s%s::[%s · %s] %s (entry=%s)", cmd, loc, engine.RuleName(f.Rule), f.Level.Label(), f.Summary, f.EntryID))
	}
	return out
}

// ParseFailOn 解析失败阈值，返回允许失败的最大等级权重（-1 表示 none）。
func ParseFailOn(s string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none":
		return -1, true
	case "critical", "s0":
		return 0, true
	case "high", "s1":
		return 1, true
	case "medium", "s2":
		return 2, true
	case "low", "s3":
		return 3, true
	}
	return 0, false
}

// ShouldFail 判断是否达到失败阈值。
// 支持语义名（none/critical/high/medium/low）与别名（s0/s1/s2/s3）。
func ShouldFail(findings []engine.Finding, failOn string) (bool, error) {
	threshold, ok := ParseFailOn(failOn)
	if !ok {
		return false, fmt.Errorf("invalid fail-on %q (want none|critical|high|medium|low)", failOn)
	}
	if threshold < 0 {
		return false, nil
	}
	for _, f := range findings {
		if rank(f.Level) <= threshold {
			return true, nil
		}
	}
	return false, nil
}
