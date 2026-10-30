package ast

import (
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// grpcRegisterRe 匹配 gRPC 服务注册函数名：RegisterXxxServer。
var grpcRegisterRe = regexp.MustCompile(`^Register(\w+)Server$`)

// symbolFromDecl 从函数/方法声明提取符号名。
func symbolFromDecl(n *sitter.Node, src []byte) Symbol {
	sym := Symbol{}
	if name := n.ChildByFieldName("name"); name != nil {
		sym.Name = name.Utf8Text(src)
	}
	// 方法的 Package 记录接收者类型，使 ArticleSerializer.Response 与
	// ArticlesSerializer.Response 不再按裸方法名合并。
	if n.Kind() == "method_declaration" {
		sym.Package = receiverType(n, src)
	}
	return sym
}

// receiverType 从方法声明的 receiver 中提取类型名（去掉指针与包前缀）。
func receiverType(n *sitter.Node, src []byte) string {
	recv := n.ChildByFieldName("receiver")
	if recv == nil {
		return ""
	}
	for i := uint(0); i < recv.NamedChildCount(); i++ {
		p := recv.NamedChild(i)
		if p.Kind() != "parameter_declaration" {
			continue
		}
		if typ := p.ChildByFieldName("type"); typ != nil {
			return baseType(typ.Utf8Text(src))
		}
	}
	return ""
}

// baseType 归一化类型文本：*pkg.Type -> Type。
func baseType(t string) string {
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, "*")
	t = strings.TrimPrefix(t, "[]")
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		t = t[i+1:]
	}
	return t
}

// buildTypeEnv 粗略推断函数内变量/参数的类型，用于把方法调用解析到正确的接收者类型。
// 只处理高置信度场景：receiver、参数、复合字面量、new(Type)、var x Type。
func buildTypeEnv(n *sitter.Node, src []byte) map[string]string {
	env := map[string]string{}

	addParams := func(list *sitter.Node) {
		if list == nil {
			return
		}
		for i := uint(0); i < list.NamedChildCount(); i++ {
			p := list.NamedChild(i)
			if p.Kind() != "parameter_declaration" {
				continue
			}
			name := p.ChildByFieldName("name")
			typ := p.ChildByFieldName("type")
			if name != nil && typ != nil {
				env[name.Utf8Text(src)] = baseType(typ.Utf8Text(src))
			}
		}
	}
	if n.Kind() == "method_declaration" {
		addParams(n.ChildByFieldName("receiver"))
	}
	addParams(n.ChildByFieldName("parameters"))

	body := n.ChildByFieldName("body")
	if body == nil {
		return env
	}

	var walk func(*sitter.Node)
	walk = func(m *sitter.Node) {
		switch m.Kind() {
		case "short_var_declaration":
			left := m.ChildByFieldName("left")
			right := m.ChildByFieldName("right")
			if left != nil && right != nil && left.NamedChildCount() > 0 && right.NamedChildCount() > 0 {
				if typ := typeFromExpr(right.NamedChild(0), src); typ != "" {
					env[left.NamedChild(0).Utf8Text(src)] = typ
				}
			}
		case "var_spec":
			name := m.ChildByFieldName("name")
			typ := m.ChildByFieldName("type")
			if name != nil && typ != nil {
				env[name.Utf8Text(src)] = baseType(typ.Utf8Text(src))
			}
		}
		for i := uint(0); i < m.NamedChildCount(); i++ {
			walk(m.NamedChild(i))
		}
	}
	walk(body)
	return env
}

