package ast

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topolink/topolink/pkg/lang"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

const sampleSrc = `package sample

import "fmt"

func GetOrder(id int) {
	orders := queryOrders(id)
	fmt.Println(orders)
}

func queryOrders(id int) []string {
	return nil
}
`

const ginSrc = `package sample

func setupRouter() {
	r := gin.Default()
	r.GET("/api/orders", GetOrder)
	r.POST("/api/orders", CreateOrder)
}

func GetOrder()    {}
func CreateOrder() {}
`

const grpcSrc = `package sample

func main() {
	s := grpc.NewServer()
	pb.RegisterOrderServer(s, &orderServer{})
}

type orderServer struct{}
`

const gormSrc = `package sample

func repo(db *gorm.DB, o *Order) {
	db.Table("orders").Where("id = ?", 1).First(o)
	db.Create(o)
	db.Delete(o)
}
`

const callbackSrc = `package sample

func withLock(client *redis.Client, fn func() error) error {
	client.SetNX("lock", 1, 0)
	return fn()
}

func Handler(client *redis.Client) {
	withLock(client, func() error {
		http.Get("http://x")
		return nil
	})
}
`

func TestExtractCalls(t *testing.T) {
	path, tree, code := parseSample(t, sampleSrc)
	defer tree.Close()

	qs := mustQuerySet(t, nil)
	defer qs.Close()

	res := qs.Extract(path, tree, code)

	if len(res.Calls) != 2 {
		t.Fatalf("want 2 calls, got %d: %+v", len(res.Calls), res.Calls)
	}
	if got := res.Calls[0].Callee.Name; got != "queryOrders" {
		t.Errorf("calls[0].callee = %q, want queryOrders", got)
	}
	if got := res.Calls[1].Callee.Name; got != "fmt.Println" {
		t.Errorf("calls[1].callee = %q, want fmt.Println", got)
	}
	if got := res.Calls[0].Caller.Name; got != "GetOrder" {
		t.Errorf("calls[0].caller = %q, want GetOrder", got)
	}
}

func TestExtractGinEntries(t *testing.T) {
	path, tree, code := parseSample(t, ginSrc)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/gin.scm"})
	defer qs.Close()

	res := qs.Extract(path, tree, code)

	if len(res.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(res.Entries), res.Entries)
	}
	if got := res.Entries[0].ID; got != "http:GET:/api/orders" {
		t.Errorf("entries[0].ID = %q, want http:GET:/api/orders", got)
	}
	if got := res.Entries[1].ID; got != "http:POST:/api/orders" {
		t.Errorf("entries[1].ID = %q, want http:POST:/api/orders", got)
	}
	if got := res.Entries[0].Handler.Name; got != "GetOrder" {
		t.Errorf("entries[0].handler = %q, want GetOrder", got)
	}
}

func TestExtractGRPCService(t *testing.T) {
	path, tree, code := parseSample(t, grpcSrc)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/grpc.scm"})
	defer qs.Close()

	res := qs.Extract(path, tree, code)

	if len(res.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(res.Entries), res.Entries)
	}
	if got := res.Entries[0].ID; got != "grpc:Order" {
		t.Errorf("entries[0].ID = %q, want grpc:Order", got)
	}
	if got := res.Entries[0].Handler.Name; got != "orderServer" {
		t.Errorf("entries[0].handler = %q, want orderServer", got)
	}
}

func TestExtractGormOps(t *testing.T) {
	path, tree, code := parseSample(t, gormSrc)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/gorm.scm"})
	defer qs.Close()

	res := qs.Extract(path, tree, code)

	if len(res.Ops) == 0 {
		t.Fatal("want db ops, got 0")
	}
	if res.Ops[0].Resource != "orders" {
		t.Errorf("ops[0].resource = %q, want orders", res.Ops[0].Resource)
	}

	var sawWrite, sawDelete bool
	for _, op := range res.Ops {
		switch op.Op {
		case OpWrite:
			sawWrite = true
		case OpDelete:
			sawDelete = true
		}
	}
	if !sawWrite {
		t.Error("want at least one OpWrite (Create)")
	}
	if !sawDelete {
		t.Error("want at least one OpDelete (Delete)")
	}
}

