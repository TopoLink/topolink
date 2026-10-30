package ast

import (
	"strings"
	"testing"
)

// A router group passed as a function argument must inherit the prefix and
// middleware of its parent group.
func TestResolveMiddlewareRouterGroupArgument(t *testing.T) {
	helloSrc := `package sample

func setup() {
	r := gin.Default()
	v1 := r.Group("/api")
	v1.Use(AuthMiddleware())
	ArticlesRegister(v1.Group("/articles"))
}
`
	articlesSrc := `package sample

func ArticlesRegister(router *gin.RouterGroup) {
	router.GET("/:slug", ArticleRetrieve)
}
`
	path1, tree1, code1 := parseSample(t, helloSrc)
	defer tree1.Close()
	path2, tree2, code2 := parseSample(t, articlesSrc)
	defer tree2.Close()

	qs := mustQuerySet(t, []string{"go/gin.scm"})
	defer qs.Close()

	results := []Result{qs.Extract(path1, tree1, code1), qs.Extract(path2, tree2, code2)}
	ResolveMiddleware(results)

	if len(results[1].Entries) != 1 {
		t.Fatalf("want 1 entry, got %+v", results[1].Entries)
	}
	e := results[1].Entries[0]
	if e.ID != "http:GET:/api/articles/:slug" {
		t.Errorf("id = %q, want http:GET:/api/articles/:slug", e.ID)
	}
	if !containsStr(e.Middleware, "AuthMiddleware()") {
		t.Errorf("middleware = %v, want AuthMiddleware()", e.Middleware)
	}
}

// Method calls should carry the inferred receiver type so that two methods
// with the same name do not get merged.
func TestCallReceiverTypeInference(t *testing.T) {
	src := `package sample

type A struct{}
type B struct{}

func (a *A) Response() {}
func (b *B) Response() {}

func use() {
	a := A{}
	a.Response()
	b := B{}
	b.Response()
}
`
	path, tree, code := parseSample(t, src)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/gin.scm"})
	defer qs.Close()

	res := qs.Extract(path, tree, code)
	got := map[string]string{}
	for _, c := range res.Calls {
		if strings.HasSuffix(c.Callee.Name, "Response") {
			got[c.Callee.Name] = c.Callee.Package
		}
	}
	if got["a.Response"] != "A" {
		t.Errorf("a.Response receiver = %q, want A", got["a.Response"])
	}
	if got["b.Response"] != "B" {
		t.Errorf("b.Response receiver = %q, want B", got["b.Response"])
	}
}

// Trailing slashes and empty paths are normalized so duplicate registrations
// collapse into one entry.
func TestRoutePathNormalizationAndDedup(t *testing.T) {
	src := `package sample

func setup() {
	r := gin.Default()
	r.POST("/orders", CreateOrder)
	r.POST("/orders/", CreateOrder)
	r.POST("", CreateOrder)
}
`
	path, tree, code := parseSample(t, src)
	defer tree.Close()

	qs := mustQuerySet(t, []string{"go/gin.scm"})
	defer qs.Close()

	results := []Result{qs.Extract(path, tree, code)}
	ResolveMiddleware(results)

	seen := map[string]bool{}
	for _, e := range results[0].Entries {
		if seen[e.ID] {
			t.Errorf("duplicate entry after normalization: %s", e.ID)
		}
		seen[e.ID] = true
		if strings.HasSuffix(e.ID, "/") && e.ID != "http:POST:/" {
			t.Errorf("trailing slash not normalized: %s", e.ID)
		}
	}
}
