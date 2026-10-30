# Topolink — Architecture & Repository Conventions

> Version v1.0 · Implementation language: Go 1.21+ (mandatory) · Target languages: Java first, then Go
>
> This is the English edition. It mirrors the Chinese original (`docs/03-技术架构与目录规范.md`) and is lightly updated to match the Phase 1 implementation.

---

## 1. Architecture Overview

```
                    ┌─────────────────────────────────────────────┐
                    │  cmd/topolink  (single CLI entry point)      │
                    │  · catches errors · logs gracefully          │
                    │  · never panics in business logic            │
                    └───────────────────────┬─────────────────────┘
                                            │
        ┌───────────────────────────────────┼───────────────────────────┐
        │  pkg/ (core assets, decoupled from the runtime)               │
        │                                                               │
        │  config ──► lang ──► ast ──► index ──► engine ──► llm/meta    │
        │  (sniff)   (parse)  (extract) (index)  (funnel+query) (ext.)  │
        │                          │                                    │
        │                          └──► semantic (AI annotations)      │
        │                                                               │
        │  diff ──► impact ──► report / baseline                        │
        └───────────────────────────────────────────────────────────────┘
                                            │
                         Storage / VCSClient interfaces (memory | Postgres | Neo4j)
```

**Data flow (Phase 1)**

`sniff go.mod → load .scm → tree-sitter parse → extract EntryNode / CallNode / DBOp → build forward / reverse / troubleshooting indexes → run the funnel → render findings`

**Design invariants**

1. Packages under `pkg/` never know about the `cmd/` runtime. `pkg/ast` extracts structured data only — it never assembles findings. `pkg/engine` combines data and runs the funnel.
2. Error handling: business logic never panics; errors bubble up as `fmt.Errorf("...: %w", err)` and are caught only in `cmd/topolink/main.go`.
3. Every external dependency (LLM, GitHub, metadata sources, storage) is an interface, so it can be mocked or swapped.
4. Stateless by default: all indexes live in memory (native maps/slices) and disappear with the container.

---

## 2. Directory Structure (current)

```
topolink/
├── action.yml                  # GitHub Action definition
├── Dockerfile                  # Pure-Go tree-sitter, no cgo
├── go.mod / go.sum
├── docs/                       # Strategy & design docs
│   ├── 03-技术架构与目录规范.md
│   ├── 04-规则清单与识别规则.md
│   └── en/
│       ├── architecture.md     # this file
│       └── rules.md
├── cmd/
│   └── topolink/               # Single CLI entry point
│       ├── main.go             # flags + wiring
│       ├── analyze.go          # extraction pipeline
│       ├── diffmode.go         # diff-driven blast radius
│       ├── query.go            # -impact / -trouble queries
│       └── report.go           # text / markdown / github output
├── pkg/                        # Core assets (runtime-decoupled)
│   ├── config/                 # .pr-risk.yml + ecosystem sniffing (go.mod)
│   ├── lang/                   # Multi-language tree-sitter registry
│   ├── ast/                    # AST traversal: entries, calls, DB ops
│   │   └── queries/go/         # .scm rules (gin, echo, chi, gorm, grpc, kafka…)
│   ├── index/                  # Forward / reverse / troubleshooting indexes
│   ├── engine/                 # Funnel rules + policy (rule toggles)
│   ├── semantic/               # AI annotations, content fingerprint cache
│   ├── diff/                   # Unified-diff parser
│   ├── impact/                 # Changed functions/tables → affected entries
│   ├── baseline/               # Suppress known findings, report only new risk
│   ├── report/                 # Markdown + GitHub annotations + exit codes
│   ├── profile/                # Library profile (cross-repo without source)
│   ├── llm/                    # RiskAnalyzer interface (L3, default noop)
│   └── meta/                   # MetadataSource interface (P2, default noop)
```

---

## 3. Core Data Structures (the index is the system of record)

```go
// EntryNode: a business entry point.
type EntryNode struct {
    ID          string    // stable ID, e.g. "http:GET:/api/order"
    Kind        EntryKind // HTTP | GRPC | KafkaConsumer | XXLJob | Cron
    Name        string    // path / method / topic / job name
    Handler     Symbol    // handler function
    Middleware  []string  // inline + inherited middleware
    SourceLoc   Loc
    PodHints    []string  // P2: runtime unit hints
}

// CallNode: one call edge (forward call chain).
type CallNode struct {
    Caller      Symbol
    Callee      Symbol
    Kind        CallKind // DB | Redis | Lock | MQ | HTTP | GRPC | ThirdParty | Func
    Loc         Loc
    Args        []string // key literals (table / key / topic / URL)
    InLoop      bool
    InTx        bool
    UnderLock   bool
    InGoroutine bool
    Standalone  bool // write result not checked (R6)
    NoTimeout   bool // no timeout detected (R7)
    CallbackOf  []string
}

// DBOp: a downstream database/storage operation.
type DBOp struct {
    EntryID     string
    Caller      Symbol
    Op          OpKind   // Read | Write | Delete
    Method      string   // Create / Update / Delete / Raw…
    Resource    string   // table name / key pattern
    Fields      []string
    Where       string
    InLoop      bool
    InTx        bool
    UnderLock   bool
    InGoroutine bool
    Standalone  bool
    Loc         Loc
}
```