// 验证：闭包内的调用记录了外层包装调用（withLock）。
func TestExtractCallbackOf(t *testing.T) {
	path, tree, code := parseSample(t, callbackSrc)
	defer tree.Close()

	qs := mustQuerySet(t, nil)
	defer qs.Close()

	res := qs.Extract(path, tree, code)

	var httpCall *CallNode
	for i := range res.Calls {
		if res.Calls[i].Callee.Name == "http.Get" {
			httpCall = &res.Calls[i]
		}
	}
	if httpCall == nil {
		t.Fatal("http.Get call not found")
	}
	if len(httpCall.CallbackOf) != 1 || httpCall.CallbackOf[0] != "withLock" {
		t.Errorf("CallbackOf = %v, want [withLock]", httpCall.CallbackOf)
	}

	// 函数摘要：withLock 应被识别为"执行回调 + 有锁"
	var wl *FuncSummary
	for i := range res.Funcs {
		if res.Funcs[i].Symbol.Name == "withLock" {
			wl = &res.Funcs[i]
		}
	}
	if wl == nil {
		t.Fatal("withLock summary not found")
	}
	if !wl.UsesCallback || !wl.HasLock {
		t.Errorf("withLock summary = %+v, want UsesCallback && HasLock", *wl)
	}
}

const manualTxSrc = `package sample

func f(db *gorm.DB) {
	tx := db.Begin()
	http.Post("http://x", "", nil)
	tx.Commit()
	http.Get("http://y")
}
`

func TestExtractManualTx(t *testing.T) {
	path, tree, code := parseSample(t, manualTxSrc)
	defer tree.Close()

	qs := mustQuerySet(t, nil)
	defer qs.Close()

	res := qs.Extract(path, tree, code)

	var post, get *CallNode
	for i := range res.Calls {
		switch res.Calls[i].Callee.Name {
		case "http.Post":
			post = &res.Calls[i]
		case "http.Get":
			get = &res.Calls[i]
		}
	}
	if post == nil || get == nil {
		t.Fatalf("missing calls: post=%v get=%v", post, get)
	}
	if !post.InTx {
		t.Error("http.Post should be InTx (between Begin and Commit)")
	}
	if get.InTx {
		t.Error("http.Get should NOT be InTx (after Commit)")
	}
}

const rawSQLSrc = `package sample

func f(db *gorm.DB) {
	db.Exec("UPDATE orders SET status = 1")
	db.Raw("DELETE FROM orders WHERE id = ?", 1)
}
`

func TestExtractRawSQL(t *testing.T) {
	path, tree, code := parseSample(t, rawSQLSrc)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/gorm.scm"})
	defer qs.Close()

	res := qs.Extract(path, tree, code)

	if len(res.Ops) != 2 {
		t.Fatalf("want 2 ops, got %d: %+v", len(res.Ops), res.Ops)
	}
	if res.Ops[0].Op != OpWrite || res.Ops[0].Method != "Update" {
		t.Errorf("Exec op = %s/%s, want Write/Update", res.Ops[0].Op, res.Ops[0].Method)
	}
	if res.Ops[0].Where != "" {
		t.Errorf("Exec (no WHERE) where = %q, want empty", res.Ops[0].Where)
	}
	if res.Ops[1].Op != OpDelete || res.Ops[1].Method != "Delete" {
		t.Errorf("Raw op = %s/%s, want Delete/Delete", res.Ops[1].Op, res.Ops[1].Method)
	}
	if res.Ops[1].Where == "" {
		t.Error("Raw with WHERE should have non-empty where")
	}
}

const troubleSrc = `package sample

import "errors"

func load() error {
	ctx, _ := context.WithTimeout(context.Background(), 0)
	_ = ctx
	log.Printf("load failed: %v", err)
	return errors.New("order-not-found")
}
`

func TestExtractTroubles(t *testing.T) {
	path, tree, code := parseSample(t, troubleSrc)
	defer tree.Close()

	qs := mustQuerySet(t, nil)
	defer qs.Close()

	res := qs.Extract(path, tree, code)

	var sawErr, sawLog, sawTimeout bool
	for _, tr := range res.Troubles {
		switch tr.Kind {
		case "err":
			if tr.Key == "order-not-found" {
				sawErr = true
			}
		case "log":
			sawLog = true
		case "timeout":
			sawTimeout = true
		}
	}
	if !sawErr {
		t.Errorf("want err:order-not-found, got %+v", res.Troubles)
	}
	if !sawLog {
		t.Errorf("want log clue, got %+v", res.Troubles)
	}
	if !sawTimeout {
		t.Errorf("want timeout clue, got %+v", res.Troubles)
	}
}

