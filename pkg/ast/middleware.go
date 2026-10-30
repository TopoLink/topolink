package ast

import (
	"path"
	"strings"

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
	prefix string
}

// groupCall 是一次把路由组作为参数传入的注册调用：
//
//	ArticlesRegister(v1.Group("/articles"))
type groupCall struct {
	callee string // ArticlesRegister
	parent string // v1
	prefix string // /articles
	file   string
	line   int
}

// ResolveMiddleware 跨文件汇总路由状态并解析每个 HTTP 入口的中间件与真实路径。
//
// 支持两种常见写法：
//   - g := r.Group("/x"); g.GET(...)
//   - Register(v1.Group("/x")); 函数内 router.GET(...)
//
// 同文件内按注册顺序判断 Use 是否生效；分组调用与 Use 在同一文件时也按行号判断。
func ResolveMiddleware(results []Result) {
	var uses []useCall
	groups := map[string]groupInfo{}
	routerParams := map[string]string{}
	var groupCalls []groupCall

	for _, r := range results {
		uses = append(uses, r.uses...)
		for k, g := range r.groups {
			groups[k] = g
		}
		for k, v := range r.routerParams {
			routerParams[k] = v
		}
		groupCalls = append(groupCalls, r.groupCalls...)
	}

	// 把“函数参数形式的 router”解析成 groupInfo：
	//   func ArticlesRegister(router *gin.RouterGroup)
	//   ArticlesRegister(v1.Group("/articles"))
	// → ArticlesRegister:router => {parent: v1, prefix: /articles, ...}
	paramGroups := map[string]groupInfo{}
	for _, c := range groupCalls {
		param, ok := routerParams[c.callee]
		if !ok {
			continue
		}
		paramGroups[c.callee+":"+param] = groupInfo{
			parent: c.parent,
			file:   c.file,
			line:   c.line,
			prefix: c.prefix,
		}
	}

	for i := range results {
		for j := range results[i].Entries {
			e := &results[i].Entries[j]
			if e.Kind != EntryHTTP {
				continue
			}
			e.Middleware = resolveMiddleware(*e, uses, groups, paramGroups)

			full := normalizePath(routePath(*e, groups, paramGroups))
			if full != e.Name {
				method := httpMethod(e.ID)
				e.Name = full
				e.ID = "http:" + method + ":" + full
			}
		}
	}

	dedupeEntries(results)
}

// routePath 沿接收者链拼接 Group 前缀。
func routePath(e EntryNode, groups, paramGroups map[string]groupInfo) string {
	var parts []string

	recv := ""
	if g, ok := receiverGroup(e, groups, paramGroups); ok {
		if g.prefix != "" {
			parts = append(parts, g.prefix)
		}
		recv = g.parent
	} else {
		recv = e.Receiver
	}

	visited := map[string]bool{}
	for recv != "" && !visited[recv] {
		visited[recv] = true
		g, ok := groups[recv]
		if !ok {
			break
		}
		if g.prefix != "" {
			parts = append(parts, g.prefix)
		}
		recv = g.parent
	}

	if len(parts) == 0 {
		return e.Name
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	parts = append(parts, e.Name)
	full := path.Join(parts...)
	if !strings.HasPrefix(full, "/") {
		full = "/" + full
	}
	return full
}

// receiverGroup 先查普通分组变量，再查“函数参数形式”的分组。
func receiverGroup(e EntryNode, groups, paramGroups map[string]groupInfo) (groupInfo, bool) {
	if g, ok := groups[e.Receiver]; ok {
		return g, true
	}
	if e.EnclosingFunc != "" {
		if g, ok := paramGroups[e.EnclosingFunc+":"+e.Receiver]; ok {
			return g, true
		}
	}
	return groupInfo{}, false
}

// httpMethod 从 "http:GET:/path" 中取出 GET。
func httpMethod(id string) string {
	s := strings.TrimPrefix(id, "http:")
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i]
	}
	return ""
}

func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

