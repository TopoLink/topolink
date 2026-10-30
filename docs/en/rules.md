# Topolink — Rule Catalog & Detection Spec (Funnel + .scm + Indexes)

> Version v1.0 · This document is the single source of truth for the Phase 1 rules.
> Related: `architecture.md` (how the code is organized) · business positioning, roadmap, and GTM live in a separate private repository.
>
> This is the English edition of `docs/04-规则清单与识别规则.md`, updated with the rules that shipped in Phase 1.

---

## 0. How the Rules Fit Together

```
config sniff → decide which .scm rules to load (Parts B/C)
    ↓
ast extracts EntryNode / CallNode / DBOp with .scm (Parts B + C)
    ↓
index builds three indexes (Parts D + E)
    ↓
engine filters and grades with the funnel (Parts A + G)
    ↓
llm runs L3 only on L1 hits + L2 weighting (Parts A.4 + F)
```

**Principle:** L0/L1/L2 are deterministic, unit-testable, and network-free. Only L3 uses an LLM, and it only receives chains that already triggered an L1 rule.

---

# Part A — Funnel Filtering

## A.1 L0: Physical filtering (never parsed, never indexed)

Goal: keep non-business code out, reducing false positives and runtime.

### Skip list
| Type | Go | Java | Common |
|---|---|---|---|
| Tests | `*_test.go`, `_test/` | `*Test.java`, `src/test/` | — |
| Dependencies | `vendor/` | — | `node_modules/` |
| Generated | `*.pb.go`, `*.gen.go`, `mock_*.go` | `*Generated.java`, Lombok/MapStruct output, MyBatis generator output | `docs/`, `.git/` |

### Parse list
- Go: `.go` files that are not tests, vendor, or generated.
- Java: `src/main/java/**` + `src/main/resources/**/mapper/**/*.xml` (MyBatis XML is a SQL source).

### Signature
```go
func shouldParse(path string) bool // false → filtered by L0
```

Users can extend the skip list with `ignore.paths` in `.pr-risk.yml`.

---

## A.2 L1: Local AST rules (a hit is a finding)

> Rules are identified internally as `R1`–`R11`; the user-facing name is semantic.

### R1 Query inside a loop (N+1)
- **Trigger:** `DBOp.InLoop == true` and `Op == Read`.
- **Go:**
  ```go
  for _, u := range users {
      db.Where("id = ?", u.ID).First(&order) // ← N+1
  }
  ```
- **Java:**
  ```java
  for (User u : users) {
      orderMapper.selectByUserId(u.getId()); // ← N+1
  }
  ```
- **Default:** medium (S2); escalate to high if there is no pagination/limit.

### R2 Write inside a loop
- **Trigger:** `DBOp.InLoop == true` and `Op ∈ {Write, Delete}`.
- **Default:** high (S1) — write amplification and lock contention.

### R3 Remote call inside a transaction
- **Trigger:** the operation is inside a transaction and a `CallKind ∈ {HTTP, GRPC, MQ, ThirdParty}` occurs in the same transaction scope.
- **Go:**
  ```go
  db.Transaction(func(tx *gorm.DB) error {
      tx.Save(&order)
      http.Post(notifyURL, ...) // ← long transaction + external dependency
      return nil
  })
  ```
- **Default:** high (S1); multiple remote calls → critical (S0).

### R4 Remote call under a lock
- **Trigger:** a remote call occurs while a lock is held.
- **Go:** between `mutex.Lock()` and `mutex.Unlock()`.
- **Java:** inside `synchronized` / `ReentrantLock.lock()` / Redisson `getLock`.
- **Default:** high (S1); coarse lock scope → critical (S0).

### R5 Write without WHERE
- **Trigger:** `Op ∈ {Write, Delete}` and `Where == ""`.
- **Go:** `db.Model(&Order{}).Update("status", 1)` with no `.Where`.
- **Java:** MyBatis `<update>` with no `<where>` / WHERE clause.
- **Default:** **critical (S0)**.

