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
// 方法调用优先按接收者类型匹配，类型未知时回退到方法名。
func reachableFuncs(start ast.Symbol, forward ForwardIndex) []ast.Symbol {
	seen := map[ast.Symbol]bool{start: true}
	queue := []ast.Symbol{start}
	for _, s := range indexTargets(forward, start) {
		if !seen[s] {
			seen[s] = true
			queue = append(queue, s)
		}
	}

	var out []ast.Symbol
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)

		for _, c := range forward[cur] {
			if c.Kind != ast.CallFunc {
				continue
			}
			next := ast.Symbol{Name: ast.LastSegment(c.Callee.Name)}
			if c.Callee.Package != "" {
				next.Package = c.Callee.Package
			}
			targets := indexTargets(forward, next)
			if c.Qualified {
				targets = indexTargetsExact(forward, next)
			}
			for _, t := range targets {
				if !seen[t] {
					seen[t] = true
					queue = append(queue, t)
				}
			}
		}
	}
	return out
}

// indexTargetsExact 只匹配精确键或顶层同名函数（包级调用）。
func indexTargetsExact(forward ForwardIndex, s ast.Symbol) []ast.Symbol {
	if _, ok := forward[s]; ok {
		return []ast.Symbol{s}
	}
	bare := ast.Symbol{Name: s.Name}
	if s.Package != "" {
		if _, ok := forward[bare]; ok {
			return []ast.Symbol{bare}
		}
	}
	if _, ok := forward[bare]; ok {
		return []ast.Symbol{bare}
	}
	return []ast.Symbol{s}
}

// indexTargets 把符号展开为正向索引里的候选键。
func indexTargets(forward ForwardIndex, s ast.Symbol) []ast.Symbol {
	var out []ast.Symbol
	if s.Package != "" {
		if _, ok := forward[s]; ok {
			return []ast.Symbol{s}
		}
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
		// 类型未知时，顶层函数和所有同名方法都要尝试，避免漏掉真实调用链。
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
