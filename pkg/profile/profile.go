// Package profile 定义"库画像"：一个仓库可跨仓复用的摘要。
// 业务仓分析时载入画像，即可在不持有库源码的情况下穿透 import 边界溯源。
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/topolink/topolink/pkg/ast"
)

// Version 是画像格式版本。
const Version = "1"

// Profile 是一个仓库的函数摘要、调用边与下游操作快照。
// 不含入口（entries），避免把库自身的入口误当成业务入口。
type Profile struct {
	Version string            `json:"version"`
	Funcs   []ast.FuncSummary `json:"funcs"`
	Calls   []ast.CallNode    `json:"calls"`
	Ops     []ast.DBOp        `json:"ops"`
}

// Build 从提取结果生成画像。
func Build(funcs []ast.FuncSummary, calls []ast.CallNode, ops []ast.DBOp) *Profile {
	return &Profile{Version: Version, Funcs: funcs, Calls: calls, Ops: ops}
}

// Save 将画像写入 JSON 文件。
func Save(path string, p *Profile) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write profile %s: %w", path, err)
	}
	return nil
}

// Load 从 JSON 文件读取画像。
func Load(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile %s: %w", path, err)
	}
	p := &Profile{}
	if err := json.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("parse profile %s: %w", path, err)
	}
	return p, nil
}
