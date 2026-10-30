# Contributing to Topolink

Thanks for taking the time to contribute! This project values **deterministic, auditable analysis** over clever heuristics.

## Ground rules

- **No `panic` in business logic.** Return `fmt.Errorf("...: %w", err)` and let `cmd/topolink` handle it.
- **Extraction stays deterministic.** `pkg/ast` must not call an LLM. AI belongs in `pkg/semantic`.
- **Keep packages decoupled.** `pkg/ast` extracts, `pkg/engine` evaluates, `cmd` orchestrates.
- **Every behavior change needs a test.** Run `go test -race ./...` before opening a PR.

## Local setup

```bash
git clone git@github.com:TopoLink/topolink.git
cd topolink
go test ./...
go build ./...
```

If you work inside a `go.work` workspace with the private `topolink-pro` module, run core commands with:

```bash
GOWORK=off go test ./...
```

## Adding a framework

1. Add a query file under `pkg/ast/queries/go/<framework>.scm`.
2. Register it in `pkg/config/config.go` (`ecosystemRules`).
3. Add a test in `pkg/ast/extract_test.go` with a small real-world snippet.
4. Update `docs/04-规则清单与识别规则.md` and `docs/en/rules.md` if the rule set changes.

## Adding a rule

1. Implement the detection in `pkg/engine` (or `pkg/ast` if it needs new structural data).
2. Map it to a semantic name in `pkg/engine/rules.go`.
3. Add positive **and** negative tests.
4. Document it in the README rule table and the rule catalog.

## Pull requests

- Keep PRs focused; one rule or one framework per PR is ideal.
- Explain the user-visible impact in the PR description.
- Include a minimal reproduction (code snippet or test) for bug fixes.

## Code style

- `gofmt` is mandatory (CI checks it).
- Prefer small interfaces and explicit error wrapping.
- Comments should explain *why*, not restate the code.

## Reporting security issues

Please do **not** open a public issue for vulnerabilities. Use GitHub's private vulnerability reporting (Security → Report a vulnerability) instead.
