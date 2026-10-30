package engine

import (
	"strings"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/llm"
	"github.com/topolink/topolink/pkg/semantic"
)

// evaluateDBRules applies database-related rules (R1/R2/R5/R6) to a single DBOp.
// The context c already merges the propagated call-site context with the op's local context.
func evaluateDBRules(entryID string, op ast.DBOp, c execCtx, anns map[string]semantic.Annotation, policy Policy) []Finding {
	var out []Finding
	isWrite := op.Op == ast.OpWrite || op.Op == ast.OpDelete

	// R5 UPDATE/DELETE without WHERE (fatal; Create/Save are inserts and need no WHERE)
	hasWhere := op.HasWhere || op.Where != ""
	if (op.Method == "Update" || op.Method == "Updates" || op.Method == "Delete") && !hasWhere && !op.ImplicitWhere {
		out = append(out, Finding{
			Rule:       "R5",
			Level:      llm.RiskFatal,
			EntryID:    entryID,
			Loc:        op.Loc,
			Summary:    "Write operation without WHERE clause (potential full-table update/delete)",
			Suggestion: "Add a WHERE clause, or explicitly cap the affected rows",
		})
		return out
	}

	// R2 DB write inside a loop (including wrapper calls; context propagated)
	if c.InLoop && isWrite {
		out = append(out, Finding{
			Rule:       "R2",
			Level:      llm.RiskHigh,
			EntryID:    entryID,
			Loc:        op.Loc,
			Summary:    "DB write inside a loop (write amplification / lock contention)",
			Suggestion: "Use batch writes instead of per-row writes",
		})
		return out
	}

	// R1 DB query inside a loop (including wrapper calls; context propagated)
	if c.InLoop && op.Op == ast.OpRead {
		out = append(out, Finding{
			Rule:       "R1",
			Level:      llm.RiskMedium,
			EntryID:    entryID,
			Loc:        op.Loc,
			Summary:    "DB query inside a loop (suspected N+1 query)",
			Suggestion: "Batch the query or preload, avoid per-item queries in loops",
		})
	}

	// R6 Write result not checked (standalone statement)
	if isWrite && op.Standalone {
		out = append(out, Finding{
			Rule:       "R6",
			Level:      llm.RiskMedium,
			EntryID:    entryID,
			Loc:        op.Loc,
			Summary:    "Write result not checked (error may be ignored)",
			Suggestion: "Check the returned error and retry or roll back on failure",
		})
	}

	// R9 Write to a sensitive / money-related table (business-critical)
	if isWrite && policy.isSensitiveTable(op.Resource, anns) {
		summary := "Write to a sensitive table \"" + op.Resource + "\"; verify idempotency and audit logging"
		if a, ok := anns["table:"+op.Resource]; ok && a.Text != "" {
			summary = "Write to sensitive table \"" + op.Resource + "\" (" + a.Text + "); verify idempotency and audit logging"
		}
		out = append(out, Finding{
			Rule:       "R9",
			Level:      llm.RiskHigh,
			EntryID:    entryID,
			Loc:        op.Loc,
			Summary:    summary,
			Suggestion: "Ensure idempotency (unique key / idempotency key) and audit logging for sensitive operations",
		})
	}

	return out
}

// evaluateCallRules applies remote/lock/cache rules (R3/R4/R7/R10) to a single call.
func evaluateCallRules(entryID string, call ast.CallNode, c execCtx) []Finding {
	var out []Finding

	// R10 Cache set without TTL (entries never expire)
	if isSetWithoutTTL(call) {
		out = append(out, Finding{
			Rule:       "R10",
			Level:      llm.RiskMedium,
			EntryID:    entryID,
			Loc:        call.Loc,
			Summary:    "Cache set without TTL (entries never expire)",
			Suggestion: "Set a TTL to avoid unbounded growth and stale data",
			Chain:      []ast.CallNode{call},
		})
	}

	remote := call.Kind == ast.CallHTTP || call.Kind == ast.CallGRPC || call.Kind == ast.CallMQ
	if !remote {
		return out
	}

	// R3 Remote call inside a transaction
	if c.InTx {
		out = append(out, Finding{
			Rule:       "R3",
			Level:      llm.RiskHigh,
			EntryID:    entryID,
			Loc:        call.Loc,
			Summary:    "Remote call inside a transaction (long-running transaction)",
			Suggestion: "Move the remote call outside the transaction, or send after commit",
			Chain:      []ast.CallNode{call},
		})
	}

	// R4 Remote call while holding a lock
	if c.UnderLock {
		out = append(out, Finding{
			Rule:       "R4",
			Level:      llm.RiskHigh,
			EntryID:    entryID,
			Loc:        call.Loc,
			Summary:    "Remote call while holding a lock (long blocking)",
			Suggestion: "Shrink the lock scope; never call remote dependencies under a lock",
			Chain:      []ast.CallNode{call},
		})
	}

	// R7 Remote call without a timeout
	if call.NoTimeout {
		out = append(out, Finding{
			Rule:       "R7",
			Level:      llm.RiskMedium,
			EntryID:    entryID,
			Loc:        call.Loc,
			Summary:    "Remote call without a timeout",
			Suggestion: "Set a context deadline or client timeout to avoid unbounded blocking",
			Chain:      []ast.CallNode{call},
		})
	}

	return out
}

// isMoneyTable 判断表名是否与资金相关。
func isMoneyTable(name string) bool {
	l := strings.ToLower(name)
	if l == "" {
		return false
	}
	for _, k := range []string{"account", "balance", "wallet", "payment", "ledger", "transaction", "fund", "settlement"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// isSensitiveTable 判断表是否敏感：先看命名，再看 AI 语义标注。
func isSensitiveTable(name string, anns map[string]semantic.Annotation) bool {
	if isMoneyTable(name) {
		return true
	}
	if a, ok := anns["table:"+name]; ok {
		l := strings.ToLower(a.Text)
		for _, k := range []string{"money", "payment", "balance", "financial", "pii", "sensitive", "ledger", "资金", "账"} {
			if strings.Contains(l, k) {
				return true
			}
		}
	}
	return false
}

// isSetWithoutTTL 判断是否为无 TTL 的缓存写入（如 redis.Set(..., 0)）。
func isSetWithoutTTL(call ast.CallNode) bool {
	name := strings.ToLower(call.Callee.Name)
	if !strings.HasSuffix(name, ".set") {
		return false
	}
	// 只对缓存/Redis 客户端生效
	if !strings.Contains(name, "redis") && !strings.Contains(name, "cache") &&
		!strings.Contains(name, "rdb") && !strings.HasPrefix(name, "client.") {
		return false
	}
	if len(call.Args) < 3 {
		return false
	}
	last := strings.TrimSpace(call.Args[len(call.Args)-1])
	return last == "0" || last == "0*time.Second" || last == "0 * time.Second"
}
