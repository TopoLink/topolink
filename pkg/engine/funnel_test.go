package engine

import (
	"context"
	"testing"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/index"
	"github.com/topolink/topolink/pkg/llm"
	"github.com/topolink/topolink/pkg/meta"
	"github.com/topolink/topolink/pkg/semantic"
)

func TestEvaluateDBRules(t *testing.T) {
	cases := []struct {
		name  string
		op    ast.DBOp
		ctx   execCtx
		rule  string
		level llm.RiskLevel
	}{
		{"R5 delete no where", ast.DBOp{Op: ast.OpDelete, Method: "Delete"}, execCtx{}, "R5", llm.RiskFatal},
		{"R2 loop write", ast.DBOp{Op: ast.OpWrite, Method: "Update", Where: "id=?"}, execCtx{InLoop: true}, "R2", llm.RiskHigh},
		{"R1 loop read", ast.DBOp{Op: ast.OpRead, Method: "First", Where: "id=?"}, execCtx{InLoop: true}, "R1", llm.RiskMedium},
		{"R6 standalone write", ast.DBOp{Op: ast.OpWrite, Method: "Create", Standalone: true}, execCtx{}, "R6", llm.RiskMedium},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateDBRules("e1", tc.op, tc.ctx, nil, Policy{})
			if len(got) == 0 {
				t.Fatalf("want %s, got none", tc.rule)
			}
			if got[0].Rule != tc.rule || got[0].Level != tc.level {
				t.Errorf("got %s/%s, want %s/%s", got[0].Rule, got[0].Level, tc.rule, tc.level)
			}
		})
	}
}

func TestEvaluateDBRulesCreateHasNoR5(t *testing.T) {
	// Create（INSERT）本就没有 WHERE，不应误报 R5
	for _, f := range evaluateDBRules("e1", ast.DBOp{Op: ast.OpWrite, Method: "Create"}, execCtx{}, nil, Policy{}) {
		if f.Rule == "R5" {
			t.Fatalf("Create should not trigger R5: %+v", f)
		}
	}
}

func TestEvaluateMoneyAndCacheRules(t *testing.T) {
	// R9: write to a money-related table
	got := evaluateDBRules("e1", ast.DBOp{Op: ast.OpWrite, Method: "Update", Where: "id=?", Resource: "accounts"}, execCtx{}, nil, Policy{})
	found := false
	for _, f := range got {
		if f.Rule == "R9" {
			found = true
		}
	}
	if !found {
		t.Errorf("want R9 for accounts write, got %+v", got)
	}

	// R10: cache set without TTL
	call := ast.CallNode{
		Kind:   ast.CallRedis,
		Callee: ast.Symbol{Name: "client.Set"},
		Args:   []string{"ctx", `"k"`, "v", "0"},
	}
	got2 := evaluateCallRules("e1", call, execCtx{})
	found = false
	for _, f := range got2 {
		if f.Rule == "R10" {
			found = true
		}
	}
	if !found {
		t.Errorf("want R10 for Set(..., 0), got %+v", got2)
	}
}

func TestRunWeakAuth(t *testing.T) {
	entries := []ast.EntryNode{
		{ID: "http:GET:/weak", Kind: ast.EntryHTTP, Name: "/weak", Middleware: []string{"Auth()"}},
		{ID: "http:GET:/strong", Kind: ast.EntryHTTP, Name: "/strong", Middleware: []string{"Guard()"}},
	}
	idx := index.Build(entries, nil, nil, nil, nil)

	eng := New(&llm.Noop{}, &meta.Noop{}).WithAnnotations(map[string]semantic.Annotation{
		"middleware:Auth": {
			Text:    "auth guard",
			Effects: []semantic.Effect{{Condition: "no token", Action: "log only", Business: "records attempt"}},
		},
		"middleware:Guard": {
			Text:    "auth guard",
			Effects: []semantic.Effect{{Condition: "no token", Action: "abort 401", Business: "rejects anonymous"}},
		},
	})

	findings, err := eng.Run(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}

	var r11 []Finding
	for _, f := range findings {
		if f.Rule == "R11" {
			r11 = append(r11, f)
		}
	}
	if len(r11) != 1 || r11[0].EntryID != "http:GET:/weak" {
		t.Fatalf("R11 = %+v, want only http:GET:/weak", r11)
	}
}