// typeFromExpr 从表达式推断类型：Type{}、&Type{}、new(Type)。
func typeFromExpr(n *sitter.Node, src []byte) string {
	switch n.Kind() {
	case "composite_literal":
		if t := n.ChildByFieldName("type"); t != nil {
			return baseType(strings.TrimSpace(t.Utf8Text(src)))
		}
	case "unary_expression":
		for i := uint(0); i < n.NamedChildCount(); i++ {
			if c := n.NamedChild(i); c.Kind() == "composite_literal" {
				return typeFromExpr(c, src)
			}
		}
	case "call_expression":
		fn := n.ChildByFieldName("function")
		if fn == nil {
			return ""
		}
		name := LastSegment(fn.Utf8Text(src))
		if name == "new" {
			if args := n.ChildByFieldName("arguments"); args != nil && args.NamedChildCount() > 0 {
				return baseType(strings.TrimSpace(args.NamedChild(0).Utf8Text(src)))
			}
			return ""
		}
		// 常见构造器命名：NewXxx() / GetXxx()。
		if strings.HasPrefix(name, "New") && len(name) > 3 {
			return baseType(name[3:])
		}
		if strings.HasPrefix(name, "Get") && len(name) > 3 {
			return baseType(name[3:])
		}
	}
	return ""
}

// funcContext 是每个函数的分析上下文（用于 R7 等函数级信号）。
type funcContext struct {
	noTimeout bool
}

// walkCalls 提取函数体内的所有调用、函数摘要与排查线索。
func walkCalls(tree *sitter.Tree, src []byte) ([]CallNode, []FuncSummary, []Trouble) {
	var calls []CallNode
	var funcs []FuncSummary
	var troubles []Trouble

	root := tree.RootNode()
	for i := uint(0); i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		switch child.Kind() {
		case "function_declaration", "method_declaration":
			caller := symbolFromDecl(child, src)
			body := child.ChildByFieldName("body")
			if body == nil {
				continue
			}
			fctx := funcContext{noTimeout: !hasTimeout(body, src)}
			env := buildTypeEnv(child, src)
			collectCalls(body, src, caller, fctx, env, nil, &calls, &troubles)

			cbs := callbackParams(child.ChildByFieldName("parameters"), src)
			uses, hasLock, hasTx := analyzeBody(body, src, toSet(cbs))
			start := child.StartPosition()
			end := child.EndPosition()
			funcs = append(funcs, FuncSummary{
				Symbol:         caller,
				StartLine:      int(start.Row) + 1,
				EndLine:        int(end.Row) + 1,
				CallbackParams: cbs,
				UsesCallback:   uses,
				HasLock:        hasLock,
				HasTx:          hasTx,
			})
		}
	}
	return calls, funcs, troubles
}

// collectCalls 递归提取调用。callbackOf 记录当前所在的闭包是由哪个包装调用传入的。
func collectCalls(n *sitter.Node, src []byte, caller Symbol, fctx funcContext, env map[string]string, callbackOf []string, calls *[]CallNode, troubles *[]Trouble) {
	if n.Kind() == "call_expression" {
		if c, ok := callFromNode(n, src, caller, env); ok {
			c.Kind = classifyCall(c.Callee.Name)
			c.InLoop = inLoop(n, src)
			c.InTx = inTransaction(n, src) || inManualTx(n, src)
			c.UnderLock = underLockBefore(n, src)
			c.InGoroutine = inGoroutine(n)
			c.Standalone = isStandalone(n)
			c.NoTimeout = fctx.noTimeout
			c.CallbackOf = callbackOf
			*calls = append(*calls, c)
		}
		if t, ok := troubleFromCall(n, src); ok {
			*troubles = append(*troubles, t)
		}

		callee := ""
		if fn := n.ChildByFieldName("function"); fn != nil {
			callee = calleeName(fn, src)
			collectCalls(fn, src, caller, fctx, env, callbackOf, calls, troubles)
		}
		if args := n.ChildByFieldName("arguments"); args != nil {
			for i := uint(0); i < args.NamedChildCount(); i++ {
				arg := args.NamedChild(i)
				if arg.Kind() == "func_literal" {
					if body := arg.ChildByFieldName("body"); body != nil {
						collectCalls(body, src, caller, fctx, env, withCallback(callbackOf, callee), calls, troubles)
					}
				} else {
					collectCalls(arg, src, caller, fctx, env, callbackOf, calls, troubles)
				}
			}
		}
		return
	}

	for i := uint(0); i < n.NamedChildCount(); i++ {
		collectCalls(n.NamedChild(i), src, caller, fctx, env, callbackOf, calls, troubles)
	}
}

