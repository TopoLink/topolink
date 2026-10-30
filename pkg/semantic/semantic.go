// Package semantic 实现"语义索引"：对代码里的语义单元（入口/中间件/表/RPC）
// 生成 AI 标注，并按【内容指纹】缓存复用 —— 只有代码内容变化时才重新标注。
package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topolink/topolink/pkg/ast"
)

// Version 是语义索引缓存格式版本。
const Version = "1"

// Annotation 是 AI 对某个语义单元的标注（软事实，带证据与置信度）。
type Annotation struct {
	Kind       string    `json:"kind,omitempty"` // middleware-purpose / business-impact / data-sensitivity / rpc-contract
	Text       string    `json:"text"`           // 总体意图
	Effects    []Effect  `json:"effects,omitempty"`
	Evidence   []ast.Loc `json:"evidence,omitempty"`
	Confidence float64   `json:"confidence,omitempty"`
	Model      string    `json:"model,omitempty"`
}

// Effect 是单元内部的一个条件分支对业务/行为的影响。
// 例如中间件里的某个 if 命中后 abort 403，用于保护跨租户越权。
type Effect struct {
	Condition string `json:"condition"` // 触发条件，如 "tenant mismatch"
	Action    string `json:"action"`    // 动作，如 "abort 403" / "continue" / "redirect"
	Business  string `json:"business"`  // 业务后果，如 "prevents cross-tenant access"
}

// Unit 是一个待标注的语义单元（含内容指纹）。
type Unit struct {
	ID          string  `json:"id"`          // 如 entry:http:GET:/orders / middleware:Auth / table:accounts
	Kind        string  `json:"kind"`        // entry / middleware / table / rpc
	Fingerprint string  `json:"fingerprint"` // 内容指纹（源码变更即变化）
	Source      string  `json:"source"`      // 送给 AI 的源码片段
	Loc         ast.Loc `json:"loc,omitempty"`
}

// Cached 是缓存里的一条记录。
type Cached struct {
	Kind        string     `json:"kind"`
	Fingerprint string     `json:"fingerprint"`
	Annotation  Annotation `json:"annotation"`
}

// Index 是语义索引缓存。
type Index struct {
	Version string            `json:"version"`
	Units   map[string]Cached `json:"units"`
}

// NewIndex 返回空的语义索引。
func NewIndex() *Index {
	return &Index{Version: Version, Units: map[string]Cached{}}
}

// LoadOrNew 读取缓存；文件不存在时返回空索引。
func LoadOrNew(path string) (*Index, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return NewIndex(), nil
		}
		return nil, fmt.Errorf("stat semantic index %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read semantic index %s: %w", path, err)
	}
	idx := NewIndex()
	if err := json.Unmarshal(data, idx); err != nil {
		return nil, fmt.Errorf("parse semantic index %s: %w", path, err)
	}
	if idx.Units == nil {
		idx.Units = map[string]Cached{}
	}
	return idx, nil
}

// Save 写入缓存。
func Save(path string, idx *Index) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal semantic index: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write semantic index %s: %w", path, err)
	}
	return nil
}

// Fingerprint 计算内容指纹：kind + id + 源码片段。源码变更 → 指纹变化。
func Fingerprint(kind, id, source string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write([]byte(source))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Enricher 为语义单元生成标注（AI 或其它实现）。
type Enricher interface {
	Annotate(ctx context.Context, unit Unit) (Annotation, error)
}

// Noop 是骨架阶段的默认实现（不调用任何模型）。
type Noop struct{}

// Annotate 返回空标注。
func (Noop) Annotate(context.Context, Unit) (Annotation, error) { return Annotation{}, nil }

// EnrichResult 是一次 enrich 的统计。
type EnrichResult struct {
	Total     int
	Reused    int
	Annotated int
}

// Enrich 按内容指纹复用标注：指纹未变的单元直接复用（不调用 Enricher），
// 指纹变化或新增的单元才调用 Enricher 重新标注。
func Enrich(ctx context.Context, units []Unit, cache *Index, e Enricher) (EnrichResult, error) {
	res := EnrichResult{Total: len(units)}
	if cache.Units == nil {
		cache.Units = map[string]Cached{}
	}

	for _, u := range units {
		if old, ok := cache.Units[u.ID]; ok && old.Fingerprint == u.Fingerprint {
			res.Reused++
			continue
		}
		ann, err := e.Annotate(ctx, u)
		if err != nil {
			return res, fmt.Errorf("annotate %s: %w", u.ID, err)
		}
		cache.Units[u.ID] = Cached{Kind: u.Kind, Fingerprint: u.Fingerprint, Annotation: ann}
		res.Annotated++
	}

	// 清理已不存在的单元，保持缓存精炼
	prune(cache, units)
	return res, nil
}

func prune(cache *Index, units []Unit) {
	current := make(map[string]bool, len(units))
	for _, u := range units {
		current[u.ID] = true
	}
	for id := range cache.Units {
		if !current[id] {
			delete(cache.Units, id)
		}
	}
}

// EnrichConcurrent 与 Enrich 等价，但以有限并发调用 Enricher（concurrency <= 1 时回退串行）。
// 适合大仓库首次索引；仍按内容指纹跳过未变更单元，尊重外部限流。
func EnrichConcurrent(ctx context.Context, units []Unit, cache *Index, e Enricher, concurrency int) (EnrichResult, error) {
	if concurrency <= 1 {
		return Enrich(ctx, units, cache, e)
	}
	if cache.Units == nil {
		cache.Units = map[string]Cached{}
	}

	var pending []Unit
	res := EnrichResult{Total: len(units)}
	for _, u := range units {
		if old, ok := cache.Units[u.ID]; ok && old.Fingerprint == u.Fingerprint {
			res.Reused++
			continue
		}
		pending = append(pending, u)
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	var firstErr error

	for _, u := range pending {
		wg.Add(1)
		sem <- struct{}{}
		go func(u Unit) {
			defer wg.Done()
			defer func() { <-sem }()

			ann, err := e.Annotate(ctx, u)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("annotate %s: %w", u.ID, err)
				}
				return
			}
			cache.Units[u.ID] = Cached{Kind: u.Kind, Fingerprint: u.Fingerprint, Annotation: ann}
			res.Annotated++
		}(u)
	}
	wg.Wait()

	prune(cache, units)
	return res, firstErr
}