func TestRunMissingAuthWithAnnotation(t *testing.T) {
	entries := []ast.EntryNode{
		{ID: "http:GET:/secure", Kind: ast.EntryHTTP, Name: "/secure", Middleware: []string{"Guard()"}},
	}
	idx := index.Build(entries, nil, nil, nil, nil)
	eng := New(&llm.Noop{}, &meta.Noop{}).WithAnnotations(map[string]semantic.Annotation{
		"middleware:Guard": {Kind: "middleware-purpose", Text: "JWT authentication; aborts with 401 on invalid token", Confidence: 0.9},
	})
	findings, err := eng.Run(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Rule == "R8" {
			t.Fatalf("Guard() should be recognized as auth via annotation, got R8: %+v", f)
		}
	}
}

func TestEvaluateMoneyWithAnnotation(t *testing.T) {
	anns := map[string]semantic.Annotation{
		"table:core_records": {Kind: "data-sensitivity", Text: "financial ledger holding settlement amounts", Confidence: 0.9},
	}
	// 表名本身不像资金表，但 AI 标注为敏感 → R9
	got := evaluateDBRules("e1", ast.DBOp{Op: ast.OpWrite, Method: "Update", Where: "id=?", Resource: "core_records"}, execCtx{}, anns, Policy{})
	found := false
	for _, f := range got {
		if f.Rule == "R9" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want R9 via annotation for core_records, got %+v", got)
	}
}

func TestRunMissingAuth(t *testing.T) {
	entries := []ast.EntryNode{
		{ID: "http:GET:/public", Kind: ast.EntryHTTP, Name: "/public"},
		{ID: "http:GET:/secure", Kind: ast.EntryHTTP, Name: "/secure", Middleware: []string{"AuthMiddleware()"}},
		{ID: "http:GET:/health", Kind: ast.EntryHTTP, Name: "/health"},
	}

	idx := index.Build(entries, nil, nil, nil, nil)
	eng := New(&llm.Noop{}, &meta.Noop{})
	findings, err := eng.Run(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}

	var r8 []Finding
	for _, f := range findings {
		if f.Rule == "R8" {
			r8 = append(r8, f)
		}
	}
	if len(r8) != 1 || r8[0].EntryID != "http:GET:/public" {
		t.Fatalf("R8 = %+v, want only http:GET:/public", r8)
	}
}

func TestEvaluateCallRules(t *testing.T) {
	if got := evaluateCallRules("e1", ast.CallNode{Kind: ast.CallHTTP}, execCtx{InTx: true}); len(got) == 0 || got[0].Rule != "R3" {
		t.Fatalf("want R3, got %+v", got)
	}
	if got := evaluateCallRules("e1", ast.CallNode{Kind: ast.CallGRPC}, execCtx{UnderLock: true}); len(got) == 0 || got[0].Rule != "R4" {
		t.Fatalf("want R4, got %+v", got)
	}
	if got := evaluateCallRules("e1", ast.CallNode{Kind: ast.CallHTTP, NoTimeout: true}, execCtx{}); len(got) == 0 || got[0].Rule != "R7" {
		t.Fatalf("want R7, got %+v", got)
	}
	if got := evaluateCallRules("e1", ast.CallNode{Kind: ast.CallFunc, NoTimeout: true}, execCtx{InTx: true}); len(got) != 0 {
		t.Fatalf("non-remote should yield none, got %+v", got)
	}
}

