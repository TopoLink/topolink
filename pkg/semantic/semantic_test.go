package semantic

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/topolink/topolink/pkg/ast"
)

type countingEnricher struct{ calls int32 }

func (c *countingEnricher) Annotate(_ context.Context, u Unit) (Annotation, error) {
	atomic.AddInt32(&c.calls, 1)
	return Annotation{Text: "annotated:" + u.ID, Confidence: 0.9}, nil
}

func TestEnrichReusesUnchanged(t *testing.T) {
	units := []Unit{
		newUnit("entry", "entry:http:GET:/orders", "func Handler(){ db.First() }", ast.Loc{File: "a.go", Line: 1}),
		newUnit("table", "table:orders", "orders SELECT ...", ast.Loc{File: "a.go", Line: 9}),
	}

	cache := NewIndex()
	e := &countingEnricher{}

	res, err := Enrich(context.Background(), units, cache, e)
	if err != nil {
		t.Fatal(err)
	}
	if res.Annotated != 2 || res.Reused != 0 {
		t.Fatalf("first: annotated=%d reused=%d, want 2/0", res.Annotated, res.Reused)
	}
	if e.calls != 2 {
		t.Fatalf("first enricher calls = %d, want 2", e.calls)
	}

	// 第二次：内容未变 → 全部复用，不再调用 AI
	e2 := &countingEnricher{}
	res, err = Enrich(context.Background(), units, cache, e2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reused != 2 || res.Annotated != 0 {
		t.Fatalf("second: annotated=%d reused=%d, want 0/2", res.Annotated, res.Reused)
	}
	if e2.calls != 0 {
		t.Fatalf("second enricher calls = %d, want 0", e2.calls)
	}
}

func TestEnrichReannotatesChanged(t *testing.T) {
	cache := NewIndex()
	e := &countingEnricher{}
	units := []Unit{newUnit("entry", "entry:http:GET:/orders", "v1 body", ast.Loc{})}
	if _, err := Enrich(context.Background(), units, cache, e); err != nil {
		t.Fatal(err)
	}

	// 源码变了 → 指纹变化 → 重新标注
	changed := []Unit{newUnit("entry", "entry:http:GET:/orders", "v2 body changed", ast.Loc{})}
	e2 := &countingEnricher{}
	res, err := Enrich(context.Background(), changed, cache, e2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Annotated != 1 || e2.calls != 1 {
		t.Fatalf("changed: annotated=%d calls=%d, want 1/1", res.Annotated, e2.calls)
	}
}

func TestEnrichConcurrent(t *testing.T) {
	var units []Unit
	for i := 0; i < 12; i++ {
		units = append(units, newUnit("entry", "entry:e"+string(rune('a'+i)), "body", ast.Loc{}))
	}
	cache := NewIndex()
	e := &countingEnricher{}

	res, err := EnrichConcurrent(context.Background(), units, cache, e, 4)
	if err != nil {
		t.Fatal(err)
	}
	if res.Annotated != 12 || len(cache.Units) != 12 {
		t.Fatalf("annotated=%d units=%d, want 12/12", res.Annotated, len(cache.Units))
	}

	// 再跑一次：全部复用
	e2 := &countingEnricher{}
	res, err = EnrichConcurrent(context.Background(), units, cache, e2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reused != 12 || e2.calls != 0 {
		t.Fatalf("reused=%d calls=%d, want 12/0", res.Reused, e2.calls)
	}
}

func TestEnrichPrunesRemoved(t *testing.T) {
	cache := NewIndex()
	cache.Units["entry:old"] = Cached{Kind: "entry", Fingerprint: "x"}

	units := []Unit{newUnit("entry", "entry:new", "body", ast.Loc{})}
	if _, err := Enrich(context.Background(), units, cache, &Noop{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Units["entry:old"]; ok {
		t.Error("removed unit should be pruned")
	}
}
