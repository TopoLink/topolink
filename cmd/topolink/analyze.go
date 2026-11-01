package main

import (
	"context"

	"github.com/topolink/topolink/pkg/engine"
	"github.com/topolink/topolink/pkg/index"
	"github.com/topolink/topolink/pkg/llm"
	"github.com/topolink/topolink/pkg/meta"
	"github.com/topolink/topolink/pkg/scan"
)

// analyze 提取 + 载入画像 + 建索引，返回可查询的索引。
func analyze(repos []string, profilePaths []string, profileOut string, ignore []string) (*index.Index, error) {
	return scan.Repos(repos, scan.Options{
		Ignore:       ignore,
		ProfilePaths: profilePaths,
		ProfileOut:   profileOut,
	})
}

// run 是扫描模式的便捷入口（供测试复用）。
func run(repos []string, profilePaths []string, profileOut string) ([]engine.Finding, error) {
	idx, err := analyze(repos, profilePaths, profileOut, nil)
	if err != nil {
		return nil, err
	}
	eng := engine.New(&llm.Noop{}, &meta.Noop{})
	return eng.Run(context.Background(), idx)
}
