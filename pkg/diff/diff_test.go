package diff

import (
	"strings"
	"testing"
)

func TestParseUnifiedDiff(t *testing.T) {
	const sample = `diff --git a/handler.go b/handler.go
index 111..222 100644
--- a/handler.go
+++ b/handler.go
@@ -5,7 +5,9 @@ func Handler() {
 	line5
 	line6
-	old
+	changed7
+	changed8
 	line9
 }
diff --git a/repo.go b/repo.go
--- a/repo.go
+++ b/repo.go
@@ -8,3 +8,4 @@ func FindByID() {
 	a
+	added11
 	b
`
	changes, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("want 2 files, got %d: %+v", len(changes), changes)
	}

	if changes[0].Path != "handler.go" || !equalInts(changes[0].Lines, []int{7, 8}) {
		t.Errorf("handler.go = %+v, want lines [7 8]", changes[0])
	}
	// hunk 从新侧第 8 行开始：a(8) 是上下文，added11 是新侧第 9 行
	if changes[1].Path != "repo.go" || !equalInts(changes[1].Lines, []int{9}) {
		t.Errorf("repo.go = %+v, want lines [9]", changes[1])
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