### R6 Ignored write error
- **Trigger:** a write/delete result (error or affected rows) is not checked.
- **Go:** `db.Save(&x)` used as a statement.
- **Java:** `mapper.update(...)` return value ignored.
- **Default:** medium (S2).

### R7 Remote call without a timeout
- **Trigger:** `CallKind ∈ {HTTP, GRPC}` and no `context.WithTimeout`, `http.Client{Timeout}`, RPC timeout, or equivalent.
- **Default:** medium (S2).

### R8 Missing authentication (implemented)
- **Trigger:** an HTTP entry has no auth middleware and its path is not a known public path.
- **Default:** high (S1).
- **Suppression:** built-in public paths (`/health`, `/login`, `/oauth`, `/webhook`, …), user `public_paths`, or an AI annotation on a middleware.

### R9 Write to a sensitive/money table (implemented)
- **Trigger:** a write/delete targets a table that is money-like by name, flagged sensitive by an AI annotation, or listed in `tables.sensitive`.
- **Default:** high (S1).
- **Suppression:** `tables.ignore` prefixes.

### R10 Cache set without TTL (implemented)
- **Trigger:** a Redis/cache `Set` call whose TTL argument is `0`.
- **Default:** medium (S2).

### R11 Auth middleware that does not actually block (implemented)
- **Trigger:** a middleware is auth-like, and its AI annotation shows no `abort`/`deny`/`redirect` effect on failure.
- **Default:** medium (S2).
- **Requires:** AI annotation (no annotation → no finding).

---

## A.3 L2: Complexity & comment heuristic (weights only)

- Cyclomatic complexity > 15
- Function length > 80 lines
- Missing function-level comment on an L1 hit
- **Effect:** ranking for L3, never a standalone finding.

---

## A.4 L3: LLM deep reasoning (only high-risk chains)

- **Trigger:** L1 hit and L2 score ≥ threshold.
- **Input:** the call chain with locations, business tags (Part F), approximate SQL.
- **Output:** `RiskReport{ Level, Summary, Suggestion }`.
- **Prompt skeleton:**
  ```
  You are a senior backend architect. The following call chain was flagged by
  static analysis (reason: {rule}). Judge: 1) real risk level 2) concrete
  business impact 3) remediation. Use only the given information; say
  "cannot determine" when evidence is insufficient.
  ```
- **Constraint:** L3 only *qualifies* risk. It never participates in dependency extraction.

---

# Part B — Entry Detection

> An entry point is where the system is exposed or passively triggered. The product is always an `EntryNode`.

## Entry ID format

```
http:{METHOD}:{path}       # http:GET:/api/v1/orders
grpc:{Service}.{Method}    # grpc:OrderService.GetOrder
kafka:{topic}              # kafka:order-created (group goes in PodHints)
xxl:{jobHandlerName}       # xxl:orderDailyJob
```

## B.1 HTTP entries

### Gin (Go)
- **Detection:** `r.GET/POST/PUT/DELETE/PATCH/Any`; `r.Group("/prefix")` prefixes.
- **Extract:** method, path, handler, `file:line`, inline + inherited middleware.
- **Sketch .scm:**
  ```scheme
  (call_expression
    function: (selector_expression
      operand: (identifier) @receiver
      field: (field_identifier) @method
      (#match? @method "^(GET|POST|PUT|DELETE|PATCH|Any)$"))
    arguments: (argument_list
      (interpreted_string_literal) @http.args
      (identifier) @handler)) @entry
  ```

### Spring MVC (Java)
- **Detection:** `@RestController`/`@Controller` class + `@RequestMapping`/`@GetMapping`/`@PostMapping`/`@PutMapping`/`@DeleteMapping`.
- **Extract:** class path + method path, HTTP method, handler.

## B.2 gRPC server entries

### grpc-go
- **Detection:** `pb.RegisterXxxServer(grpcServer, &impl{})` → methods of `impl`.
- **Extract:** service, method, handler, location.

### grpc-java
- **Detection:** `class XxxImpl extends XxxGrpc.XxxImplBase` + `@Override` methods.

## B.3 Kafka consumer entries

