// Package scan turns one or more repositories into the shared Topolink index.
//
// It is the reusable pipeline behind the open-source CLI and is also consumed
// by the commercial adapter (topolink-pro).
package scan

import (
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"strings"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/config"
	"github.com/topolink/topolink/pkg/index"
	"github.com/topolink/topolink/pkg/lang"
	"github.com/topolink/topolink/pkg/profile"
)

// Options controls scanning.
type Options struct {
	Ignore       []string
	ProfilePaths []string
	ProfileOut   string
}

// extraction is the aggregated result across repositories.
type extraction struct {
	results  []ast.Result
	entries  []ast.EntryNode
	calls    []ast.CallNode
	ops      []ast.DBOp
	funcs    []ast.FuncSummary
	troubles []ast.Trouble
	files    int
}

// Repos extracts, loads profiles and builds one index across all roots.
func Repos(roots []string, opts Options) (*index.Index, error) {
	ex, err := extract(roots, opts.Ignore)
	if err != nil {
		return nil, err
	}

	if opts.ProfileOut != "" {
		p := profile.Build(ex.funcs, ex.calls, ex.ops)
		if err := profile.Save(opts.ProfileOut, p); err != nil {
			return nil, err
		}
		log.Printf("profile written: %s (%d funcs, %d calls, %d ops)", opts.ProfileOut, len(p.Funcs), len(p.Calls), len(p.Ops))
	}

	for _, path := range opts.ProfilePaths {
		p, err := profile.Load(path)
		if err != nil {
			return nil, err
		}
		ex.funcs = append(ex.funcs, p.Funcs...)
		ex.calls = append(ex.calls, p.Calls...)
		ex.ops = append(ex.ops, p.Ops...)
		log.Printf("profile loaded: %s (%d funcs, %d calls, %d ops)", path, len(p.Funcs), len(p.Calls), len(p.Ops))
	}

	idx := index.Build(ex.entries, ex.calls, ex.ops, ex.funcs, ex.troubles)
	log.Printf("scanned %d files: %d entries, %d calls, %d db-ops",
		ex.files, len(ex.entries), len(ex.calls), len(ex.ops))
	return idx, nil
}

// extract aggregates the ecosystem rules and AST results across repositories.
func extract(repos []string, ignore []string) (*extraction, error) {
	ruleSet := make(map[string]bool)
	var rules []string
	for _, repo := range repos {
		rs, err := config.Sniff(repo)
		if err != nil {
			return nil, fmt.Errorf("sniff %s: %w", repo, err)
		}
		for _, r := range rs {
			if !ruleSet[r] {
				ruleSet[r] = true
				rules = append(rules, r)
			}
		}
	}
	log.Printf("detected ecosystem rules: %v", rules)

	qs, err := ast.LoadQuerySet(lang.GoLanguage(), rules)
	if err != nil {
		return nil, fmt.Errorf("load queries: %w", err)
	}
	defer qs.Close()

	reg := lang.New()
	ex := &extraction{}
	for _, repo := range repos {
		if err := walkRepo(repo, reg, qs, ex, ignore); err != nil {
			return nil, err
		}
	}

	// Cross-file route state: same-file ordering, cross-file global inclusion.
	ast.ResolveMiddleware(ex.results)
	for _, res := range ex.results {
		ex.entries = append(ex.entries, res.Entries...)
		ex.calls = append(ex.calls, res.Calls...)
		ex.ops = append(ex.ops, res.Ops...)
		ex.funcs = append(ex.funcs, res.Funcs...)
		ex.troubles = append(ex.troubles, res.Troubles...)
		ex.files++
	}
	return ex, nil
}

// walkRepo walks one repository's .go files and appends the results.
func walkRepo(repo string, reg *lang.Registry, qs *ast.QuerySet, ex *extraction, ignore []string) error {
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
		}
		if len(ignore) > 0 {
			rel, relErr := filepath.Rel(repo, p)
			if relErr == nil && config.MatchPath(rel, ignore) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}

		tree, src, err := reg.ParseFile(p)
		if err != nil {
			return err
		}
		res := qs.Extract(p, tree, src)
		tree.Close()

		ex.results = append(ex.results, res)
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", repo, err)
	}
	return nil
}
