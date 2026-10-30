package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// File 是 .pr-risk.yml 的解析结果（用户级策略，全部可选）。
//
//	rules:
//	  disable: [R8, money-write]
//	ignore:
//	  paths: ["internal/gen/**", "**/*.pb.go"]
//	public_paths: [webhook, callback]
//	tables:
//	  sensitive: [ledger, wallet]
//	  ignore: [tmp_, audit_]
//	fail_on: high
type File struct {
	Rules struct {
		Disable []string `yaml:"disable"`
	} `yaml:"rules"`
	Ignore struct {
		Paths []string `yaml:"paths"`
	} `yaml:"ignore"`
	PublicPaths []string `yaml:"public_paths"`
	FailOn      string   `yaml:"fail_on"`
	Tables      struct {
		Sensitive []string `yaml:"sensitive"`
		Ignore    []string `yaml:"ignore"`
	} `yaml:"tables"`
}

// LoadFile 读取指定路径的配置；文件不存在时返回空配置（零配置优先）。
func LoadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &f, nil
}

// MatchPath 判断相对路径是否命中忽略规则。
// 支持：目录前缀（internal/gen/**）、双星后缀（**/*.pb.go）、普通 glob、文件名匹配。
func MatchPath(rel string, patterns []string) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "/")
	for _, raw := range patterns {
		p := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(raw)), "/")
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "/**") {
			prefix := strings.TrimSuffix(p, "/**")
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				return true
			}
			continue
		}
		if strings.HasPrefix(p, "**/") {
			suffix := strings.TrimPrefix(p, "**/")
			if rel == suffix || strings.HasSuffix(rel, "/"+suffix) {
				return true
			}
			if ok, _ := path.Match(suffix, path.Base(rel)); ok {
				return true
			}
			continue
		}
		// 裸目录/文件名（无通配符、无斜杠）：按路径段精确匹配。
		if !strings.ContainsAny(p, "*?[") && !strings.Contains(p, "/") {
			for _, seg := range strings.Split(rel, "/") {
				if seg == p {
					return true
				}
			}
			continue
		}
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if ok, _ := path.Match(p, path.Base(rel)); ok {
			return true
		}
	}
	return false
}
