package engine

import (
	"strings"

	"github.com/topolink/topolink/pkg/semantic"
)

// Policy 是用户级策略（来自 .pr-risk.yml），全部可选。
// 设计上由 cmd 层把 config.File 翻译成 Policy，engine 不依赖 config，避免反向耦合。
type Policy struct {
	DisabledRules   map[string]bool // key: 规则码（R8）或语义名（missing-auth）
	PublicPaths     map[string]bool // 追加的公开路径首段
	SensitiveTables []string        // 追加的敏感表关键词
	IgnoredTables   []string        // 忽略的表前缀（不触发 R9）
}

// ruleEnabled 判断规则是否启用（支持规则码与语义名两种写法，大小写不敏感）。
func (p Policy) ruleEnabled(rule string) bool {
	if len(p.DisabledRules) == 0 {
		return true
	}
	return !p.DisabledRules[strings.ToLower(rule)] && !p.DisabledRules[strings.ToLower(RuleName(rule))]
}

// isPublicPath 在内置公开路径基础上叠加用户追加项。
func (p Policy) isPublicPath(name string) bool {
	if isPublicPath(name) {
		return true
	}
	if len(p.PublicPaths) == 0 {
		return false
	}
	segs := strings.Split(strings.Trim(strings.ToLower(name), "/"), "/")
	if len(segs) == 0 || segs[0] == "" {
		return false
	}
	return p.PublicPaths[segs[0]]
}

// isSensitiveTable 先应用用户"忽略前缀"，再应用"追加敏感关键词"，最后回退内置启发式。
func (p Policy) isSensitiveTable(name string, anns map[string]semantic.Annotation) bool {
	lower := strings.ToLower(name)
	for _, k := range p.IgnoredTables {
		if k != "" && strings.HasPrefix(lower, strings.ToLower(k)) {
			return false
		}
	}
	for _, k := range p.SensitiveTables {
		if k != "" && strings.Contains(lower, strings.ToLower(k)) {
			return true
		}
	}
	return isSensitiveTable(name, anns)
}
