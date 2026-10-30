// Package engine 组合数据并执行过滤漏斗 + 两个查询入口。
// 设计铁律：pkg/engine 负责"组合 + 漏斗"，不负责"提取"（那是 pkg/ast 的事）。
package engine

import (
	"context"
	"strings"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/index"
	"github.com/topolink/topolink/pkg/llm"
	"github.com/topolink/topolink/pkg/meta"
	"github.com/topolink/topolink/pkg/semantic"
)

// Finding 一条告警 / 发现。
type Finding struct {
	Rule       string // 命中的规则编号（R1..R7）
	Level      llm.RiskLevel
	EntryID    string
	Loc        ast.Loc
	Summary    string
	Suggestion string
	Chain      []ast.CallNode
}

// Engine 核心业务引擎。
type Engine struct {
	analyzer    llm.RiskAnalyzer
	meta        meta.MetadataSource
	annotations map[string]semantic.Annotation
	policy      Policy
}

// New 组装引擎。Phase 1 传入 noop 实现，Phase 2 换成真实 LLM / 元数据源。
func New(analyzer llm.RiskAnalyzer, m meta.MetadataSource) *Engine {
	return &Engine{analyzer: analyzer, meta: m}
}

// WithAnnotations 注入 AI 语义标注（按单元 ID，如 middleware:Auth / table:accounts）。
func (e *Engine) WithAnnotations(anns map[string]semantic.Annotation) *Engine {
	e.annotations = anns
	return e
}

// WithPolicy 注入用户策略（规则开关 / 公开路径 / 表关键词）。
func (e *Engine) WithPolicy(p Policy) *Engine {
	e.policy = p
	return e
}

// execCtx 是沿调用链传播的执行上下文。
type execCtx struct {
	InLoop      bool
	InTx        bool
	UnderLock   bool
	InGoroutine bool
}

func (c execCtx) merge(o execCtx) execCtx {
	return execCtx{
		InLoop:      c.InLoop || o.InLoop,
		InTx:        c.InTx || o.InTx,
		UnderLock:   c.UnderLock || o.UnderLock,
		InGoroutine: c.InGoroutine || o.InGoroutine,
	}
}

func ctxFromCall(c ast.CallNode) execCtx {
	return execCtx{InLoop: c.InLoop, InTx: c.InTx, UnderLock: c.UnderLock, InGoroutine: c.InGoroutine}
}

func ctxFromOp(op ast.DBOp) execCtx {
	return execCtx{InLoop: op.InLoop, InTx: op.InTx, UnderLock: op.UnderLock, InGoroutine: op.InGoroutine}
}

// resolveCallee 把被调符号解析成"定义名"，用于在正向调用图里查找。
// 仅对普通函数/方法调用做 last-segment 归一（外部 HTTP/gRPC/MQ/Lock 调用不归一，避免误连）。
func resolveCallee(call ast.CallNode) ast.Symbol {
	if call.Kind == ast.CallFunc {
		name := ast.LastSegment(call.Callee.Name)
		if call.Callee.Package != "" {
			return ast.Symbol{Package: call.Callee.Package, Name: name}
		}
		return ast.Symbol{Name: name}
	}
	return call.Callee
}

// forwardTargetsExact 只匹配精确键或顶层同名函数（包级调用）。
func forwardTargetsExact(forward index.ForwardIndex, s ast.Symbol) []ast.Symbol {
	if _, ok := forward[s]; ok {
		return []ast.Symbol{s}
	}
	bare := ast.Symbol{Name: s.Name}
	if _, ok := forward[bare]; ok {
		return []ast.Symbol{bare}
	}
	return []ast.Symbol{s}
}

// forwardTargets 把调用目标展开为正向索引里的候选符号。
// 优先精确匹配（含接收者类型），其次裸名，最后按方法名兜底。
func forwardTargets(forward index.ForwardIndex, s ast.Symbol) []ast.Symbol {
	var out []ast.Symbol
	if s.Package != "" {
		if _, ok := forward[s]; ok {
			return []ast.Symbol{s}
		}
		// 已知接收者类型但精确方法没有出边：只有同名方法唯一时才回退
		// （例如嵌入字段提升的 isFavoriteBy），否则只认精确类型，
		// 避免 TagSerializer.Response 被误当成 ArticlesSerializer.Response。
		var matches []ast.Symbol
		for k := range forward {
			if k.Name == s.Name {
				matches = append(matches, k)
			}
		}
		if len(matches) == 1 {
			return matches
		}
		return []ast.Symbol{s}
	} else {
		if _, ok := forward[s]; ok {
			out = append(out, s)
		}
		for k := range forward {
			if k.Name == s.Name && k.Package != "" {
				out = append(out, k)
			}
		}
	}
	if len(out) == 0 {
		out = append(out, s)
	}
	return out
}