// withCallback 返回追加了 callee 的新栈（避免底层数组共享导致的别名问题）。
func withCallback(stack []string, callee string) []string {
	out := make([]string, len(stack)+1)
	copy(out, stack)
	out[len(stack)] = callee
	return out
}

// callFromNode 从 call_expression 提取一个调用节点。
func callFromNode(n *sitter.Node, src []byte, caller Symbol, env map[string]string) (CallNode, bool) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return CallNode{}, false
	}
	name := calleeName(fn, src)
	if name == "" {
		return CallNode{}, false
	}
	p := n.StartPosition()
	callee := Symbol{Name: name}
	qualified := false
	if fn.Kind() == "selector_expression" {
		if op := fn.ChildByFieldName("operand"); op != nil && op.Kind() == "identifier" {
			operand := op.Utf8Text(src)
			if typ := env[operand]; typ != "" {
				callee.Package = typ
			} else if isLowerPackage(operand) {
				// common.Bind / http.Get / users.Register：包级调用
				qualified = true
			}
		}
	}
	return CallNode{
		Caller:    caller,
		Callee:    callee,
		Kind:      CallFunc,
		Loc:       Loc{Line: int(p.Row) + 1, Column: int(p.Column) + 1},
		Args:      argTexts(n, src),
		Qualified: qualified,
	}, true
}

// isLowerPackage 粗略判断标识符是否为包名（包名约定全小写）。
func isLowerPackage(s string) bool {
	return s != "" && s == strings.ToLower(s)
}

// calleeName 提取被调符号名：db.Where -> "db.Where"，queryOrders -> "queryOrders"。
func calleeName(fn *sitter.Node, src []byte) string {
	switch fn.Kind() {
	case "selector_expression":
		op := fn.ChildByFieldName("operand")
		fld := fn.ChildByFieldName("field")
		if op != nil && fld != nil {
			return op.Utf8Text(src) + "." + fld.Utf8Text(src)
		}
	case "identifier", "field_identifier":
		return fn.Utf8Text(src)
	}
	return fn.Utf8Text(src)
}

// argTexts 提取调用实参的源码文本。
func argTexts(n *sitter.Node, src []byte) []string {
	args := n.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	var out []string
	for i := uint(0); i < args.NamedChildCount(); i++ {
		out = append(out, args.NamedChild(i).Utf8Text(src))
	}
	return out
}

// stringLiteral 从字符串字面量节点提取内容（去掉引号/反引号）。
func stringLiteral(n *sitter.Node, src []byte) (string, bool) {
	switch n.Kind() {
	case "interpreted_string_literal", "raw_string_literal":
		s := n.Utf8Text(src)
		if len(s) >= 2 {
			return s[1 : len(s)-1], true
		}
	}
	return "", false
}