**Three in-memory indexes (all serializable)**

```go
// Forward: function/entry → call chain.
type ForwardIndex map[Symbol][]CallNode

// Reverse: resource → entries (read/write aware).
type ReverseIndex map[ResourceKey][]Ref
type ResourceKey struct { Kind string; Name string } // table | key | config | method | topic
type Ref struct { EntryID string; Op OpKind; Loc Loc }

// Troubleshooting: error code / log template / timeout / enum → source location.
type TroubleIndex map[string][]Loc // "err:10001", "log:xxx", "timeout:yyy", "enum:zzz"
```

---

## 4. Interface Boundaries

```go
// LLM: L3 business-semantics reasoning (P1 noop, P2 real provider).
type RiskAnalyzer interface {
    Analyze(ctx context.Context, chain []CallNode, meta []BusinessTag) (*RiskReport, error)
}

// Business metadata: YApi / proto comments / TiDB schema / XXL admin.
type MetadataSource interface {
    Fetch(ctx context.Context, ref ResourceKey) ([]BusinessTag, error)
}

// Source control: GitHub (P1) / GitLab / Yunxiao (P2).
type VCSClient interface {
    GetPRDiff(ctx context.Context, repo, pr string) ([]FileDiff, error)
    PostComment(ctx context.Context, repo, pr, body string) error
}

// Storage: memory (P1) / Postgres (P2) / Neo4j (P3) behind one interface.
type Storage interface {
    Save(ctx context.Context, idx *Index, key string) error
    Load(ctx context.Context, key string) (*Index, error)
}
```

The AI semantic layer is deliberately separate from dependency extraction:

```go
// semantic.Unit / Annotation / Effect
type Annotation struct {
    Kind       string   // middleware-purpose / business-impact / data-sensitivity
    Text       string
    Effects    []Effect // Condition → Action → Business
    Evidence   []ast.Loc
    Confidence float64
    Model      string
}
```

Annotations are cached by a SHA-256 **content fingerprint** (`kind + id + source`), so unchanged code is never re-sent to the model.

---

## 5. Key Engineering Decisions

### 5.1 No cgo
- `smacker/go-tree-sitter` requires CGo and complicates Docker/cross-compilation.
- We use the pure-Go binding `github.com/tree-sitter/go-tree-sitter`, so the Dockerfile is a simple multi-stage build with no C toolchain.

### 5.2 Dependencies
- `gopkg.in/yaml.v3` (policy file)
- `github.com/tree-sitter/go-tree-sitter` + grammar modules
- `go.mod` sniffing is a small hand-written parser (avoids pulling `golang.org/x/mod` and its Go-version requirement)

### 5.3 Language strategy (implementation vs. target)
- **Implementation**: Go 1.21+ (fixed).
- **Targets**: Java first, then Go. `.scm` files live under `queries/go/` and (roadmap) `queries/java/`; `pkg/lang` is the parser registry.

### 5.4 Funnel filtering

- **L0 (physical)**: skip `*_test.go`, `Test*.java`, `vendor/`, `node_modules/`, `docs/`, generated code (`*.pb.go`, `*_gen.go`, `mock_*.go`), plus user-configured `ignore.paths`.
- **L1 (local AST rules)**: query-in-loop, write-in-loop, remote-in-transaction, remote-under-lock, write-without-WHERE, unchecked-write, missing-timeout, missing-auth, money-write, cache-without-TTL, weak-auth.
- **L2 (complexity/comment heuristics)**: weights for L3 ranking, never standalone findings.
- **L3 (LLM)**: only L1 hits are eligible; the model produces risk level + business-impact summary.

### 5.5 Diff-driven blast radius

`pkg/diff` parses unified diffs and keeps added/removed line text. `pkg/impact` maps changed functions/tables/route registrations to affected entry points. The CLI then filters findings to that blast radius, so a PR only reports risk reachable from the change — not a repo-wide lint report.

### 5.6 Baseline

`pkg/baseline` stores accepted findings by `rule | entry | file | line` (base name only, so it is portable across machines). `-baseline` suppresses known findings; `-baseline-out` writes the current set. Only new risk fails the build.

---

## 6. Phase 1 Implementation Checklist

1. `config/`: sniff `go.mod` → return the `.scm` list; parse `.pr-risk.yml`.
2. `lang/`: parser registry; pure-Go bindings.
3. `ast/`: produce `EntryNode` / `CallNode` / `DBOp` / `FuncSummary` / `Trouble`; extraction only.
4. `index/`: build the three indexes; support serialization for future persistence.
5. `engine/`: funnel rules + `Policy` (rule toggles, public paths, table keywords).
6. `semantic/`: content-addressed AI annotations, reused across runs.
7. `diff` + `impact` + `report` + `baseline`: PR-oriented output.
8. `cmd/topolink/main.go`: the only place that catches errors and exits non-zero.
