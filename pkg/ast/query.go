package ast

import (
	"embed"
	"fmt"
	"path"
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// queryFS 内嵌各生态的 tree-sitter 查询规则。
//
//go:embed queries/go/*.scm
var queryFS embed.FS

// Result 是一次文件提取的结构化结果。
type Result struct {
	Entries  []EntryNode
	Calls    []CallNode
	Ops      []DBOp
	Funcs    []FuncSummary
	Troubles []Trouble

	// 路由状态（供 ResolveMiddleware 跨文件汇总）
	uses   []useCall
	groups map[string]groupInfo
}

// QuerySet 是加载好的一组 tree-sitter 查询规则（按生态）。
type QuerySet struct {
	rules []*compiledRule
}

type compiledRule struct {
	file  string
	query *sitter.Query
}

// LoadQuerySet 加载并编译指定规则文件（如 "go/gin.scm"、"go/gorm.scm"）。
// 规则文件由 config.Sniff 根据 go.mod 决定，实现"生态感知"。
func LoadQuerySet(lang *sitter.Language, ruleFiles []string) (*QuerySet, error) {
	qs := &QuerySet{}
	for _, f := range ruleFiles {
		src, err := queryFS.ReadFile(path.Join("queries", f))
		if err != nil {
			return nil, fmt.Errorf("read query %s: %w", f, err)
		}
		q, qerr := sitter.NewQuery(lang, string(src))
		if qerr != nil {
			return nil, fmt.Errorf("compile query %s: %w", f, qerr)
		}
		qs.rules = append(qs.rules, &compiledRule{file: f, query: q})
	}
	return qs, nil
}

// Close 释放所有编译好的 query。
func (qs *QuerySet) Close() {
	for _, r := range qs.rules {
		r.query.Close()
	}
}

// Extract 用已加载的规则提取入口 / 调用链 / 下游操作。
//
// 入口与下游操作由 .scm 规则驱动（生态特定）；调用链用通用 Go 遍历（生态无关）。
func (qs *QuerySet) Extract(path string, tree *sitter.Tree, src []byte) Result {
	var entries []EntryNode
	var ops []DBOp

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	root := tree.RootNode()
	for _, rule := range qs.rules {
		names := rule.query.CaptureNames()
		matches := cursor.Matches(rule.query, root, src)
		for m := matches.Next(); m != nil; m = matches.Next() {
			c := groupCaptures(m, names)
			switch {
			case len(c["http.args"]) > 0:
				if e, ok := httpEntry(c, path, src); ok {
					entries = append(entries, e)
				}
			case len(c["grpc.register"]) > 0:
				if e, ok := grpcEntry(c, path, src); ok {
					entries = append(entries, e)
				}
			case len(c["kafka.method"]) > 0:
				if e, ok := kafkaEntry(c, path, src); ok {
					entries = append(entries, e)
				}
			case len(c["db.method"]) > 0:
				if op, ok := dbOp(c, path, src); ok {
					ops = append(ops, op)
				}
			}
		}
	}

	// 通用调用链 + 函数摘要 + 排查线索（生态无关）
	calls, funcs, troubles := walkCalls(tree, src)
	for i := range calls {
		calls[i].Loc.File = path
	}
	for i := range troubles {
		troubles[i].Loc.File = path
	}
	for i := range funcs {
		funcs[i].File = path
	}

	applyUses, applyGroups := collectRouterState(tree, src, path)

	return Result{
		Entries:  entries,
		Calls:    calls,
		Ops:      ops,
		Funcs:    funcs,
		Troubles: troubles,
		uses:     applyUses,
		groups:   applyGroups,
	}
}

// groupCaptures 按捕获名分组。注意：match 内存会被 Next() 复用，故在循环体内立即消费。
func groupCaptures(m *sitter.QueryMatch, names []string) map[string][]sitter.Node {
	c := make(map[string][]sitter.Node, len(m.Captures))
	for _, cap := range m.Captures {
		c[names[cap.Index]] = append(c[names[cap.Index]], cap.Node)
	}
	return c
}

func first(c map[string][]sitter.Node, name string) *sitter.Node {
	if nodes := c[name]; len(nodes) > 0 {
		return &nodes[0]
	}
	return nil
}

func httpEntry(c map[string][]sitter.Node, path string, src []byte) (EntryNode, bool) {
	methodNode := first(c, "http.method")
	argsNode := first(c, "http.args")
	if methodNode == nil || argsNode == nil || argsNode.NamedChildCount() < 2 {
		return EntryNode{}, false
	}

	p, ok := stringLiteral(argsNode.NamedChild(0), src)
	if !ok {
		return EntryNode{}, false
	}

	n := argsNode.NamedChildCount()
	handler := argsNode.NamedChild(n - 1).Utf8Text(src)

	var middleware []string
	for i := uint(1); i < n-1; i++ {
		middleware = append(middleware, argsNode.NamedChild(i).Utf8Text(src))
	}

	receiver := ""
	if r := first(c, "http.receiver"); r != nil {
		receiver = r.Utf8Text(src)
	}

	method := methodNode.Utf8Text(src)
	if method == "HandleFunc" || method == "Handle" {
		method = "ANY"
	}
	pos := methodNode.StartPosition()

	return EntryNode{
		ID:         fmt.Sprintf("http:%s:%s", method, p),
		Kind:       EntryHTTP,
		Name:       p,
		Handler:    Symbol{Name: handler},
		Receiver:   receiver,
		Middleware: middleware,
		SourceLoc:  Loc{File: path, Line: int(pos.Row) + 1, Column: int(pos.Column) + 1},
	}, true
}

func grpcEntry(c map[string][]sitter.Node, path string, src []byte) (EntryNode, bool) {
	regNode := first(c, "grpc.register")
	if regNode == nil {
		return EntryNode{}, false
	}
	m := grpcRegisterRe.FindStringSubmatch(regNode.Utf8Text(src))
	if m == nil {
		return EntryNode{}, false
	}
	service := m[1]

	implType := ""
	if impl := first(c, "grpc.impl"); impl != nil {
		implType = extractImplType(impl, src)
	}
	pos := regNode.StartPosition()

	return EntryNode{
		ID:        "grpc:" + service,
		Kind:      EntryGRPC,
		Name:      service,
		Handler:   Symbol{Name: implType},
		SourceLoc: Loc{File: path, Line: int(pos.Row) + 1, Column: int(pos.Column) + 1},
		PodHints:  []string{"impl:" + implType},
	}, true
}

func kafkaEntry(c map[string][]sitter.Node, path string, src []byte) (EntryNode, bool) {
	methodNode := first(c, "kafka.method")
	if methodNode == nil {
		return EntryNode{}, false
	}
	name := methodNode.Utf8Text(src)
	pos := methodNode.StartPosition()

	return EntryNode{
		ID:        "kafka:" + name,
		Kind:      EntryKafkaConsumer,
		Name:      name,
		Handler:   Symbol{Name: name},
		SourceLoc: Loc{File: path, Line: int(pos.Row) + 1, Column: int(pos.Column) + 1},
	}, true
}

// gormTerminal 是真正执行数据库操作的 gorm 方法（链式修饰方法不产生 DBOp）。
var gormTerminal = map[string]OpKind{
	"Find": OpRead, "First": OpRead, "Take": OpRead, "Last": OpRead,
	"Scan": OpRead, "Count": OpRead, "Pluck": OpRead,
	"Create": OpWrite, "Save": OpWrite, "Update": OpWrite, "Updates": OpWrite,
	"Delete": OpDelete, "Raw": OpRead, "Exec": OpWrite,
}

// dbOp 由 gorm 调用映射为下游操作（仅终结方法），并做链式分析与 AST 祖先分析。
func dbOp(c map[string][]sitter.Node, path string, src []byte) (DBOp, bool) {
	methodNode := first(c, "db.method")
	callNode := first(c, "db.call")
	if methodNode == nil || callNode == nil {
		return DBOp{}, false
	}

	op, ok := gormTerminal[methodNode.Utf8Text(src)]
	if !ok {
		return DBOp{}, false // Table/Where/Model 等修饰方法不产生操作
	}
	method := methodNode.Utf8Text(src)

	table, where := chainInfo(callNode, src)

	// 原生 SQL：Raw/Exec 的动作类型、WHERE 与表名从 SQL 文本推断
	if method == "Raw" || method == "Exec" {
		op, method, where, table = classifySQL(callNode, src)
	}

	pos := callNode.StartPosition()
	return DBOp{
		Op:         op,
		Method:     method,
		Resource:   table,
		Where:      where,
		Caller:     enclosingFunc(callNode, src),
		InLoop:     inLoop(callNode),
		InTx:       inTransaction(callNode, src) || inManualTx(callNode, src),
		UnderLock:  underLockBefore(callNode, src),
		Standalone: isStandalone(callNode),
		Loc:        Loc{File: path, Line: int(pos.Row) + 1, Column: int(pos.Column) + 1},
	}, true
}

// sqlTableNameRe 从 SQL 文本提取表名（FROM / JOIN / INTO / UPDATE 后的标识符）。
var sqlTableNameRe = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE)\s+([a-zA-Z_][\w]*)`)

// classifySQL 从 Raw/Exec 的 SQL 文本推断操作类型、方法名、WHERE 与表名。
func classifySQL(call *sitter.Node, src []byte) (OpKind, string, string, string) {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return OpRead, "Select", "", ""
	}
	s, ok := stringLiteral(args.NamedChild(0), src)
	if !ok {
		return OpRead, "Select", "", ""
	}

	table := ""
	if m := sqlTableNameRe.FindStringSubmatch(s); m != nil {
		table = m[1]
	}

	u := strings.ToUpper(strings.TrimSpace(s))
	where := ""
	if strings.Contains(u, "WHERE") {
		where = "raw"
	}
	switch {
	case strings.HasPrefix(u, "DELETE"):
		return OpDelete, "Delete", where, table
	case strings.HasPrefix(u, "UPDATE"):
		return OpWrite, "Update", where, table
	case strings.HasPrefix(u, "INSERT"), strings.HasPrefix(u, "REPLACE"):
		return OpWrite, "Create", where, table
	default:
		return OpRead, "Select", where, table
	}
}

// chainInfo 沿 gorm 链式调用向下，提取表名（Table）与 WHERE 条件（Where）。
func chainInfo(call *sitter.Node, src []byte) (table, where string) {
	for node := call; node != nil; {
		fn := node.ChildByFieldName("function")
		if fn == nil || fn.Kind() != "selector_expression" {
			break
		}
		field := fn.ChildByFieldName("field")
		operand := fn.ChildByFieldName("operand")
		if field != nil {
			switch field.Utf8Text(src) {
			case "Table":
				if args := node.ChildByFieldName("arguments"); args != nil && table == "" {
					table = firstStringArg(args, src)
				}
			case "Where":
				if args := node.ChildByFieldName("arguments"); args != nil && where == "" {
					where = firstStringArg(args, src)
				}
			}
		}
		if operand == nil || operand.Kind() != "call_expression" {
			break
		}
		node = operand
	}
	return table, where
}

// enclosingFunc 向上查找最近的函数/方法声明，返回其符号。
func enclosingFunc(n *sitter.Node, src []byte) Symbol {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Kind() {
		case "function_declaration", "method_declaration":
			return symbolFromDecl(p, src)
		}
	}
	return Symbol{}
}

// inLoop 判断节点是否处于 for / range 循环体内。
func inLoop(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == "for_statement" {
			return true
		}
	}
	return false
}

// inTransaction 判断节点是否处于 *.Transaction(func(tx){...}) 闭包内。
func inTransaction(n *sitter.Node, src []byte) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() != "func_literal" {
			continue
		}
		for q := p.Parent(); q != nil; q = q.Parent() {
			if q.Kind() == "call_expression" {
				if isMethodCall(q, "Transaction", src) {
					return true
				}
				break
			}
		}
	}
	return false
}

// isMethodCall 判断 call_expression 的被调方法名是否为 method（如 Transaction）。
func isMethodCall(call *sitter.Node, method string, src []byte) bool {
	f := call.ChildByFieldName("function")
	if f == nil || f.Kind() != "selector_expression" {
		return false
	}
	fld := f.ChildByFieldName("field")
	return fld != nil && fld.Utf8Text(src) == method
}

// firstStringArg 取参数列表里第一个字符串字面量（用于 db.Table("orders") 提取表名）。
func firstStringArg(args *sitter.Node, src []byte) string {
	for i := uint(0); i < args.NamedChildCount(); i++ {
		if s, ok := stringLiteral(args.NamedChild(i), src); ok {
			return s
		}
	}
	return ""
}