func collectRouterState(tree *sitter.Tree, src []byte, file string) ([]useCall, map[string]groupInfo, map[string]string, []groupCall) {
	var uses []useCall
	groups := map[string]groupInfo{}
	routerParams := map[string]string{}
	var groupCalls []groupCall

	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "call_expression":
			fn := n.ChildByFieldName("function")
			if fn == nil {
				break
			}
			// .Use(...)
			if fn.Kind() == "selector_expression" {
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
						file:     file,
						line:     int(n.StartPosition().Row) + 1,
						names:    names,
					})
				}
			}
			// Register(v1.Group("/prefix"))
			args := n.ChildByFieldName("arguments")
			if args != nil {
				for i := uint(0); i < args.NamedChildCount(); i++ {
					arg := args.NamedChild(i)
					if arg.Kind() != "call_expression" {
						continue
					}
					inner := arg.ChildByFieldName("function")
					if inner == nil || inner.Kind() != "selector_expression" {
						continue
					}
					field := inner.ChildByFieldName("field")
					operand := inner.ChildByFieldName("operand")
					if field == nil || operand == nil {
						continue
					}
					if field.Utf8Text(src) != "Group" && field.Utf8Text(src) != "GroupFunc" {
						continue
					}
					groupCalls = append(groupCalls, groupCall{
						callee: LastSegment(fn.Utf8Text(src)),
						parent: operand.Utf8Text(src),
						prefix: firstStringArg(arg.ChildByFieldName("arguments"), src),
						file:   file,
						line:   int(n.StartPosition().Row) + 1,
					})
				}
			}

		case "short_var_declaration":
			// g := r.Group(...) / g := r.GroupFunc(...)
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
								file:   file,
								line:   int(n.StartPosition().Row) + 1,
								prefix: firstStringArg(call.ChildByFieldName("arguments"), src),
							}
						}
					}
				}
			}

		case "function_declaration", "method_declaration":
			// func ArticlesRegister(router *gin.RouterGroup)
			name := n.ChildByFieldName("name")
			params := n.ChildByFieldName("parameters")
			if name != nil && params != nil {
				for i := uint(0); i < params.NamedChildCount(); i++ {
					p := params.NamedChild(i)
					if p.Kind() != "parameter_declaration" {
						continue
					}
					typ := p.ChildByFieldName("type")
					if typ == nil || !strings.Contains(typ.Utf8Text(src), "RouterGroup") {
						continue
					}
					if pn := p.ChildByFieldName("name"); pn != nil {
						routerParams[name.Utf8Text(src)] = pn.Utf8Text(src)
					}
				}
			}
		}

		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(tree.RootNode())
	return uses, groups, routerParams, groupCalls
}

func resolveMiddleware(e EntryNode, uses []useCall, groups, paramGroups map[string]groupInfo) []string {
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

	recv := e.Receiver
	limitFile := e.SourceLoc.File
	limitLine := e.SourceLoc.Line
	visited := map[string]bool{}

	for recv != "" && !visited[recv] {
		visited[recv] = true
		for _, u := range uses {
			if u.receiver != recv {
				continue
			}
			// 同文件才用行号判断先后；跨文件视为全局生效。
			if u.file == limitFile && u.line >= limitLine {
				continue
			}
			add(u.names)
		}

		g, ok := groups[recv]
		if !ok && e.EnclosingFunc != "" {
			g, ok = paramGroups[e.EnclosingFunc+":"+recv]
		}
		if !ok {
			break
		}
		limitFile = g.file
		limitLine = g.line
		recv = g.parent
	}
	return out
}

// dedupeEntries 合并同一 method+path 的重复注册（如 /x 与 /x/）。
func dedupeEntries(results []Result) {
	seen := map[string]*EntryNode{}
	for i := range results {
		out := results[i].Entries[:0]
		for j := range results[i].Entries {
			e := &results[i].Entries[j]
			if e.Kind != EntryHTTP {
				out = append(out, *e)
				continue
			}
			if first, ok := seen[e.ID]; ok {
				first.Middleware = mergeUnique(first.Middleware, e.Middleware)
				continue
			}
			seen[e.ID] = e
			out = append(out, *e)
		}
		results[i].Entries = out
	}
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if s != "" && !seen[s] {
			seen[s] = true
			a = append(a, s)
		}
	}
	return a
}
