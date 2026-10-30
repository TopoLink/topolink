package engine

// ruleCatalog 把内部规则编号映射为稳定、可读的名称。
// 编号（R1..R7）保留为稳定的内部标识与配置别名；对外展示用名称。
var ruleCatalog = map[string]string{
	"R1":  "query-in-loop",
	"R2":  "write-in-loop",
	"R3":  "remote-in-transaction",
	"R4":  "remote-under-lock",
	"R5":  "write-without-where",
	"R6":  "unchecked-write",
	"R7":  "missing-timeout",
	"R8":  "missing-auth",
	"R9":  "money-write",
	"R10": "cache-no-ttl",
	"R11": "weak-auth",
}

// RuleName 返回规则的可读名称；未知编号回退为原编号。
func RuleName(code string) string {
	if n, ok := ruleCatalog[code]; ok {
		return n
	}
	return code
}
