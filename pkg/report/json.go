package report

import (
	"encoding/json"

	"github.com/topolink/topolink/pkg/engine"
)

// JSONFinding is the stable JSON schema used by CI and benchmarks.
type JSONFinding struct {
	Rule       string `json:"rule"`
	Name       string `json:"name"`
	Severity   string `json:"severity"`
	Entry      string `json:"entry"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Summary    string `json:"summary"`
	Suggestion string `json:"suggestion"`
}

// ToJSONFindings converts findings to the stable JSON schema, severity-sorted.
func ToJSONFindings(findings []engine.Finding) []JSONFinding {
	out := make([]JSONFinding, 0, len(findings))
	for _, f := range sorted(findings) {
		out = append(out, JSONFinding{
			Rule:       f.Rule,
			Name:       engine.RuleName(f.Rule),
			Severity:   f.Level.Label(),
			Entry:      f.EntryID,
			File:       f.Loc.File,
			Line:       f.Loc.Line,
			Summary:    f.Summary,
			Suggestion: f.Suggestion,
		})
	}
	return out
}

// JSON renders findings as a JSON array.
func JSON(findings []engine.Finding) ([]byte, error) {
	return json.MarshalIndent(ToJSONFindings(findings), "", "  ")
}
