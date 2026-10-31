// Package main 是 Phase 1 CLI 的唯一入口。
// 设计铁律：业务逻辑严禁 panic，错误统一用 fmt.Errorf("%w") 上抛，
// 仅在此处统一捕捉并优雅输出。
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/topolink/topolink/pkg/baseline"
	"github.com/topolink/topolink/pkg/config"
	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/impact"
	"github.com/topolink/topolink/pkg/llm"
	"github.com/topolink/topolink/pkg/meta"
	"github.com/topolink/topolink/pkg/report"
	"github.com/topolink/topolink/pkg/semantic"
)

// stringList 支持重复参数（-repo / -profile）。
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// toSet 把字符串列表转成小写集合（用于规则/路径/表名匹配）。
func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]bool, len(items))
	for _, it := range items {
		if s := strings.ToLower(strings.TrimSpace(it)); s != "" {
			m[s] = true
		}
	}
	return m
}

func main() {
	log.SetPrefix("topolink: ")
	log.SetFlags(0)

	var repos, profilePaths stringList
	var profileOut, impactKey, troubleKey, format, failOn, diffRange, diffFile, baselinePath, baselineOut, semanticPath, aiBase, aiModel, aiKey, configPath string
	var aiConcurrency int
	flag.Var(&repos, "repo", "repository root to analyze (repeatable)")
	flag.Var(&profilePaths, "profile", "library profile to load (repeatable)")
	flag.StringVar(&profileOut, "profile-out", "", "write a library profile to this path after extraction")
	flag.StringVar(&impactKey, "impact", "", "reverse-impact query, e.g. table:orders / method:GetUser / key:cache:user")
	flag.StringVar(&troubleKey, "trouble", "", "troubleshooting lookup, e.g. err:order-not-found / log:load failed / timeout:context.WithTimeout")
	flag.StringVar(&format, "format", "text", "output format: text|markdown|github|json")
	flag.StringVar(&failOn, "fail-on", "none", "exit non-zero when findings reach severity: none|critical|high|medium|low (aliases: s0/s1/s2/s3)")
	flag.StringVar(&diffRange, "diff", "", "blast-radius mode: 'auto' (PR base...HEAD), a git range, or empty for a full scan")
	flag.StringVar(&diffFile, "diff-file", "", "analyze only the blast radius of a unified diff file")
	flag.StringVar(&baselinePath, "baseline", "", "suppress findings recorded in this baseline file (report only new risk)")
	flag.StringVar(&baselineOut, "baseline-out", "", "write the current findings as a baseline file")
	flag.StringVar(&semanticPath, "semantic", "", "semantic index path: build/update AI annotations, reusing unchanged units by content fingerprint")
	flag.StringVar(&aiBase, "ai-base", "", "LLM base URL (default $OPENAI_BASE_URL or https://api.openai.com/v1)")
	flag.StringVar(&aiModel, "ai-model", "", "LLM model (default $OPENAI_MODEL or gpt-4o-mini)")
	flag.StringVar(&aiKey, "ai-key", "", "LLM API key (default $OPENAI_API_KEY); empty disables AI enrichment")
	flag.IntVar(&aiConcurrency, "ai-concurrency", 1, "max concurrent LLM calls during semantic indexing")
	flag.StringVar(&configPath, "config", ".pr-risk.yml", "policy file: rule toggles, ignore paths, public paths, table keywords")
	flag.Parse()

	// 记录显式传入的 flag，避免配置覆盖用户命令行。
	setFlags := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	if cfg.FailOn != "" && !setFlags["fail-on"] {
		failOn = cfg.FailOn
	}

	if diffRange == "auto" {
		diffRange = autoDiffRange(os.Getenv("GITHUB_EVENT_NAME"), os.Getenv("GITHUB_BASE_REF"))
	}

	if len(repos) == 0 {
		repos = []string{"."}
	}

	idx, err := analyze(repos, profilePaths, profileOut, cfg.Ignore.Paths)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	// 语义索引：按内容指纹增量标注（未变更单元复用缓存，不重复调用 AI）
	var annotations map[string]semantic.Annotation
	if semanticPath != "" {
		cache, err := semantic.LoadOrNew(semanticPath)
		if err != nil {
			log.Fatalf("error: %v", err)
		}

		key := aiKey
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		base := aiBase
		if base == "" {
			base = os.Getenv("OPENAI_BASE_URL")
		}
		model := aiModel
		if model == "" {
			model = os.Getenv("OPENAI_MODEL")
		}
		if model == "" {
			model = "gpt-4o-mini"
		}

		var enricher semantic.Enricher
		if key != "" {
			enricher = semantic.NewOpenAIEnricher(base, key, model)
			log.Printf("AI enrichment enabled (model=%s)", model)
		} else {
			enricher = semantic.Noop{}
			log.Printf("AI enrichment disabled (no API key); recording fingerprints only")
		}

		units := semantic.BuildUnits(idx.Entries, idx.Funcs, idx.Ops)
		res, err := semantic.EnrichConcurrent(context.Background(), units, cache, enricher, aiConcurrency)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		if err := semantic.Save(semanticPath, cache); err != nil {
			log.Fatalf("error: %v", err)
		}
		log.Printf("semantic index: %d units (%d reused, %d annotated) → %s", res.Total, res.Reused, res.Annotated, semanticPath)

		annotations = make(map[string]semantic.Annotation, len(cache.Units))
		for id, c := range cache.Units {
			annotations[id] = c.Annotation
		}

		shown := 0
		for _, u := range units {
			if c, ok := cache.Units[u.ID]; ok && c.Annotation.Text != "" && shown < 5 {
				log.Printf("  [%s] %s", u.ID, c.Annotation.Text)
				for _, ef := range c.Annotation.Effects {
					log.Printf("      · if %s → %s (%s)", ef.Condition, ef.Action, ef.Business)
				}
				shown++
			}
		}
	}

	// 查询模式：输出反向影响
	if impactKey != "" {
		key, err := parseResourceKey(impactKey)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		printImpact(idx, key)
		return
	}

	// 排查模式：按错误码/日志模板/超时定位源码
	if troubleKey != "" {
		printTrouble(idx, troubleKey)
		return
	}

	eng := engine.New(&llm.Noop{}, &meta.Noop{})
	if annotations != nil {
		eng = eng.WithAnnotations(annotations)
	}
	eng = eng.WithPolicy(engine.Policy{
		DisabledRules:   toSet(cfg.Rules.Disable),
		PublicPaths:     toSet(cfg.PublicPaths),
		SensitiveTables: cfg.Tables.Sensitive,
		IgnoredTables:   cfg.Tables.Ignore,
	})
	findings, err := eng.Run(context.Background(), idx)
	if err != nil {
		log.Fatalf("error: run engine: %v", err)
	}

	// baseline：载入已接受集合 / 导出当前集合
	var bl *baseline.File
	if baselinePath != "" {
		bl, err = baseline.Load(baselinePath)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
	}
	if baselineOut != "" {
		if err := baseline.Save(baselineOut, baseline.FromFindings(findings)); err != nil {
			log.Fatalf("error: %v", err)
		}
		log.Printf("baseline written: %s (%d findings)", baselineOut, len(findings))
	}

	// diff 模式：只看这次改动的影响面（爆炸半径）
	if diffRange != "" || diffFile != "" {
		changes, err := loadDiff(diffRange, diffFile, repos)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		rep := diffReport{
			changedFuncs:   impact.ChangedFunctions(idx.Funcs, changes),
			changedTables:  impact.ChangedTables(changes),
			changedEntries: impact.ChangedEntries(idx.Entries, changes),
			annotations:    annotations,
			entries:        entryMap(idx.Entries),
		}
		changedIDs := make([]string, 0, len(rep.changedEntries))
		for _, e := range rep.changedEntries {
			changedIDs = append(changedIDs, e.ID)
		}
		rep.affected = impact.MergeEntries(changedIDs, impact.MergeEntries(
			impact.AffectedEntries(idx, rep.changedFuncs),
			impact.AffectedEntriesByTables(idx, rep.changedTables),
		))
		rep.findings = impact.FilterFindings(findings, rep.affected)
		if bl != nil {
			var suppressed int
			rep.findings, suppressed = baseline.Filter(rep.findings, bl)
			log.Printf("baseline: suppressed %d known finding(s)", suppressed)
		}

		renderDiff(rep, format)

		fail, err := report.ShouldFail(rep.findings, failOn)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		if fail {
			os.Exit(1)
		}
		return
	}

	// 全仓扫描模式
	reported := findings
	if bl != nil {
		var suppressed int
		reported, suppressed = baseline.Filter(reported, bl)
		log.Printf("baseline: suppressed %d known finding(s)", suppressed)
	}
	render(reported, format)

	fail, err := report.ShouldFail(reported, failOn)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	if fail {
		os.Exit(1)
	}
}
