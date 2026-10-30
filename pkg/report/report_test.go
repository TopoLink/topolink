package report

import (
	"strings"
	"testing"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/llm"
)

func TestMarkdownAndAnnotations(t *testing.T) {
	findings := []engine.Finding{
		{Rule: "R5", Level: llm.RiskFatal, EntryID: "http:GET:/x", Loc: ast.Loc{File: "a.go", Line: 10}, Summary: "full delete"},
		{Rule: "R1", Level: llm.RiskMedium, EntryID: "http:GET:/y", Loc: ast.Loc{File: "b.go", Line: 20}, Summary: "n+1"},
	}

	md := Markdown(findings)
	if !strings.Contains(md, "Code Blast Radius Report") || !strings.Contains(md, "write-without-where") {
		t.Errorf("markdown missing content:\n%s", md)
	}

	ann := Annotations(findings)
	if len(ann) != 2 {
		t.Fatalf("want 2 annotations, got %d", len(ann))
	}
	// S0 排在前且用 error，带 file/line
	if !strings.Contains(ann[0], "::error") || !strings.Contains(ann[0], "a.go,line=10") {
		t.Errorf("annotation[0] = %q", ann[0])
	}
	// S2 用 warning
	if !strings.Contains(ann[1], "::warning") {
		t.Errorf("annotation[1] = %q", ann[1])
	}
}

func TestShouldFail(t *testing.T) {
	s0 := []engine.Finding{{Rule: "R5", Level: llm.RiskFatal}}
	s2 := []engine.Finding{{Rule: "R1", Level: llm.RiskMedium}}

	cases := []struct {
		findings []engine.Finding
		failOn   string
		want     bool
	}{
		{s0, "s0", true},
		{s0, "critical", true},
		{s0, "s2", true},
		{s2, "s0", false},
		{s2, "critical", false},
		{s2, "s2", true},
		{s2, "medium", true},
		{s0, "none", false},
	}
	for _, tc := range cases {
		got, err := ShouldFail(tc.findings, tc.failOn)
		if err != nil {
			t.Fatalf("ShouldFail(%q) error: %v", tc.failOn, err)
		}
		if got != tc.want {
			t.Errorf("ShouldFail(%q) = %v, want %v", tc.failOn, got, tc.want)
		}
	}

	if _, err := ShouldFail(s0, "bogus"); err == nil {
		t.Error("invalid fail-on should return an error")
	}
}
