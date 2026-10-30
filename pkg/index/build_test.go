package index

import (
	"testing"

	"github.com/topolink/topolink/pkg/ast"
)

func TestBuildLinksOpsToEntry(t *testing.T) {
	entries := []ast.EntryNode{
		{ID: "http:GET:/api/orders", Kind: ast.EntryHTTP, Name: "/api/orders", Handler: ast.Symbol{Name: "GetOrders"}},
	}
	// GetOrders 调用 helper，helper 内含 DB 操作 —— op 应经 BFS 关联回入口
	calls := []ast.CallNode{
		{Caller: ast.Symbol{Name: "GetOrders"}, Callee: ast.Symbol{Name: "helper"}, Kind: ast.CallFunc},
	}
	ops := []ast.DBOp{
		{Caller: ast.Symbol{Name: "helper"}, Op: ast.OpRead, Resource: "orders"},
	}

	idx := Build(entries, calls, ops, nil, nil)

	if len(idx.Ops) != 1 {
		t.Fatalf("want 1 op, got %d", len(idx.Ops))
	}
	if got := idx.Ops[0].EntryID; got != "http:GET:/api/orders" {
		t.Errorf("op entry = %q, want http:GET:/api/orders", got)
	}

	key := ast.ResourceKey{Kind: ast.ResourceTable, Name: "orders"}
	if len(idx.Reverse[key]) != 1 {
		t.Errorf("want 1 reverse ref for orders, got %d", len(idx.Reverse[key]))
	}
}