// wrapperInfo 描述一个"高阶包装函数"建立的上下文。
type wrapperInfo struct {
	lock bool
	tx   bool
}

// buildWrappers 从函数摘要中识别回调式锁/事务包装函数。
// 条件：有 func 类型参数、体内确实执行了回调、且体内有加锁或开启事务。
func buildWrappers(funcs []ast.FuncSummary) map[string]wrapperInfo {
	m := make(map[string]wrapperInfo)
	for _, f := range funcs {
		if len(f.CallbackParams) == 0 || !f.UsesCallback {
			continue
		}
		if f.HasLock || f.HasTx {
			m[f.Symbol.Name] = wrapperInfo{lock: f.HasLock, tx: f.HasTx}
		}
	}
	return m
}

// callbackContext 根据调用所在的包装闭包链，推导其继承的锁/事务上下文。
// 优先用函数摘要精确判断（支持跨包名 last-segment 匹配），未知包装名用命名约定兜底。
func callbackContext(call ast.CallNode, wrappers map[string]wrapperInfo) execCtx {
	var c execCtx
	for _, w := range call.CallbackOf {
		info, ok := wrappers[w]
		if !ok {
			info, ok = wrappers[ast.LastSegment(w)]
		}
		if ok {
			c.UnderLock = c.UnderLock || info.lock
			c.InTx = c.InTx || info.tx
			continue
		}
		l := strings.ToLower(w)
		if strings.Contains(l, "lock") {
			c.UnderLock = true
		}
		if strings.HasSuffix(l, "tx") || strings.Contains(l, "transaction") {
			c.InTx = true
		}
	}
	return c
}

// Run 执行漏斗过滤：L1 本地 AST 规则（含跨函数上下文传播）。
func (e *Engine) Run(ctx context.Context, idx *index.Index) ([]Finding, error) {
	var findings []Finding
	seen := make(map[string]bool)
	wrappers := buildWrappers(idx.Funcs)

	for _, entry := range idx.Entries {
		if entry.Kind == ast.EntryHTTP && !e.policy.isPublicPath(entry.Name) && !hasAuthMiddleware(entry.Middleware, e.annotations) {
			addFinding(&findings, seen, Finding{
				Rule:       "R8",
				Level:      llm.RiskHigh,
				EntryID:    entry.ID,
				Loc:        entry.SourceLoc,
				Summary:    "HTTP entry has no authentication/authorization middleware",
				Suggestion: "Attach an auth middleware to the route or its router group",
			})
		}

		// R11 auth middleware that does not actually block (annotation effects show no abort/deny)
		if entry.Kind == ast.EntryHTTP {
			for _, mw := range entry.Middleware {
				name := ast.MiddlewareName(mw)
				if !isAuthMw(mw, name, e.annotations) {
					continue
				}
				if blocks, known := authMiddlewareBlocks(name, e.annotations); known && !blocks {
					addFinding(&findings, seen, Finding{
						Rule:       "R11",
						Level:      llm.RiskMedium,
						EntryID:    entry.ID,
						Loc:        entry.SourceLoc,
						Summary:    "Auth middleware \"" + name + "\" does not appear to abort/deny on failure",
						Suggestion: "Make the auth middleware return/abort on invalid credentials instead of only logging",
					})
				}
			}
		}

		eff := effectiveContexts(entry.Handler, idx.Forward)

		for _, op := range idx.Ops {
			base, ok := eff[op.Caller]
			if !ok {
				continue
			}
			final := base.merge(ctxFromOp(op))
			for _, f := range evaluateDBRules(entry.ID, op, final, e.annotations, e.policy) {
				addFinding(&findings, seen, f)
			}
		}

		for _, call := range idx.Calls {
			base, ok := eff[call.Caller]
			if !ok {
				continue
			}
			final := base.merge(ctxFromCall(call)).merge(callbackContext(call, wrappers))
			for _, f := range evaluateCallRules(entry.ID, call, final) {
				addFinding(&findings, seen, f)
			}
		}
	}

	// 应用用户规则开关（R 码或语义名均可）
	if len(e.policy.DisabledRules) > 0 {
		enabled := findings[:0]
		for _, f := range findings {
			if e.policy.ruleEnabled(f.Rule) {
				enabled = append(enabled, f)
			}
		}
		findings = enabled
	}

	return findings, nil
}