// extractImplType 从 "&impl{}"、"&impl{...}"、"impl" 提取实现类型名。
func extractImplType(n *sitter.Node, src []byte) string {
	s := strings.TrimSpace(n.Utf8Text(src))
	s = strings.TrimPrefix(s, "&")
	if i := strings.IndexByte(s, '{'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// classifyCall 按被调符号名粗略分类（HTTP / gRPC / MQ / Lock / Func）。
func classifyCall(callee string) CallKind {
	l := strings.ToLower(callee)
	switch {
	case strings.HasPrefix(l, "http.") || strings.Contains(l, "resty.") ||
		strings.HasSuffix(l, "http.get") || strings.HasSuffix(l, "http.post"):
		return CallHTTP
	case strings.Contains(l, "grpc.") || strings.HasSuffix(l, ".invoke"):
		return CallGRPC
	case strings.Contains(l, "kafka") || strings.Contains(l, "sarama") ||
		strings.HasSuffix(l, ".send") || strings.HasSuffix(l, ".produce"):
		return CallMQ
	case isLockCallee(callee):
		return CallLock
	case isRedisCall(callee):
		return CallRedis
	default:
		return CallFunc
	}
}

// isRedisCall 判断是否为 Redis / 缓存调用（非加锁）。
func isRedisCall(callee string) bool {
	l := strings.ToLower(callee)
	return strings.Contains(l, "redis") || strings.HasPrefix(l, "rdb.") || strings.HasPrefix(l, "cache.")
}

// isLockCallee 判断被调方是否为加锁操作（含 sync.Mutex 与 Redis 分布式锁）。
func isLockCallee(name string) bool {
	l := strings.ToLower(name)
	return strings.HasSuffix(l, ".lock") || strings.HasSuffix(l, ".rlock") ||
		strings.Contains(l, "setnx") || strings.Contains(l, "redsync") ||
		strings.Contains(l, "lockcontext") || strings.HasSuffix(l, ".lockcontext")
}

// inGoroutine 判断调用是否位于 go func(){...} 协程内。
func inGoroutine(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == "go_statement" {
			return true
		}
	}
	return false
}

// isStandalone 判断调用是否为独立表达式语句（返回值未被检查）。
func isStandalone(n *sitter.Node) bool {
	p := n.Parent()
	return p != nil && p.Kind() == "expression_statement"
}

// hasTimeout 判断函数体是否包含超时设置（R7 粗判）。
func hasTimeout(body *sitter.Node, src []byte) bool {
	if body == nil {
		return false
	}
	text := body.Utf8Text(src)
	return strings.Contains(text, "WithTimeout") ||
		strings.Contains(text, "WithDeadline") ||
		strings.Contains(text, "Timeout:") ||
		strings.Contains(text, "SetDeadline") ||
		strings.Contains(text, "TimeoutMicros")
}

// underLockBefore 判断调用点是否在同函数内某个加锁调用之后。
func underLockBefore(n *sitter.Node, src []byte) bool {
	body := enclosingBody(n)
	if body == nil {
		return false
	}
	lockByte, ok := firstLockCall(body, src)
	if !ok {
		return false
	}
	return n.StartByte() > lockByte
}

// enclosingBody 返回最近的函数 / 闭包体。
func enclosingBody(n *sitter.Node) *sitter.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.Kind() {
		case "function_declaration", "method_declaration", "func_literal":
			return p.ChildByFieldName("body")
		}
	}
	return nil
}

