# Adding a Language Adapter (Java next)

> Short answer: **do not build a second engine.** Keep one core (IR + rules + indexes + reporting) and add a language adapter. Go is the first adapter, Java is the second.

---

## 1. What is language-specific today

Shared and language-agnostic:

- `pkg/ast/types.go` — the IR (`EntryNode`, `CallNode`, `DBOp`, `FuncSummary`, `Trouble`)
- `pkg/index` — forward / reverse / troubleshooting indexes
- `pkg/engine` — rules `R1`–`R11`, context propagation, policy
- `pkg/diff`, `pkg/impact`, `pkg/report`, `pkg/baseline`, `pkg/semantic`
- `pkg/profile` — library profile

Go-specific and therefore belongs behind an adapter:

- `pkg/ast/query.go` — Gin / GORM / gRPC / Kafka extractors
- `pkg/ast/extract.go` — Go AST walking, receiver-type inference, loop/tx/lock context
- `pkg/ast/middleware.go` — Gin `.Use` / `Group` resolution
- `pkg/ast/queries/go/*.scm`
- `pkg/config` — `go.mod` sniffing
- `pkg/lang` — currently a thin Go-only registry

---

## 2. Target architecture

```
cmd/topolink
   │
   ▼
pkg/lang.Registry
   ├── golang.Adapter   queries/go/*.scm + Go AST walker + Gin/GORM
   └── java.Adapter     queries/java/*.scm + Java AST walker + Spring/MyBatis
   │
   ▼
pkg/ast (IR only)  ──►  pkg/index  ──►  pkg/engine  ──►  pkg/report
```

The adapter only produces the shared IR. Rules, indexes, diff, impact, baseline and semantics stay untouched.

---

## 3. Adapter interface

```go
package lang

type Adapter interface {
    Name() string
    Extensions() []string
    Sniff(root string) ([]string, error) // return the .scm rule files for this repo
    Parse(path string) (tree *sitter.Tree, src []byte, err error)
    NewExtractor() ast.Extractor
}

type Extractor interface {
    Extract(path string, tree *sitter.Tree, src []byte) ast.Result
    Resolve(results []ast.Result) // routes + middleware, per language
}
```

`ast.Result` already carries `Entries`, `Calls`, `Ops`, `Funcs`, `Troubles`, so the merge point is stable.

---

## 4. Refactor steps

1. Move the Go extractor into `pkg/lang/golang` (mechanical move; keep `pkg/ast` as IR + shared helpers).
2. Introduce `lang.Registry` with `ForFile(path)` and `ForRepo(root)`.
3. Change `config.Sniff` to return `{Language, RuleFiles}`:
   - `go.mod` → Go
   - `pom.xml` / `build.gradle` / `*.java` → Java
4. In `cmd/analyze.go`, pick the adapter per file, collect `[]ast.Result` per language, call `Resolve` per adapter, then merge into one `index.Build`.
5. Add `pkg/lang/java` and `queries/java/*.scm`.
6. Keep R1–R11 unchanged. If Java needs a new rule, add it in `pkg/engine` against the IR.

This can be done incrementally: step 1–4 are pure refactoring and keep the Go behavior identical; step 5 starts the Java coverage.

---

## 5. Java mapping

| Java construct | IR output |
|---|---|
| `@RestController` + `@GetMapping` / `@PostMapping` | `EntryNode` (HTTP), path = class + method mapping |
| `@Transactional` | `InTx = true` on downstream operations |
| MyBatis `<select>` / `<insert>` / `<update>` / `<delete>` | `DBOp` with table and `WHERE` |
| Mapper interface method | links the call to the XML statement |
| `@DubboService` / `@DubboReference` | `EntryNode` (RPC) / `CallGRPC`-like remote call |
| `@XxlJob("name")` | `EntryNode` (`XXLJob`) |
| `RedisTemplate` / `@Cacheable` | `CallRedis` / cache resource |
| `synchronized` / `ReentrantLock` / Redisson | `UnderLock` |
| `@KafkaListener` | `EntryNode` (`KafkaConsumer`) |
| `restTemplate` / Feign | `CallHTTP` |

Start with Spring MVC + MyBatis + JDBC (open core). Dubbo, XXL-Job, Nacos/Apollo, YApi/TiDB and sharding belong to the pro adapter set.

---

## 6. Open core vs pro

| Layer | Open core | Pro |
|---|---|---|
| Go adapter | Gin, Echo, Chi, Iris, net/http, GORM, gRPC, Kafka | — |
| Java adapter | Spring MVC, MyBatis, JDBC, `@Transactional` | Dubbo, XXL-Job, Nacos, Apollo, YApi, TiDB, sharding |
| Cross-service | — | gRPC method resolution, Kafka producer/consumer pairing |
| Graph | in-memory | Neo4j |

The core adapter proves the IR and rules are language-neutral; the pro adapter monetizes the China stack.

---

## 7. Why not a separate repository / engine

- The moat is the **IR + rules + blast-radius UX**, not the parser. A second engine duplicates indexes, rules, reporting, baseline and semantic caching.
- One IR enables **cross-language blast radius** later (a Go service calling a Java service over gRPC).
- A new language should be a new adapter + `.scm` files, not a new product.

---

## 8. Effort estimate

| Milestone | Effort |
|---|---|
| Adapter seam + move Go extractor (no behavior change) | 1–2 days |
| Java grammar + Spring MVC entries + basic calls | 3–5 days |
| MyBatis XML → `DBOp` (table/WHERE) | 3–5 days |
| `@Transactional` / locks / Kafka / Dubbo (pro) | 1–2 weeks |

The first Java milestone only needs to produce the same IR; the existing R1–R11 rules then work without changes.