### Go (sarama / kafka-go)
- **Detection:** consumer handler implementations; `Subscribe(topics...)`.
- **Extract:** topic, group, handler.

### Java (Spring Kafka)
- **Detection:** `@KafkaListener(topics = "...", groupId = "...")`.
- **Extract:** topic, groupId, handler.

## B.4 XXL-Job entries (China-stack differentiator)

### Java
- **Detection:** methods annotated with `@XxlJob("orderDailyJob")`.
- **Extract:** jobHandlerName, handler, location.
- **Value:** Western tools do not recognize this entry type at all.

---

# Part C — Call Chain & Downstream Operations

## C.1 Call kinds

| Kind | Meaning | Detection |
|---|---|---|
| DB | database read/write | GORM / MyBatis / JdbcTemplate (C.2) |
| Redis | cache / distributed lock | redis client / redisTemplate |
| Lock | locking | `sync.Mutex` / `synchronized` / Redisson |
| MQ | message production | producer.Send / KafkaTemplate.send |
| HTTP | outbound HTTP | `http.Get/Post` / restTemplate / Feign |
| GRPC | outbound gRPC | client stub calls |
| ThirdParty | third-party SDK | payment/SMS/cloud SDKs |
| Func | ordinary call | everything else (builds the chain) |

## C.2 DB operations

### GORM (Go)
- **Reads:** `Find`, `First`, `Take`, `Select`, `Count`, `Pluck`, `Scan`, `Raw(SELECT)`.
- **Writes:** `Create`, `Update`, `Delete`, `Save`, `Exec`, `Raw(INSERT/UPDATE/DELETE)`.
- **Table extraction (priority):**
  1. `db.Table("orders")`
  2. `db.Model(&Order{})` → pluralized struct name (+ `gorm:"table:xxx"`)
  3. `db.Raw("... FROM orders ...")` → parse SQL
- **Fields/WHERE:** literals in `Where(...)` / `Select(...)`, chained conditions.

### MyBatis (Java)
- **Mapper method → XML:** `OrderMapper.selectByUserId()` → `<select id="selectByUserId">`.
- **Table/fields/WHERE:** parse the XML SQL.
- **Read/write:** `<select>` = Read; `<insert>/<update>/<delete>` = Write.

### Spring JdbcTemplate (Java)
- **Detection:** `jdbcTemplate.query/queryForObject/update/execute`.
- **Table/WHERE:** parse SQL literals.

## C.3 Redis / cache
- **Go:** `redis.Get/Set/HGet/HSet/...`; key from the first literal or format string.
- **Java:** `redisTemplate.opsForValue().get(key)`, `@Cacheable(cacheNames=...)`.
- **Output:** `ResourceKey{Kind:"key", Name:"cache:user:..."}`.

## C.4 Locks
- **Go:** `sync.Mutex.Lock()`, distributed `SetNX`/redlock.
- **Java:** `synchronized`, `ReentrantLock.lock()`, Redisson `getLock(key)`.

## C.5 MQ production
- **Go:** `producer.Send/Produce(msg)`.
- **Java:** `kafkaTemplate.send("topic", ...)`, `rocketMQTemplate.convertAndSend`.

## C.6 Context detection (fills the DBOp booleans)

| Field | Method (AST ancestor chain) |
|---|---|
| `InLoop` | ancestors include `for` / `range` / `while` |
| `InTx` | inside `db.Transaction(func(tx){...})` / `@Transactional` / `TransactionTemplate` |
| `UnderLock` | between `Lock()` and `Unlock()` / inside `synchronized` or `lock()` |
| `InGoroutine` | inside `go func(){...}` / `new Thread` / `CompletableFuture.runAsync` |

---

# Part D — Reverse Index

> Answers: "if I change this resource, which entries are affected?"

## Resource key format

| Kind | Format | Example | Source |
|---|---|---|---|
| table | `table:{name}` | `table:orders` | C.2 |
| key | `key:{pattern}` | `key:cache:user:{id}` | C.3 |
| config | `config:{key}` | `config:db.max_open` | config reads |
| method | `method:{Service.Method}` | `method:UserService.GetUser` | shared methods |
| topic | `topic:{name}` | `topic:order-created` | MQ |

