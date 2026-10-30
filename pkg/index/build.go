package index

import (
	"github.com/topolink/topolink/pkg/ast"
)

// Build 从提取结果构建索引：正向调用链、反查索引，并把 DBOp 关联到入口。
//
// 关联方式：从每个入口的 handler 出发，沿正向调用链 BFS，得到可达函数集合；
// 落在可达函数内的 DBOp 即归属该入口（第一个可达入口）。
func Build(entries []ast.EntryNode, calls []ast.CallNode, ops []ast.DBOp, funcs []ast.FuncSummary, troubles []ast.Trouble) *Index {
	idx := New()
	idx.Entries = dedupEntries(entries)
	idx.Calls = calls
	idx.Funcs = funcs

	// 正向索引：caller -> calls
	for _, c := range calls {
		idx.Forward[c.Caller] = append(idx.Forward[c.Caller], c)
	}

	// 函数 -> 入口 ID 映射（BFS 可达性）
	entryOf := make(map[ast.Symbol][]string)
	for _, e := range idx.Entries {
		for _, fn := range reachableFuncs(e.Handler, idx.Forward) {
			entryOf[fn] = append(entryOf[fn], e.ID)
		}
	}
	idx.FuncEntries = entryOf

	// 方法反查索引：代码库内定义的方法/函数 -> 使用它的入口（"改这个公共方法影响谁"）
	defined := make(map[string]bool, len(funcs))
	for _, f := range funcs {
		defined[f.Symbol.Name] = true
	}
	for fn, ids := range entryOf {
		if !defined[fn.Name] {
			continue
		}
		key := ast.ResourceKey{Kind: ast.ResourceMethod, Name: fn.Name}
		for _, id := range ids {
			idx.Reverse[key] = append(idx.Reverse[key], ast.Ref{EntryID: id})
		}
	}

	// 填充 DBOp.EntryID，并建反查索引（table -> op refs）
	for _, op := range ops {
		if ids := entryOf[op.Caller]; len(ids) > 0 {
			op.EntryID = ids[0]
		}
		idx.Ops = append(idx.Ops, op)

		if op.Resource != "" {
			key := ast.ResourceKey{Kind: ast.ResourceTable, Name: op.Resource}
			idx.Reverse[key] = append(idx.Reverse[key], ast.Ref{
				EntryID: op.EntryID,
				Op:      op.Op,
				Loc:     op.Loc,
			})
		}
	}

	// 排查索引：err:/log:/timeout: -> 源码位置
	for _, t := range troubles {
		key := t.Kind + ":" + t.Key
		idx.Trouble[key] = append(idx.Trouble[key], t.Loc)
	}

	return idx
}

// dedupEntries 按 ID 去重入口（多个生态规则可能命中同一入口）。
func dedupEntries(entries []ast.EntryNode) []ast.EntryNode {
	seen := make(map[string]bool, len(entries))
	out := make([]ast.EntryNode, 0, len(entries))
	for _, e := range entries {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	return out
}

// reachableFuncs 从起点函数沿正向调用链 BFS，返回所有可达函数符号（含自身）。
// 对普通调用做 last-segment 归一，使方法调用（service.GetUser）能接上定义（GetUser）。
func reachableFuncs(start ast.Symbol, forward ForwardIndex) []ast.Symbol {
	seen := map[ast.Symbol]bool{start: true}
	queue := []ast.Symbol{start}

	var out []ast.Symbol
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)

		for _, c := range forward[cur] {
			next := c.Callee
			if c.Kind == ast.CallFunc {
				next = ast.Symbol{Name: ast.LastSegment(c.Callee.Name)}
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return out
}
