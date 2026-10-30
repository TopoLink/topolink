package engine

import (
	"testing"

	"github.com/topolink/topolink/pkg/ast"
)

func TestEvaluateDBRulesImplicitWhere(t *testing.T) {
	cases := []struct {
		name   string
		op     ast.DBOp
		wantR5 bool
	}{
		{
			name:   "model instance updates has implicit primary key where",
			op:     ast.DBOp{Op: ast.OpWrite, Method: "Updates", ImplicitWhere: true},
			wantR5: false,
		},
		{
			name:   "non-literal where is still a where",
			op:     ast.DBOp{Op: ast.OpWrite, Method: "Delete", HasWhere: true},
			wantR5: false,
		},
		{
			name:   "zero-value model update can be full table",
			op:     ast.DBOp{Op: ast.OpWrite, Method: "Update"},
			wantR5: true,
		},
		{
			name:   "delete without where",
			op:     ast.DBOp{Op: ast.OpDelete, Method: "Delete"},
			wantR5: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateDBRules("e1", tc.op, execCtx{}, nil, Policy{})
			found := false
			for _, f := range got {
				if f.Rule == "R5" {
					found = true
				}
			}
			if found != tc.wantR5 {
				t.Errorf("R5 = %v, want %v (findings: %+v)", found, tc.wantR5, got)
			}
		})
	}
}
