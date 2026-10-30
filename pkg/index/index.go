// Package index 构建并持有代码拓扑的三张内存索引（正向 / 反查 / 排查）。
// 设计铁律：无状态、纯内存、可序列化（P2 落库 / P3 跨仓库合并的基础）。
package index

import (
	"encoding/json"

	"github.com/topolink/topolink/pkg/ast"
)

// ForwardIndex 正向索引：函数/入口 -> 调用链。
type ForwardIndex map[ast.Symbol][]ast.CallNode

// ReverseIndex 反查索引：资源 -> 入口（"反向影响查询"，区分读写）。
type ReverseIndex map[ast.ResourceKey][]ast.Ref

// TroubleIndex 排查索引：错误码/日志模板/超时/枚举 -> 源码位置。
type TroubleIndex map[string][]ast.Loc

// Index 拓扑索引聚合（本产品的"系统 of record"载体）。
type Index struct {
	Entries     []ast.EntryNode
	Calls       []ast.CallNode
	Ops         []ast.DBOp
	Funcs       []ast.FuncSummary
	Forward     ForwardIndex
	Reverse     ReverseIndex
	Trouble     TroubleIndex
	FuncEntries map[ast.Symbol][]string // 函数 -> 可达它的入口 ID（diff 影响定位用）
}

// New 返回一个初始化好的空索引。
func New() *Index {
	return &Index{
		Forward:     make(ForwardIndex),
		Reverse:     make(ReverseIndex),
		Trouble:     make(TroubleIndex),
		FuncEntries: make(map[ast.Symbol][]string),
	}
}

// Serialize 序列化为 JSON（P2 拓扑快照落库的基础）。
func (idx *Index) Serialize() ([]byte, error) {
	data, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Deserialize 从 JSON 还原索引。
func Deserialize(data []byte) (*Index, error) {
	idx := New()
	if err := json.Unmarshal(data, idx); err != nil {
		return nil, err
	}
	return idx, nil
}
