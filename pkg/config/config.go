// Package config 负责生态嗅探（Ecosystem Sniffer）。
// 设计铁律：零配置优先——嗅探 go.mod 自动推断要加载哪些 .scm 规则。
// 用户级配置（.pr-risk.yml）在路线图中，届时再引入解析。
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ecosystemRule 把一个 go.mod 依赖前缀映射到对应的 .scm 规则文件。
type ecosystemRule struct {
	modulePrefix string
	rules        []string
}

// ecosystemRules 是"生态嗅探"的核心映射表。
var ecosystemRules = []ecosystemRule{
	{"github.com/gin-gonic/gin", []string{"go/gin.scm"}},
	{"github.com/labstack/echo", []string{"go/echo.scm"}},
	{"github.com/kataras/iris", []string{"go/iris.scm"}},
	{"github.com/go-chi/chi", []string{"go/chi.scm"}},
	{"gorm.io/gorm", []string{"go/gorm.scm"}},
	{"google.golang.org/grpc", []string{"go/grpc.scm"}},
	{"github.com/IBM/sarama", []string{"go/kafka.scm"}},
	{"github.com/Shopify/sarama", []string{"go/kafka.scm"}},
	{"github.com/segmentio/kafka-go", []string{"go/kafka.scm"}},
}

// baseRules 是无论什么项目都加载的规则（net/http 是标准库）。
var baseRules = []string{"go/nethttp.scm"}

// Sniff 解析 go.mod，返回应加载的 .scm 规则列表（去重、保持顺序）。
func Sniff(root string) ([]string, error) {
	rules := append([]string{}, baseRules...)
	seen := make(map[string]bool, len(rules))
	for _, r := range rules {
		seen[r] = true
	}

	modPath := root + "/go.mod"
	if _, err := os.Stat(modPath); err != nil {
		if os.IsNotExist(err) {
			return rules, nil // 没有 go.mod 也至少扫 net/http
		}
		return nil, fmt.Errorf("stat %s: %w", modPath, err)
	}

	modules, err := parseGoMod(modPath)
	if err != nil {
		return nil, err
	}

	for _, m := range modules {
		for _, eco := range ecosystemRules {
			if !strings.HasPrefix(m, eco.modulePrefix) {
				continue
			}
			for _, r := range eco.rules {
				if !seen[r] {
					seen[r] = true
					rules = append(rules, r)
				}
			}
		}
	}
	return rules, nil
}

// parseGoMod 轻量解析 go.mod 的 require 模块路径。
func parseGoMod(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var modules []string
	inRequireBlock := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}

		switch {
		case line == "require (" || line == "require(":
			inRequireBlock = true
		case inRequireBlock && line == ")":
			inRequireBlock = false
		case inRequireBlock:
			if mod := firstField(line); mod != "" {
				modules = append(modules, mod)
			}
		case strings.HasPrefix(line, "require "):
			if mod := firstField(strings.TrimPrefix(line, "require ")); mod != "" {
				modules = append(modules, mod)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return modules, nil
}

// firstField 返回以空白分隔的第一个字段（模块路径）。
func firstField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
