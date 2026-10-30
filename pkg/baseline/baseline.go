// Package baseline 记录"已接受"的 findings 指纹，用于只报告新增风险。
package baseline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/topolink/topolink/pkg/engine"
)

// Version 是 baseline 文件格式版本。
const Version = "1"

// File 是一个 baseline 文件：一组已接受的 finding 指纹。
type File struct {
	Version  string   `json:"version"`
	Findings []string `json:"findings"`
}

// Fingerprint 返回 finding 的稳定指纹（用文件名而非绝对路径，便于跨机器复用）。
func Fingerprint(f engine.Finding) string {
	return fmt.Sprintf("%s|%s|%s|%d", engine.RuleName(f.Rule), f.EntryID, filepath.Base(f.Loc.File), f.Loc.Line)
}

// FromFindings 从 findings 生成 baseline（去重）。
func FromFindings(findings []engine.Finding) *File {
	b := &File{Version: Version}
	seen := map[string]bool{}
	for _, f := range findings {
		fp := Fingerprint(f)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		b.Findings = append(b.Findings, fp)
	}
	return b
}

// Save 写入 baseline 文件。
func Save(path string, b *File) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write baseline %s: %w", path, err)
	}
	return nil
}

// Load 读取 baseline 文件。
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	b := &File{}
	if err := json.Unmarshal(data, b); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	return b, nil
}

// Filter 返回不在 baseline 中的 findings（新增），以及被抑制的数量。
func Filter(findings []engine.Finding, b *File) (kept []engine.Finding, suppressed int) {
	if b == nil {
		return findings, 0
	}
	known := make(map[string]bool, len(b.Findings))
	for _, fp := range b.Findings {
		known[fp] = true
	}
	for _, f := range findings {
		if known[Fingerprint(f)] {
			suppressed++
			continue
		}
		kept = append(kept, f)
	}
	return kept, suppressed
}
