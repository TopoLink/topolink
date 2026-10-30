# Security Policy

## Supported versions

Topolink is pre-1.0. Security fixes land on `main` and are released as patch tags.

## Reporting a vulnerability

Please **do not** open a public issue for a security problem.

Use GitHub's private vulnerability reporting:

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability**.
3. Describe the issue, the impact, and a minimal reproduction.

We aim to acknowledge reports within 3 business days and to ship a fix or mitigation as soon as practical.

## Scope

Topolink runs locally / in CI and does not send source code anywhere by default.

Relevant areas:

- Docker / GitHub Action entrypoint and permissions
- Shell or path handling around `git diff`
- Optional AI enrichment (`-semantic`) — only the selected source snippets and prompts leave the runner
- Parsing of untrusted repository content (tree-sitter queries)

Out of scope for this policy: findings produced by the analyzer about your own codebase.
