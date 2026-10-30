package baseline

import (
	"testing"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/llm"
)

func TestFingerprintAndFilter(t *testing.T) {
	known := engine.Finding{Rule: "R1", Level: llm.RiskMedium, EntryID: "http:GET:/orders", Loc: ast.Loc{File: "/a/b/handler.go", Line: 19}}
	other := engine.Finding{Rule: "R5", Level: llm.RiskFatal, EntryID: "http:DELETE:/orders", Loc: ast.Loc{File: "/a/b/handler.go", Line: 39}}

	b := FromFindings([]engine.Finding{known})
	if len(b.Findings) != 1 {
		t.Fatalf("baseline findings = %d, want 1", len(b.Findings))
	}

	kept, suppressed := Filter([]engine.Finding{known, other}, b)
	if suppressed != 1 {
		t.Errorf("suppressed = %d, want 1", suppressed)
	}
	if len(kept) != 1 || kept[0].Rule != "R5" {
		t.Errorf("kept = %+v, want only R5", kept)
	}
}

func TestFingerprintIsPathIndependent(t *testing.T) {
	a := engine.Finding{Rule: "R1", EntryID: "e", Loc: ast.Loc{File: "/x/handler.go", Line: 5}}
	b := engine.Finding{Rule: "R1", EntryID: "e", Loc: ast.Loc{File: "/y/z/handler.go", Line: 5}}
	if Fingerprint(a) != Fingerprint(b) {
		t.Errorf("fingerprint should depend on base name only: %q vs %q", Fingerprint(a), Fingerprint(b))
	}
}