// firstLockCall 返回函数体内第一个加锁调用的字节偏移。
func firstLockCall(body *sitter.Node, src []byte) (uint, bool) {
	var found uint
	var ok bool
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if ok {
			return
		}
		if n.Kind() == "call_expression" {
			if fn := n.ChildByFieldName("function"); fn != nil && isLockCallee(calleeName(fn, src)) {
				found = n.StartByte()
				ok = true
				return
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(body)
	return found, ok
}

// callbackParams 返回类型为 func 的参数名（回调参数）。
func callbackParams(params *sitter.Node, src []byte) []string {
	var out []string
	if params == nil {
		return out
	}
	for i := uint(0); i < params.NamedChildCount(); i++ {
		param := params.NamedChild(i)
		typ := param.ChildByFieldName("type")
		if typ == nil {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(typ.Utf8Text(src)), "func") {
			if name := param.ChildByFieldName("name"); name != nil {
				out = append(out, name.Utf8Text(src))
			}
		}
	}
	return out
}

// analyzeBody 分析函数体：是否执行回调、是否有锁、是否有事务。
func analyzeBody(body *sitter.Node, src []byte, callbacks map[string]bool) (usesCallback, hasLock, hasTx bool) {
	if body == nil {
		return
	}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Kind() == "call_expression" {
			name := ""
			if fn := n.ChildByFieldName("function"); fn != nil {
				name = calleeName(fn, src)
			}
			if isLockCallee(name) {
				hasLock = true
			}
			if strings.HasSuffix(name, ".Transaction") || name == "Transaction" {
				hasTx = true
			}
			if args := n.ChildByFieldName("arguments"); args != nil {
				for i := uint(0); i < args.NamedChildCount(); i++ {
					if callbacks[strings.TrimSpace(args.NamedChild(i).Utf8Text(src))] {
						usesCallback = true
					}
				}
			}
			if callbacks[name] {
				usesCallback = true
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(body)
	return
}

func toSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// troubleFromCall 从一次调用识别排查线索（错误码 / 日志模板 / 超时）。
func troubleFromCall(n *sitter.Node, src []byte) (Trouble, bool) {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return Trouble{}, false
	}
	name := calleeName(fn, src)
	lower := strings.ToLower(name)

	firstStr := ""
	if args := n.ChildByFieldName("arguments"); args != nil && args.NamedChildCount() > 0 {
		if s, ok := stringLiteral(args.NamedChild(0), src); ok {
			firstStr = s
		}
	}

	pos := n.StartPosition()
	loc := Loc{Line: int(pos.Row) + 1, Column: int(pos.Column) + 1}

	switch {
	case isErrorCtor(lower):
		if firstStr == "" {
			return Trouble{}, false
		}
		return Trouble{Kind: "err", Key: firstStr, Loc: loc}, true
	case isLogCall(lower):
		if firstStr == "" {
			return Trouble{}, false
		}
		return Trouble{Kind: "log", Key: firstStr, Loc: loc}, true
	case isTimeoutCall(lower):
		return Trouble{Kind: "timeout", Key: name, Loc: loc}, true
	}
	return Trouble{}, false
}

// isErrorCtor 判断是否为错误构造调用（errors.New / fmt.Errorf 等）。
func isErrorCtor(lower string) bool {
	return lower == "errors.new" || lower == "fmt.errorf" ||
		lower == "xerrors.errorf" || lower == "errors.errorf"
}

// isLogCall 判断是否为日志调用（log/slog/logrus/zap，或 *.Errorf / *.Fatalf）。
func isLogCall(lower string) bool {
	if strings.HasPrefix(lower, "fmt.") {
		return false // fmt.Print* 不算日志
	}
	switch {
	case strings.HasPrefix(lower, "log."), strings.HasPrefix(lower, "slog."),
		strings.Contains(lower, "logrus."), strings.Contains(lower, "zap."):
		return true
	case strings.HasSuffix(lower, ".errorf"), strings.HasSuffix(lower, ".fatalf"):
		return true
	}
	return false
}

// isTimeoutCall 判断是否为超时设置调用（context.WithTimeout / WithDeadline）。
func isTimeoutCall(lower string) bool {
	return lower == "context.withtimeout" || lower == "context.withdeadline" ||
		strings.HasSuffix(lower, ".withtimeout") || strings.HasSuffix(lower, ".withdeadline")
}

// inManualTx 判断调用是否位于手动事务内（db.Begin() 之后、Commit/Rollback 之前）。
func inManualTx(n *sitter.Node, src []byte) bool {
	body := enclosingBody(n)
	if body == nil {
		return false
	}
	beginByte, ok := firstCallMatching(body, src, isBeginCall)
	if !ok || n.StartByte() <= beginByte {
		return false
	}
	if endByte, hasEnd := firstCallAfter(body, src, beginByte, isTxEndCall); hasEnd && n.StartByte() >= endByte {
		return false
	}
	return true
}

func isBeginCall(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".begin")
}

func isTxEndCall(name string) bool {
	l := strings.ToLower(name)
	return strings.HasSuffix(l, ".commit") || strings.HasSuffix(l, ".rollback")
}

func firstCallMatching(body *sitter.Node, src []byte, pred func(string) bool) (uint, bool) {
	return firstCallAfter(body, src, 0, pred)
}

// firstCallAfter 返回字节偏移 after 之后第一个满足条件的调用。
func firstCallAfter(body *sitter.Node, src []byte, after uint, pred func(string) bool) (uint, bool) {
	var found uint
	var ok bool
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if ok {
			return
		}
		if n.Kind() == "call_expression" && n.StartByte() > after {
			if fn := n.ChildByFieldName("function"); fn != nil && pred(calleeName(fn, src)) {
				found = n.StartByte()
				ok = true
				return
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(body)
	return found, ok
}