## Extraction rules
- **Config keys:** `viper.Get("db.max_open")`, `@Value("${...}")`, Apollo, Nacos.
- **Shared methods:** non-entry functions called by multiple entries (≥ 2) enter the reverse index.
- **Read/write awareness:** `Ref.Op ∈ {Read, Write}` is mandatory.

---

# Part E — Troubleshooting Index

> Answers: "where does this error/timeout/log come from, and where do I look next?"

| Dimension | Key | Detection | Extracted |
|---|---|---|---|
| Error code | `err:{code}` | `errors.New("10001: ...")`, `BizException("10001")` | code + location |
| Error return | `errret:{func}` | `return err` / `throw` | file:line |
| Log template | `log:{literal}` | `log.Error("...")` | template + location |
| Timeout | `timeout:{loc}` | `context.WithTimeout`, `http.Client{Timeout}` | value + location |
| Enum | `enum:{name}` | Java `enum`, Go `iota` groups | values + comments |

---

# Part F — Business Metadata (Phase 2, interface reserved)

> Phase 1 defines `MetadataSource`; adapters land in Phase 2.

| Source | Match key | Enriches | Confidence |
|---|---|---|---|
| YApi | `http:{method}:{path}` | title, description, params | high |
| proto comments | `grpc:{Service}.{Method}` | leading `//` comment | high |
| TiDB schema | `table:{name}` | table/column comments | high |
| XXL admin | `xxl:{jobHandlerName}` | job description | medium |

- **Output:** `BusinessTag{ Kind, Value, Confidence }`.
- **Use:** high-confidence tags feed L3; low-confidence tags must say "cannot determine".

---

# Part G — Severity

| Level | Meaning | Default action |
|---|---|---|
| **critical (S0)** | possible full-table update, data loss, long blocking | block merge |
| **high (S1)** | clear performance/stability risk | require human confirmation |
| **medium (S2)** | potential risk, worth optimizing | inform only |
| **low (S3)** | advisory | optional |

`-fail-on` accepts both semantic names and `s0`/`s1`/`s2`/`s3` aliases; invalid values fail fast.

## Decision matrix

| Rule | Base condition | Default | Escalation |
|---|---|---|---|
| R5 write without WHERE | write && no where | **critical** | — |
| R2 write in loop | InLoop && Write | **high** | critical if no batching |
| R3 remote in TX | InTx && remote | high | critical if ≥ 2 remote calls |
| R4 remote under lock | UnderLock && remote | high | critical if lock is coarse |
| R1 query in loop | InLoop && Read | medium | high if no pagination |
| R7 remote without timeout | remote && no timeout | medium | — |
| R6 ignored write error | Write && unchecked | medium | — |
| R8 missing auth | HTTP && no auth && not public | high | — |
| R9 money write | Write && sensitive table | high | — |
| R10 cache without TTL | cache Set with TTL 0 | medium | — |
| R11 weak auth | auth middleware with no abort/deny | medium | — |

---

# Part H — Rule Quality & Regression

> Rules are the deterministic moat. Accuracy decides whether the product lives.

## Test corpus
1. Pick **3+ real microservice repositories** (at least 1 Go + 2 Java, covering Spring Boot + MyBatis + Dubbo + XXL).
2. Hand-label golden cases: known entries, known chains, known high-risk points.
3. Every rule needs at least 5 positive and 5 negative cases.

## Quality targets

| Metric | Definition | Phase 1 target |
|---|---|---|
| Entry detection accuracy | correct entries / all entries | ≥ 95% |
| Call-chain recall | real calls traced / all calls | ≥ 90% |
| DBOp field accuracy | table/key/WHERE/context correct | ≥ 90% |
| High-risk false-positive rate | false positives / all findings | ≤ 15% |

## Regression discipline
- Any `.scm` or rule change must run the golden corpus.
- Every false positive/negative is fed back into the corpus.
- Rule changes must update this document (this file is the rule truth).