// 关键场景：封装。GetOrders 在循环里调用 helper，helper 内查库。
// 该查库本地不在循环内，但应通过上下文传播被判为"循环内查库"并关联到入口。
func TestRunWrapperLoopPropagation(t *testing.T) {
	entries := []ast.EntryNode{
		{ID: "http:GET:/api/orders", Kind: ast.EntryHTTP, Handler: ast.Symbol{Name: "GetOrders"}},
	}
	calls := []ast.CallNode{
		{Caller: ast.Symbol{Name: "GetOrders"}, Callee: ast.Symbol{Name: "helper"}, Kind: ast.CallFunc, InLoop: true},
	}
	ops := []ast.DBOp{
		{Caller: ast.Symbol{Name: "helper"}, Op: ast.OpRead, Method: "First", Where: "id=?", Resource: "orders"},
	}

	idx := index.Build(entries, calls, ops, nil, nil)
	eng := New(&llm.Noop{}, &meta.Noop{})
	findings, err := eng.Run(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}

	var r1 *Finding
	for i := range findings {
		if findings[i].Rule == "R1" {
			r1 = &findings[i]
		}
	}
	if r1 == nil {
		t.Fatalf("want R1 via wrapper loop propagation, got %+v", findings)
	}
	if r1.EntryID != "http:GET:/api/orders" {
		t.Errorf("entry = %q, want http:GET:/api/orders", r1.EntryID)
	}
}

// 高阶封装：http.Get 在 withLock 的闭包里 → 应判为持锁远程（R4）。
func TestRunCallbackLockWrapper(t *testing.T) {
	entries := []ast.EntryNode{
		{ID: "http:GET:/h", Kind: ast.EntryHTTP, Handler: ast.Symbol{Name: "Handler"}},
	}
	calls := []ast.CallNode{
		{
			Caller:      ast.Symbol{Name: "Handler"},
			Callee:      ast.Symbol{Name: "http.Get"},
			Kind:        ast.CallHTTP,
			CallbackOf:  []string{"withLock"},
			NoTimeout:   true,
			InGoroutine: false,
		},
	}
	funcs := []ast.FuncSummary{
		{Symbol: ast.Symbol{Name: "withLock"}, CallbackParams: []string{"fn"}, UsesCallback: true, HasLock: true},
	}

	idx := index.Build(entries, calls, nil, funcs, nil)
	eng := New(&llm.Noop{}, &meta.Noop{})
	findings, err := eng.Run(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}

	var r4 *Finding
	for i := range findings {
		if findings[i].Rule == "R4" {
			r4 = &findings[i]
		}
	}
	if r4 == nil {
		t.Fatalf("want R4 via callback lock wrapper, got %+v", findings)
	}
	if r4.EntryID != "http:GET:/h" {
		t.Errorf("entry = %q, want http:GET:/h", r4.EntryID)
	}
}

// 方法调用解析：Handler 调 service.load()（方法调用），load 定义里查库。
// 应通过 last-segment 解析接上，并继承循环上下文 → R1。
func TestRunMethodCallResolution(t *testing.T) {
	entries := []ast.EntryNode{
		{ID: "http:GET:/x", Kind: ast.EntryHTTP, Handler: ast.Symbol{Name: "Handler"}},
	}
	calls := []ast.CallNode{
		{Caller: ast.Symbol{Name: "Handler"}, Callee: ast.Symbol{Name: "service.load"}, Kind: ast.CallFunc, InLoop: true},
	}
	ops := []ast.DBOp{
		{Caller: ast.Symbol{Name: "load"}, Op: ast.OpRead, Method: "First", Where: "id=?"},
	}

	idx := index.Build(entries, calls, ops, nil, nil)
	eng := New(&llm.Noop{}, &meta.Noop{})
	findings, err := eng.Run(context.Background(), idx)
	if err != nil {
		t.Fatal(err)
	}

	var r1 *Finding
	for i := range findings {
		if findings[i].Rule == "R1" {
			r1 = &findings[i]
		}
	}
	if r1 == nil {
		t.Fatalf("want R1 via method-call resolution, got %+v", findings)
	}
	if r1.EntryID != "http:GET:/x" {
		t.Errorf("entry = %q, want http:GET:/x", r1.EntryID)
	}
}
