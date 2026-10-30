// Package lang 提供多语言 tree-sitter 解析的统一抽象（parser registry）。
// 技术选型：纯 Go 绑定 github.com/tree-sitter/go-tree-sitter（无 cgo）。
package lang

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	gogrammar "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

// GoLanguage 返回 Go 的 tree-sitter grammar。
func GoLanguage() *sitter.Language {
	return sitter.NewLanguage(gogrammar.Language())
}

// Registry 语言解析注册表。Phase 1 先支持 Go，Java 待加。
type Registry struct {
	goParser *sitter.Parser
}

// New 初始化注册表。
func New() *Registry {
	return &Registry{}
}

// ParseFile 解析单个文件，返回语法树和源码（调用方负责 tree.Close()）。
func (r *Registry) ParseFile(path string) (*sitter.Tree, []byte, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return r.parseGo(src)
	default:
		return nil, nil, fmt.Errorf("unsupported file type %q: %s", filepath.Ext(path), path)
	}
}

func (r *Registry) parseGo(src []byte) (*sitter.Tree, []byte, error) {
	if r.goParser == nil {
		r.goParser = sitter.NewParser()
		lang := GoLanguage()
		if err := r.goParser.SetLanguage(lang); err != nil {
			return nil, nil, fmt.Errorf("set go language: %w", err)
		}
	}
	return r.goParser.Parse(src, nil), src, nil
}
