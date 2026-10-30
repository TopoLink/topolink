package ast

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// useCall 是一次 .Use(...) 中间件注册。
type useCall struct {
	receiver string
	file     string
	line     int
	names    []string
}

// groupInfo 是路由分组赋值（g := r.Group(...)）的继承信息。
type groupInfo struct {
	parent string
	file   string
	line   int
}

// ResolveMiddleware 跨文件汇总路由状态并解析每个 HTTP 入口的中间件。
//
// 同文件内按注册顺序（Use 在入口之前才生效）；跨文件无法比较行号，
// 因此对同名接收者采用"全局包含"——适用于 package 级 router/group 变量
// （例如 router 定义在 router.go，中间件注册在 middleware.go）。
func ResolveMiddleware(results []Result) {
	var uses []useCall
	groups := map[string]groupInfo{}
	for _, r := range results {
		uses = append(uses, r.uses...)
		for k, g := range r.groups {
			groups[k] = g
		}
	}

	for i := range results {
		for j := range results[i].Entries {
			e := &results[i].Entries[j]
			if e.Kind != EntryHTTP {
				continue
			}
			e.Middleware = resolveMiddleware(*e, uses, groups)
		}
	}
}

func collectRouterState(tree *sitter.Tree, src []byte, path string) ([]useCall, map[string]groupInfo) {
	var uses []useCall
	groups := map[string]groupInfo{}

	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Kind() == "call_expression" {
			if fn := n.ChildByFieldName("function"); fn != nil && fn.Kind() == "selector_expression" {
				field := fn.ChildByFieldName("field")
				operand := fn.ChildByFieldName("operand")
				if field != nil && operand != nil && field.Utf8Text(src) == "Use" {
					var names []string
					if args := n.ChildByFieldName("arguments"); args != nil {
						for i := uint(0); i < args.NamedChildCount(); i++ {
							names = append(names, args.NamedChild(i).Utf8Text(src))
						}
					}
					uses = append(uses, useCall{
						receiver: operand.Utf8Text(src),
						file:     path,
						line:     int(n.StartPosition().Row) + 1,
						names:    names,
					})
				}
			}
		}

		// g := r.Group(...) / g := r.GroupFunc(...)
		if n.Kind() == "short_var_declaration" {
			left := n.ChildByFieldName("left")
			right := n.ChildByFieldName("right")
			if left != nil && right != nil && left.NamedChildCount() > 0 && right.NamedChildCount() > 0 {
				call := right.NamedChild(0)
				if call.Kind() == "call_expression" {
					if fn := call.ChildByFieldName("function"); fn != nil && fn.Kind() == "selector_expression" {
						field := fn.ChildByFieldName("field")
						operand := fn.ChildByFieldName("operand")
						if field != nil && operand != nil && (field.Utf8Text(src) == "Group" || field.Utf8Text(src) == "GroupFunc") {
							groups[left.NamedChild(0).Utf8Text(src)] = groupInfo{
								parent: operand.Utf8Text(src),
								file:   path,
								line:   int(n.StartPosition().Row) + 1,
							}
						}
					}
				}
			}
		}

		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(tree.RootNode())
	return uses, groups
}

func resolveMiddleware(e EntryNode, uses []useCall, groups map[string]groupInfo) []string {
	seen := map[string]bool{}
	var out []string
	add := func(names []string) {
		for _, name := range names {
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}

	// 内联中间件
	add(e.Middleware)

	// 沿接收者链上溯：收集注册在其之前的 Use（祖先以分组创建行为界）
	recv := e.Receiver
	limit := e.SourceLoc.Line
	visited := map[string]bool{}
	for recv != "" && !visited[recv] {
		visited[recv] = true
		for _, u := range uses {
			if u.receiver != recv {
				continue
			}
			// 同文件才用行号判断先后；跨文件视为全局生效。
			if u.file == e.SourceLoc.File && u.line >= limit {
				continue
			}
			add(u.names)
		}
		g, ok := groups[recv]
		if !ok {
			break
		}
		if g.file == e.SourceLoc.File {
			limit = g.line
		}
		recv = g.parent
	}
	return out
}