const middlewareSrc = `package sample

func setup() {
	r := gin.Default()
	r.Use(AuthMiddleware())
	r.GET("/orders", GetOrders)
	api := r.Group("/api")
	api.Use(RateLimit())
	api.POST("/x", CreateX)
}
`

func TestExtractMiddleware(t *testing.T) {
	path, tree, code := parseSample(t, middlewareSrc)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/gin.scm"})
	defer qs.Close()

	res := qs.Extract(path, tree, code)
	ResolveMiddleware([]Result{res})
	if len(res.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(res.Entries), res.Entries)
	}

	byID := map[string]EntryNode{}
	for _, e := range res.Entries {
		byID[e.ID] = e
	}

	if !containsStr(byID["http:GET:/orders"].Middleware, "AuthMiddleware()") {
		t.Errorf("/orders middleware = %v, want AuthMiddleware()", byID["http:GET:/orders"].Middleware)
	}
	mw := byID["http:POST:/api/x"].Middleware
	if !containsStr(mw, "RateLimit()") || !containsStr(mw, "AuthMiddleware()") {
		t.Errorf("/x middleware = %v, want RateLimit() + AuthMiddleware()", mw)
	}
}

func TestResolveMiddlewareCrossFile(t *testing.T) {
	routerSrc := `package sample

func setup() {
	r := gin.Default()
	api := r.Group("/api")
	api.GET("/orders", GetOrders)
}
`
	mwSrc := `package sample

func register() {
	api.Use(AuthMiddleware())
}
`
	path1, tree1, code1 := parseSample(t, routerSrc)
	defer tree1.Close()
	path2, tree2, code2 := parseSample(t, mwSrc)
	defer tree2.Close()

	qs := mustQuerySet(t, []string{"go/gin.scm"})
	defer qs.Close()

	res1 := qs.Extract(path1, tree1, code1)
	res2 := qs.Extract(path2, tree2, code2)
	ResolveMiddleware([]Result{res1, res2})

	var got []string
	for _, e := range res1.Entries {
		if e.ID == "http:GET:/api/orders" {
			got = e.Middleware
		}
	}
	if !containsStr(got, "AuthMiddleware()") {
		t.Errorf("cross-file middleware = %v, want AuthMiddleware()", got)
	}
}

func TestExtractGinMethodHandler(t *testing.T) {
	src := `package sample

func setup(h *Handlers) {
	r := gin.Default()
	r.GET("/orders", h.ListOrders)
}
`
	path, tree, code := parseSample(t, src)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/gin.scm"})
	defer qs.Close()

	res := qs.Extract(path, tree, code)
	if len(res.Entries) != 1 {
		t.Fatalf("want 1 entry, got %+v", res.Entries)
	}
	if got := res.Entries[0].Handler.Name; got != "ListOrders" {
		t.Errorf("handler = %q, want ListOrders", got)
	}
}

func TestConsumerLoopNotBusinessLoop(t *testing.T) {
	src := `package sample

import "context"

func (c *Consumer) Consume(ctx context.Context) {
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			return
		}
		c.repo.Save(msg)
	}
}
`
	path, tree, code := parseSample(t, src)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/gorm.scm"})
	defer qs.Close()

	res := qs.Extract(path, tree, code)
	var save *CallNode
	for i := range res.Calls {
		if strings.HasSuffix(res.Calls[i].Callee.Name, "Save") {
			save = &res.Calls[i]
		}
	}
	if save == nil {
		t.Fatalf("Save call not extracted: %+v", res.Calls)
	}
	if save.InLoop {
		t.Errorf("consumer loop should not mark Save as InLoop")
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func parseSample(t *testing.T, src string) (string, *sitter.Tree, []byte) {
	t.Helper()
	path := writeTempGo(t, src)
	reg := lang.New()
	tree, code, err := reg.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, tree, code
}

func mustQuerySet(t *testing.T, rules []string) *QuerySet {
	t.Helper()
	qs, err := LoadQuerySet(lang.GoLanguage(), rules)
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

func writeTempGo(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
