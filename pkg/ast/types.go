// Package ast 定义代码拓扑提取的纯数据类型。
// 设计铁律：本包只负责"结构化数据"的定义，绝不掺杂告警组装逻辑。
package ast

import (
	"fmt"
	"strings"
)

// LastSegment 返回符号名的最后一段（service.GetUser -> GetUser）。
// 方法定义只记录方法名，故把调用点解析到定义时用最后一段匹配。
func LastSegment(s string) string {
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// MiddlewareName 归一化中间件调用文本：Auth(jwtSecret) -> Auth，
// pkg.Auth() -> Auth。原始调用仍保留在 EntryNode.Middleware 中用于展示，
// 但语义单元 ID 与规则匹配使用归一化后的函数名。
func MiddlewareName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = s[:i]
	}
	return LastSegment(s)
}

// Symbol 标识一个代码符号（函数 / 方法）。
type Symbol struct {
	Package string // 包名或命名空间
	Name    string // 符号名
}

// String 返回 "package.Name"。
func (s Symbol) String() string {
	if s.Package == "" {
		return s.Name
	}
	return s.Package + "." + s.Name
}

// MarshalText 使 Symbol 可作为 JSON map key（序列化为 "package.Name"）。
func (s Symbol) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText 从 "package.Name" 或裸名 "Name" 还原 Symbol。
func (s *Symbol) UnmarshalText(text []byte) error {
	str := string(text)
	i := strings.LastIndexByte(str, '.')
	if i < 0 {
		s.Package = ""
		s.Name = str
		return nil
	}
	s.Package = str[:i]
	s.Name = str[i+1:]
	return nil
}

// Loc 源码位置。
type Loc struct {
	File   string
	Line   int
	Column int
}

// EntryKind 入口类型。
type EntryKind string

const (
	EntryHTTP          EntryKind = "HTTP"          // HTTP handler
	EntryGRPC          EntryKind = "GRPC"          // gRPC server method
	EntryKafkaConsumer EntryKind = "KafkaConsumer" // Kafka 消费者
	EntryXXLJob        EntryKind = "XXLJob"        // XXL-Job 定时任务
	EntryCron          EntryKind = "Cron"          // 其他定时任务
)

// EntryNode 一个业务入口（系统对外暴露或被动触发的执行起点）。
type EntryNode struct {
	ID            string // 稳定 ID，如 "http:GET:/api/orders"
	Kind          EntryKind
	Name          string   // 入口名称（路径 / method / topic / job name）
	Handler       Symbol   // 处理函数
	Receiver      string   // HTTP 路由注册的接收者（如 gin 的 r、group 变量）
	EnclosingFunc string   // 注册路由的函数（用于解析函数参数形式的路由组）
	Middleware    []string // 入口挂载的中间件（内联 + .Use 继承）
	SourceLoc     Loc      // 源码位置
	PodHints      []string // Phase 2: 运行单元归属线索
}

// CallKind 调用类型。
type CallKind string

const (
	CallDB         CallKind = "DB"         // 数据库读写
	CallRedis      CallKind = "Redis"      // 缓存/分布式锁
	CallLock       CallKind = "Lock"       // 加锁
	CallMQ         CallKind = "MQ"         // 消息生产
	CallHTTP       CallKind = "HTTP"       // 出站 HTTP
	CallGRPC       CallKind = "GRPC"       // 出站 gRPC
	CallThirdParty CallKind = "ThirdParty" // 第三方 SDK
	CallFunc       CallKind = "Func"       // 普通函数
)

