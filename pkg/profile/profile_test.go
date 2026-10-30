package profile

import (
	"testing"

	"github.com/topolink/topolink/pkg/ast"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	path := t.TempDir() + "/sub/profile.json"

	p := Build(
		[]ast.FuncSummary{{Symbol: ast.Symbol{Name: "QueryOrders"}}},
		[]ast.CallNode{{Caller: ast.Symbol{Name: "A"}, Callee: ast.Symbol{Name: "QueryOrders"}}},
		[]ast.DBOp{{Resource: "orders"}},
	)
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Funcs) != 1 || got.Funcs[0].Symbol.Name != "QueryOrders" {
		t.Errorf("funcs = %+v", got.Funcs)
	}
	if len(got.Ops) != 1 || got.Ops[0].Resource != "orders" {
		t.Errorf("ops = %+v", got.Ops)
	}
}
