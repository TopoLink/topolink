# Topolink

**Deterministic code blast-radius analysis for Go microservices.**

Topolink traces every change from your HTTP / gRPC / Kafka entry points down to the database, flags the risky ones (N+1 queries, full-table writes, long transactions, lock-held remote calls), and answers two questions developers actually ask:

- **Before you merge** — *What breaks if I change this table or this method?*
- **When it's on fire** — *Where does this error come from?*

Everything is extracted from the AST with [tree-sitter](https://tree-sitter.github.io/) — **no LLM in the critical path**, no server, no database. It runs in a single Docker container and finishes in milliseconds.

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/topolink/topolink.svg)](https://pkg.go.dev/github.com/topolink/topolink)

---

## Quick start — GitHub Action

```yaml
name: PR Risk Firewall
on: [pull_request]

jobs:
  topolink:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0   # needed to diff against the base branch
      - uses: TopoLink/topolink@v1
        with:
          fail-on: high   # none | critical | high | medium
          # diff: auto     # default: analyze only this PR's diff; set '' for a full scan
```

The action:

- posts **inline annotations** on the changed files,
- writes a **Markdown job summary**,
- **fails the check** when findings reach the configured severity.

## Quick start — CLI

> Building from source needs a C toolchain (`gcc`/`clang`): tree-sitter grammars are C code. The Docker image already bundles everything.

```bash
go install github.com/topolink/topolink/cmd/topolink@latest

# scan the current repo
topolink -repo .

# Markdown report
topolink -repo . -format markdown

# GitHub Actions format (annotations + job summary + exit code)
topolink -repo . -format github -fail-on s1
```

Example output:

```
topolink: detected ecosystem rules: [go/nethttp.scm go/gin.scm go/gorm.scm]
topolink: scanned 1 files: 3 entries, 11 calls, 2 db-ops
topolink: 2 L1 finding(s):
  [query-in-loop · medium] DB query inside a loop (suspected N+1 query) entry=http:GET:/orders (handler.go:22)
  [write-without-where · critical] Write operation without WHERE clause (potential full-table update/delete) entry=http:DELETE:/orders (handler.go:39)
```

### Diff-driven blast radius

Scanning the whole repo is useful for a one-off audit. The question on every PR is different: *what does **this change** affect?*

```bash
# analyze only what this branch changed
topolink -repo . -diff origin/main...HEAD

# or from a pre-generated unified diff
topolink -repo . -diff-file pr.diff
```

```
topolink: diff impact: 1 changed function(s), 1 affected entry(ies)
  changed: FindByID (repo.go:17)
  affected entry: http:GET:/orders
1 L1 finding(s):
  [query-in-loop · medium] DB query inside a loop (suspected N+1 query) entry=http:GET:/orders (repo.go:19)
```

Only the entry points reachable from **your diff** are reported — a blast radius, not a repo-wide lint report.

### AI semantic enrichment (optional)

Set an OpenAI-compatible model and Topolink annotates entries, middleware (including each abort branch), and tables with business meaning. Annotations are cached by **content fingerprint**, so unchanged code is never re-sent to the model.

```bash
OPENAI_API_KEY=sk-... topolink -repo . -semantic .topolink/semantic.json -ai-model gpt-4o-mini
```

The PR report then reads like:

```
changed table: orders — financial ledger holding money
affected entry: http:GET:/orders — order query API

Protection of affected entries
- http:GET:/orders
  - Auth: JWT auth guard
    - if no token → abort 401 (rejects anonymous)
    - if tenant mismatch → abort 403 (prevents cross-tenant access)
```

In CI, set `OPENAI_API_KEY` and cache the semantic index between runs:

```yaml
- uses: actions/cache@v4
  with:
    path: .topolink
    key: topolink-semantic-${{ github.ref }}
```

Without an API key the action still works — it records fingerprints and only uses name-based heuristics.

### Policy file (`.pr-risk.yml`)

Zero config is the default, but teams can tune behavior without changing the CLI:

```yaml
rules:
  disable: [missing-auth, R7]       # rule names or R codes
ignore:
  paths: ["internal/gen/**", "**/*.pb.go"]
public_paths: [webhook, callback]   # extra unauthenticated first path segments
tables:
  sensitive: [ledger, wallet]       # extra money/sensitive keywords
  ignore: [tmp_, audit_]            # never flag tables with these prefixes
fail_on: high                       # used when -fail-on is not passed
```

All keys are optional. Use `-config <path>` to point at a different file.

### Cross-file route assembly

Middleware inheritance works across files: a router group defined in `router.go` and `api.Use(Auth())` registered in `middleware.go` are merged. Within a file, registration order still matters (a `Use` only applies to routes registered after it).

---

## What it detects

| Rule | Detection | Severity |
|------|-----------|----------|
| **`write-without-where`** (R5) | `UPDATE` / `DELETE` without `WHERE` | **critical** |
| **`write-in-loop`** (R2) | DB write inside a loop | **high** |
| **`remote-in-transaction`** (R3) | Remote call inside a transaction | **high** |
| **`remote-under-lock`** (R4) | Remote call while holding a lock | **high** |
| **`query-in-loop`** (R1) | DB query inside a loop (N+1) | medium |
| **`unchecked-write`** (R6) | Write result not checked | medium |
| **`missing-timeout`** (R7) | Remote call without a timeout | medium |
| **`missing-auth`** (R8) | HTTP entry without auth middleware (public paths exempt) | high |
| **`money-write`** (R9) | Write to a sensitive / money-related table | high |
| **`cache-no-ttl`** (R10) | Cache write without a TTL | medium |
| **`weak-auth`** (R11) | Auth middleware that never aborts/denies on failure | medium |

Rules are **inter-procedural**. A query buried in `repo.FindByID()` is still flagged if its caller runs it in a loop. Context (loop / transaction / lock) propagates across:

- **functions and methods** — `repo.Find()` resolves to the `Find` definition,
- **packages** — same module, multiple packages,
- **callback-based wrappers** — `withLock(func(){ ... })` and `withTx(func(tx){ ... })`,
- **manual transactions** — `db.Begin()` … `tx.Commit()`.

### Severity levels

| Level | Code | Meaning |
|-------|------|---------|
| **critical** | `S0` | Data loss / full-table mutation |
| **high** | `S1` | Production-stability risk (long transactions, lock-held remote calls) |
| **medium** | `S2` | Performance / robustness smell (N+1, unchecked writes, missing timeouts) |
| **low** | `S3` | Informational |

## Supported ecosystems

Auto-detected from `go.mod` — **zero config required**:

| Area | Frameworks |
|------|------------|
| HTTP | Gin, Echo, Iris, Chi, `net/http` |
| Database | GORM (including raw SQL via `Raw` / `Exec`) |
| RPC | gRPC |
| Messaging | Sarama, kafka-go |

Adding a new framework is one `.scm` file plus one line in the ecosystem map.

---

## Two query modes

Beyond PR checks, Topolink answers questions about your codebase directly.

### Reverse impact — *what breaks if I change this?*

```bash
topolink -repo . -impact table:orders
topolink -repo . -impact method:FindByID
```

```
impact of table:orders → 1 reference(s):
  entry=http:GET:/orders op=Read (repo.go:9)
```

### Troubleshooting — *where does this come from?*

```bash
topolink -repo . -trouble "err:order-not-found"
topolink -repo . -trouble "log:load failed: %v"
topolink -repo . -trouble "timeout:context.WithTimeout"
```

```
trouble clue "err:order-not-found" → 1 location(s):
  handler.go:10
```

---

## How it works

```
go.mod ──sniff──► select .scm queries
                        │
                     tree-sitter AST
                        │
         entry points ─► call graph ─► DB / remote / lock ops
                        │
               propagate loop / tx / lock context
                        │
                  R1–R11 deterministic rules
                        │
        findings + annotations + job summary
```

No network calls, no telemetry, and **no source code leaves the runner**.

## Output formats

| `-format` | Behavior |
|-----------|----------|
| `text` (default) | human-readable log lines |
| `markdown` | severity-sorted report table |
| `github` | `::error` / `::warning` annotations + `$GITHUB_STEP_SUMMARY` |

## Exit codes

| `-fail-on` | Exit non-zero when… |
|------------|---------------------|
| `none` (default) | never |
| `critical` (`s0`) | any **critical** finding |
| `high` (`s1`) | any **critical** or **high** finding |
| `medium` (`s2`) | any **critical**, **high**, or **medium** finding |
| `low` (`s3`) | any finding |

---

## Limitations

Topolink is an honest, deterministic first pass — not a soundness-complete prover:

- **Go only** (for now).
- Symbol resolution is **receiver-aware** for common cases (method receivers, parameters, composite literals, `NewX()` / `GetX()` constructors). When the receiver type cannot be inferred, it falls back to name matching, so unusual assignments may still merge same-named methods.
- **R6 / R7 are heuristics** (statement-level / function-level).
- Dynamic route paths (for example a path held in a variable) cannot be resolved to a concrete URL.
- GORM `Model(instance)` / `Save` are treated as implicit primary-key `WHERE`; zero-value `Model(&Type{})` is still treated as a potential full-table write.

## Roadmap

- [ ] L2 complexity weighting to prioritize findings
- [ ] gRPC method-level entries
- [ ] Additional languages (Java first)

## Documentation

- [Architecture & repository conventions](docs/en/architecture.md)
- [Rule catalog & detection spec](docs/en/rules.md)
- [Adding a language adapter (Java next)](docs/en/language-adapters.md)
- Chinese originals: [`docs/03`](docs/03-技术架构与目录规范.md), [`docs/04`](docs/04-规则清单与识别规则.md), [`docs/06`](docs/06-多语言适配器.md)

## Contributing

Issues and PRs are welcome. Run `go test ./...` before submitting.

## License

Apache-2.0 — see [LICENSE](LICENSE).