// CallNode 一次调用（构建正向调用链）。
type CallNode struct {
	Caller Symbol
	Callee Symbol
	Kind   CallKind
	Loc    Loc
	Args   []string // 关键实参字面量（表名 / key / topic / URL）

	// 调用点上下文（用于跨函数传播，支撑"封装"场景：
	// 若 B 在循环/事务里调用 A，则 A 内部的操作应继承该上下文）
	InLoop      bool
	InTx        bool
	UnderLock   bool
	InGoroutine bool

	// 附加分析信号
	Standalone bool // 是否为独立表达式语句（R6：写操作返回值未检查）
	NoTimeout  bool // 所属函数未发现超时设置（R7：远程调用无超时）

	// Qualified 表示这是包级调用（common.Bind、http.Get），解析时只匹配
	// 顶层同名函数，不应回退到同名方法。
	Qualified bool

	// CallbackOf 记录"该调用位于哪个包装调用的闭包内"（由内到外的包装被调名）。
	// 例如 withLock(func(){ withTx(func(){ http.Get() }) }) 中，http.Get 的
	// CallbackOf = ["withTx", "withLock"]。用于高阶封装（回调式锁/事务）的上下文传播。
	CallbackOf []string
}

// FuncSummary 函数/方法摘要，用于高阶封装（回调式锁/事务）识别与 diff 影响定位。
type FuncSummary struct {
	Symbol         Symbol
	File           string
	StartLine      int
	EndLine        int
	CallbackParams []string // 类型为 func 的参数名
	UsesCallback   bool     // 体内是否调用/传递了回调参数
	HasLock        bool     // 体内是否有加锁操作
	HasTx          bool     // 体内是否有 .Transaction(...)
}

// Trouble 是一条排查线索（错误码 / 日志模板 / 超时设置）。
type Trouble struct {
	Kind string // err / log / timeout
	Key  string // 错误码、日志模板或超时调用名
	Loc  Loc
}

// OpKind 下游操作类型。
type OpKind string

const (
	OpRead   OpKind = "Read"
	OpWrite  OpKind = "Write"
	OpDelete OpKind = "Delete"
)

// DBOp 下游数据库/存储操作（"下游操作分析"核心）。
type DBOp struct {
	EntryID       string // 归属入口
	Caller        Symbol // 所属函数（用于把 op 关联到入口）
	Op            OpKind
	Method        string   // 原始方法名（如 Create / Update / Delete）
	Resource      string   // 表名 / key 模式
	Fields        []string // 涉及字段
	Where         string   // WHERE 条件近似（或近似 SQL）
	HasWhere      bool     // 是否存在 WHERE（非字面量也算）
	ImplicitWhere bool     // 是否由 GORM 主键隐式限定（Model(实例) / Save）
	InLoop        bool     // 是否在循环内（R1/R2）
	InTx          bool     // 是否在事务内（R3）
	UnderLock     bool     // 是否持锁调用（R4）
	InGoroutine   bool     // 是否在协程内
	Standalone    bool     // 是否为独立语句（R6：写返回值未检查）
	Loc           Loc
}

// ResourceKind 反查索引的资源类型。
type ResourceKind string

const (
	ResourceTable  ResourceKind = "table"  // 表名
	ResourceCache  ResourceKind = "key"    // 缓存 key
	ResourceConfig ResourceKind = "config" // 配置 key
	ResourceMethod ResourceKind = "method" // 公共方法
	ResourceTopic  ResourceKind = "topic"  // Kafka topic
)

// ResourceKey 反查索引的资源标识。
type ResourceKey struct {
	Kind ResourceKind
	Name string
}

// String 返回 "kind:name"。
func (r ResourceKey) String() string {
	return string(r.Kind) + ":" + r.Name
}

// MarshalText 使 ResourceKey 可作为 JSON map key（序列化为 "kind:name"）。
func (r ResourceKey) MarshalText() ([]byte, error) {
	return []byte(r.String()), nil
}

// UnmarshalText 从 "kind:name" 还原 ResourceKey（按第一个冒号切分，保留 name 中的冒号）。
func (r *ResourceKey) UnmarshalText(text []byte) error {
	s := string(text)
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return fmt.Errorf("invalid resource key %q", s)
	}
	r.Kind = ResourceKind(s[:i])
	r.Name = s[i+1:]
	return nil
}

// Ref 反向引用：某个资源被哪个入口、以何种方式使用。
type Ref struct {
	EntryID string
	Op      OpKind
	Loc     Loc
}
