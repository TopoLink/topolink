// Package llm 封装大模型客户端，提供通用接口。
// 设计铁律：必须接口化，便于零成本切换 OpenAI / Claude / Qwen / 私有模型，并方便 Mock。
package llm

import (
	"context"

	"github.com/topolink/topolink/pkg/ast"
	"github.com/topolink/topolink/pkg/meta"
)

// RiskLevel 风险等级（S0 致命 ~ S3 低危）。
type RiskLevel string

const (
	RiskFatal  RiskLevel = "S0"
	RiskHigh   RiskLevel = "S1"
	RiskMedium RiskLevel = "S2"
	RiskLow    RiskLevel = "S3"
)

// Label 返回人类可读的等级名（critical / high / medium / low）。
func (l RiskLevel) Label() string {
	switch l {
	case RiskFatal:
		return "critical"
	case RiskHigh:
		return "high"
	case RiskMedium:
		return "medium"
	default:
		return "low"
	}
}

// RiskReport L3 深度定性结果。
type RiskReport struct {
	Level      RiskLevel
	Summary    string // 业务影响摘要
	Suggestion string // 修复建议
}

// RiskAnalyzer 大模型风险分析接口（L3）。
// 输入：命中高危的 AST 链路 + 业务标签；输出：风险定性。
type RiskAnalyzer interface {
	Analyze(ctx context.Context, chain []ast.CallNode, tags []meta.BusinessTag) (*RiskReport, error)
}

// Noop 是 Phase 1 的默认实现（L3 默认关闭，不调用任何模型）。
type Noop struct{}

// Analyze 始终返回低危，不产生任何成本。
func (Noop) Analyze(context.Context, []ast.CallNode, []meta.BusinessTag) (*RiskReport, error) {
	return &RiskReport{Level: RiskLow}, nil
}
