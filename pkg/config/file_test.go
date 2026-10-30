package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".pr-risk.yml")
	data := `rules:
  disable: [R8, money-write]
ignore:
  paths: ["internal/gen/**", "**/*.pb.go"]
public_paths: [webhook]
tables:
  sensitive: [ledger, wallet]
  ignore: [tmp_]
fail_on: high
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Rules.Disable) != 2 || f.Rules.Disable[0] != "R8" {
		t.Errorf("rules.disable = %v", f.Rules.Disable)
	}
	if len(f.Ignore.Paths) != 2 {
		t.Errorf("ignore.paths = %v", f.Ignore.Paths)
	}
	if len(f.PublicPaths) != 1 || f.PublicPaths[0] != "webhook" {
		t.Errorf("public_paths = %v", f.PublicPaths)
	}
	if f.FailOn != "high" {
		t.Errorf("fail_on = %q", f.FailOn)
	}
	if len(f.Tables.Sensitive) != 2 || f.Tables.Ignore[0] != "tmp_" {
		t.Errorf("tables = %+v", f.Tables)
	}
}

func TestLoadFileMissing(t *testing.T) {
	f, err := LoadFile(filepath.Join(t.TempDir(), "nope.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Rules.Disable) != 0 || f.FailOn != "" {
		t.Errorf("missing file should yield empty config, got %+v", f)
	}
}

func TestMatchPath(t *testing.T) {
	patterns := []string{"internal/gen/**", "**/*.pb.go", "mock_*.go", "vendor"}

	yes := []string{
		"internal/gen/foo.go",
		"internal/gen",
		"a/b/order.pb.go",
		"order.pb.go",
		"mock_db.go",
		"vendor/lib.go",
	}
	for _, p := range yes {
		if !MatchPath(p, patterns) {
			t.Errorf("MatchPath(%q) = false, want true", p)
		}
	}

	no := []string{"internal/app/foo.go", "order.go", "cmd/main.go"}
	for _, p := range no {
		if MatchPath(p, patterns) {
			t.Errorf("MatchPath(%q) = true, want false", p)
		}
	}
}
