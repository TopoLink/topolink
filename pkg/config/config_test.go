package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSniffWithGinAndGorm(t *testing.T) {
	dir := t.TempDir()
	mod := "module example.com/x\n\ngo 1.21\n\nrequire (\n\tgithub.com/gin-gonic/gin v1.9.1\n\tgorm.io/gorm v1.25.0\n)\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}

	rules, err := Sniff(dir)
	if err != nil {
		t.Fatal(err)
	}

	have := map[string]bool{}
	for _, r := range rules {
		have[r] = true
	}
	for _, want := range []string{"go/nethttp.scm", "go/gin.scm", "go/gorm.scm"} {
		if !have[want] {
			t.Errorf("missing rule %q in %v", want, rules)
		}
	}
}

func TestSniffNoGoMod(t *testing.T) {
	dir := t.TempDir()
	rules, err := Sniff(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0] != "go/nethttp.scm" {
		t.Errorf("rules = %v, want [go/nethttp.scm]", rules)
	}
}