// srcRange 是一个需要读取的源码行区间（1-based，闭区间）。
type srcRange struct {
	file       string
	start, end int
}

// loadSnippets 按文件分组读取源码，只保留请求的行区间。
// 峰值内存为"单个文件 + 已抽取片段"，而不是整个仓库的源码。
func loadSnippets(reqs map[srcRange]bool) map[srcRange]string {
	byFile := map[string][]srcRange{}
	for r := range reqs {
		if r.file == "" || r.start < 1 || r.end < r.start {
			continue
		}
		byFile[r.file] = append(byFile[r.file], r)
	}

	out := make(map[srcRange]string, len(reqs))
	for file, ranges := range byFile {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, r := range ranges {
			end := r.end
			if end > len(lines) {
				end = len(lines)
			}
			if end < r.start {
				continue
			}
			out[r] = strings.Join(lines[r.start-1:end], "\n")
		}
	}
	return out
}

// BuildUnits 从结构化索引构建语义单元。源码片段决定指纹与 AI 输入。
func BuildUnits(entries []ast.EntryNode, funcs []ast.FuncSummary, ops []ast.DBOp) []Unit {
	funcByName := make(map[string]ast.FuncSummary, len(funcs))
	for _, f := range funcs {
		if _, ok := funcByName[f.Symbol.Name]; !ok {
			funcByName[f.Symbol.Name] = f
		}
	}

	// 先收集所有需要读取的行区间，再按文件一次性读取。
	reqs := map[srcRange]bool{}
	addFunc := func(name string) {
		if f, ok := funcByName[name]; ok {
			reqs[srcRange{file: f.File, start: f.StartLine, end: f.EndLine}] = true
		}
	}
	for _, e := range entries {
		addFunc(e.Handler.Name)
	}
	for _, e := range entries {
		for _, mw := range e.Middleware {
			name := ast.MiddlewareName(mw)
			addFunc(name)
		}
	}
	for _, op := range ops {
		if op.Resource != "" && op.Loc.File != "" {
			reqs[srcRange{file: op.Loc.File, start: op.Loc.Line, end: op.Loc.Line}] = true
		}
	}
	snips := loadSnippets(reqs)

	funcBody := func(name string) (string, ast.Loc) {
		f, ok := funcByName[name]
		if !ok {
			return "", ast.Loc{}
		}
		return snips[srcRange{file: f.File, start: f.StartLine, end: f.EndLine}], ast.Loc{File: f.File, Line: f.StartLine}
	}

	var units []Unit
	seen := map[string]bool{}

	// 入口
	for _, e := range entries {
		src := string(e.Kind) + " " + e.Name
		if b, _ := funcBody(e.Handler.Name); b != "" {
			src += "\n" + b
		}
		units = append(units, newUnit("entry", "entry:"+e.ID, src, e.SourceLoc))
		seen["entry:"+e.ID] = true
	}

	// 中间件
	for _, e := range entries {
		for _, mw := range e.Middleware {
			name := ast.MiddlewareName(mw)
			name = ast.LastSegment(name)
			id := "middleware:" + name
			if seen[id] {
				continue
			}
			seen[id] = true
			src := mw
			loc := e.SourceLoc
			if b, floc := funcBody(name); b != "" {
				src += "\n" + b
				loc = floc
			}
			units = append(units, newUnit("middleware", id, src, loc))
		}
	}

	// 表
	for _, op := range ops {
		if op.Resource == "" {
			continue
		}
		id := "table:" + op.Resource
		if seen[id] {
			continue
		}
		seen[id] = true
		var b strings.Builder
		b.WriteString(op.Resource)
		for _, o := range ops {
			if o.Resource != op.Resource || o.Loc.File == "" {
				continue
			}
			if s := snips[srcRange{file: o.Loc.File, start: o.Loc.Line, end: o.Loc.Line}]; s != "" {
				b.WriteString("\n")
				b.WriteString(s)
			}
		}
		units = append(units, newUnit("table", id, b.String(), op.Loc))
	}

	return units
}

func newUnit(kind, id, source string, loc ast.Loc) Unit {
	return Unit{ID: id, Kind: kind, Source: source, Loc: loc, Fingerprint: Fingerprint(kind, id, source)}
}
