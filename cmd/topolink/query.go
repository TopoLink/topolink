package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/index"
)

// parseResourceKey 解析 "kind:name"；无冒号默认按表名处理。
func parseResourceKey(s string) (ast.ResourceKey, error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return ast.ResourceKey{Kind: ast.ResourceTable, Name: s}, nil
	}
	kind := ast.ResourceKind(s[:i])
	name := s[i+1:]
	if name == "" {
		return ast.ResourceKey{}, fmt.Errorf("empty resource name in %q", s)
	}
	switch kind {
	case ast.ResourceTable, ast.ResourceCache, ast.ResourceConfig, ast.ResourceMethod, ast.ResourceTopic:
		return ast.ResourceKey{Kind: kind, Name: name}, nil
	default:
		return ast.ResourceKey{}, fmt.Errorf("unknown resource kind %q (want table/method/key/config/topic)", kind)
	}
}

func printImpact(idx *index.Index, key ast.ResourceKey) {
	refs := idx.Reverse[key]
	if len(refs) == 0 {
		log.Printf("no impact found for %s:%s", key.Kind, key.Name)
		return
	}

	seen := make(map[string]bool, len(refs))
	log.Printf("impact of %s:%s → %d reference(s):", key.Kind, key.Name, len(refs))
	for _, r := range refs {
		var parts []string
		if r.EntryID != "" {
			parts = append(parts, "entry="+r.EntryID)
		} else {
			parts = append(parts, "entry=(unlinked)")
		}
		if r.Op != "" {
			parts = append(parts, "op="+string(r.Op))
		}
		if r.Loc.File != "" {
			parts = append(parts, fmt.Sprintf("%s:%d", r.Loc.File, r.Loc.Line))
		}
		line := "  " + strings.Join(parts, " ")
		if seen[line] {
			continue
		}
		seen[line] = true
		log.Print(line)
	}
}

func printTrouble(idx *index.Index, key string) {
	locs := idx.Trouble[key]
	if len(locs) == 0 {
		log.Printf("no troubleshooting clue found for %q", key)
		return
	}
	log.Printf("trouble clue %q → %d location(s):", key, len(locs))
	for _, loc := range locs {
		if loc.File != "" {
			log.Printf("  %s:%d", loc.File, loc.Line)
		}
	}
}
