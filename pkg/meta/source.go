// Package meta 定义业务元数据源接口（YApi / proto 注释 / TiDB / XXL）。
// Phase 1 只定义接口，Phase 2 接入真实实现（每家私有部署不同，故必须接口化）。
package meta

import (
	"context"

	"github.com/topolink/topolink/pkg/ast"
)

// BusinessTag 业务标签（补全"这个接口/表是做什么业务的"）。
type BusinessTag struct {
	Kind       string  // 来源：yapi / proto / tidb / xxl
	Value      string  // 标题 / 描述 / 注释
	Confidence float64 // 0..1，证据充分程度
}

// MetadataSource 业务元数据源接口。
// 不同公司用不同的 YApi 部署、文档平台，故必须接口化。
type MetadataSource interface {
	Fetch(ctx context.Context, ref ast.ResourceKey) ([]BusinessTag, error)
}

// Noop 是 Phase 1 的默认实现（不拉取任何元数据）。
type Noop struct{}

// Fetch 始终返回空。
func (Noop) Fetch(context.Context, ast.ResourceKey) ([]BusinessTag, error) {
	return nil, nil
}
