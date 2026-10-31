package report

import (
	"encoding/json"
	"testing"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/llm"
)

func TestJSON(t *testing.T) {
	findings := []engine.Finding{{
		Rule:    "R5",
		Level:   llm.RiskFatal,
		EntryID: "http:POST:/orders",
		Loc:     ast.Loc{File: "orders.go", Line: 42},
		Summary: "write without where",
	}}

	data, err := JSON(findings)
	if err != nil {
		t.Fatal(err)
	}

	var got []JSONFinding
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %+v", got)
	}
	if got[0].Name != "write-without-where" || got[0].Severity != "critical" {
		t.Errorf("unexpected JSON finding: %+v", got[0])
	}
	if got[0].Entry != "http:POST:/orders" || got[0].Line != 42 {
		t.Errorf("unexpected location: %+v", got[0])
	}
}