// effectiveContexts 从入口 handler 出发，带上下文 BFS，返回每个可达函数的有效上下文。
func effectiveContexts(start ast.Symbol, forward index.ForwardIndex) map[ast.Symbol]execCtx {
	eff := map[ast.Symbol]execCtx{start: {}}
	reached := map[ast.Symbol]bool{start: true}
	queue := []ast.Symbol{start}

	// 入口 handler 可能是方法（h.ListOrders），其索引键带接收者类型；
	// 先按名字找到候选起点。
	for _, s := range forwardTargets(forward, start) {
		if !reached[s] {
			reached[s] = true
			eff[s] = execCtx{}
			queue = append(queue, s)
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		curCtx := eff[cur]

		for _, call := range forward[cur] {
			edge := curCtx.merge(ctxFromCall(call))
			targets := forwardTargets(forward, resolveCallee(call))
			if call.Qualified {
				targets = forwardTargetsExact(forward, resolveCallee(call))
			}
			for _, next := range targets {
				existing := eff[next]
				merged := existing.merge(edge)

				if !reached[next] {
					reached[next] = true
					eff[next] = merged
					queue = append(queue, next)
				} else if merged != existing {
					// 上下文单调增长，必然收敛
					eff[next] = merged
					queue = append(queue, next)
				}
			}
		}
	}
	return eff
}

// addFinding 去重后加入结果。
func addFinding(dst *[]Finding, seen map[string]bool, f Finding) bool {
	key := f.Rule + "|" + f.EntryID + "|" + f.Loc.File + "|" + itoa(f.Loc.Line)
	if seen[key] {
		return false
	}
	seen[key] = true
	*dst = append(*dst, f)
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// isAuthMw 判断中间件是否属于鉴权：先看命名，再看 AI 标注文本。
func isAuthMw(mw, name string, anns map[string]semantic.Annotation) bool {
	if isAuthName(mw) {
		return true
	}
	if a, ok := anns["middleware:"+name]; ok && isAuthText(a.Text) {
		return true
	}
	return false
}

// authMiddlewareBlocks 依据 AI 的分支效果判断中间件是否会拦截。
// known=false 表示没有可用标注，无法判断。
func authMiddlewareBlocks(name string, anns map[string]semantic.Annotation) (blocks bool, known bool) {
	a, ok := anns["middleware:"+name]
	if !ok || len(a.Effects) == 0 {
		return false, false
	}
	for _, ef := range a.Effects {
		l := strings.ToLower(ef.Action)
		if strings.Contains(l, "abort") || strings.Contains(l, "reject") || strings.Contains(l, "deny") ||
			strings.Contains(l, "stop") || strings.Contains(l, "block") ||
			strings.Contains(l, "401") || strings.Contains(l, "403") || strings.Contains(l, "return") {
			return true, true
		}
	}
	return false, true
}

// hasAuthMiddleware 判断中间件是否包含鉴权：先看命名，再看 AI 语义标注。
func hasAuthMiddleware(mws []string, anns map[string]semantic.Annotation) bool {
	for _, mw := range mws {
		if isAuthName(mw) {
			return true
		}
		name := ast.MiddlewareName(mw)
		if a, ok := anns["middleware:"+name]; ok && isAuthText(a.Text) {
			return true
		}
	}
	return false
}

func isAuthName(mw string) bool {
	l := strings.ToLower(mw)
	for _, k := range []string{"auth", "jwt", "permission", "acl", "rbac", "casbin", "login", "session", "token"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

func isAuthText(text string) bool {
	l := strings.ToLower(text)
	for _, k := range []string{"auth", "jwt", "permission", "tenant", "authorize", "authenticate", "rbac", "acl"} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// publicPathSegments 是常见"无需鉴权"路径的首段。
// 用首段精确匹配（而非前缀），避免 /health-records、/login-history 这类
// 敏感接口被误判为公开路径。
var publicPathSegments = map[string]bool{
	"health": true, "healthz": true, "ping": true, "metrics": true,
	"ready": true, "live": true, "readyz": true, "livez": true,
	"version": true, "login": true, "signin": true, "register": true,
	"signup": true, "oauth": true, "callback": true, "webhook": true,
	"webhooks": true, "docs": true, "swagger": true, "openapi": true,
	"favicon.ico": true, "robots.txt": true,
}

// isPublicPath 判断是否为常见公开路径（不要求鉴权）。
func isPublicPath(name string) bool {
	segs := strings.Split(strings.Trim(strings.ToLower(name), "/"), "/")
	if len(segs) == 0 || segs[0] == "" {
		return false
	}
	return publicPathSegments[segs[0]]
}

// Impact 影响面查询（改前）：改某资源会波及哪些入口。
func (e *Engine) Impact(idx *index.Index, key ast.ResourceKey) []ast.Ref {
	return idx.Reverse[key]
}

// Trouble 排查查询（改中）：错误码/日志/超时 -> 源码位置。
func (e *Engine) Trouble(idx *index.Index, key string) []ast.Loc {
	return idx.Trouble[key]
}
