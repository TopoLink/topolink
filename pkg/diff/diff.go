// Package diff 解析 unified diff，提取每个文件在新侧的变更行号。
package diff

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// FileChanges 描述一个文件在 diff 新侧的变更行。
type FileChanges struct {
	Path    string   // 新侧路径（已去掉 a/ b/ 前缀）
	Lines   []int    // 新增/修改的行号（新侧，1-based）
	Added   []string // 新增行文本
	Removed []string // 删除行文本
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// Parse 解析 unified diff，按文件返回变更行号。
func Parse(r io.Reader) ([]FileChanges, error) {
	type acc struct {
		path    string
		lines   []int
		added   []string
		removed []string
	}
	byPath := map[string]*acc{}
	var order []string

	var cur *acc
	newLine := 0
	inHunk := false

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "+++ ") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			path = strings.TrimPrefix(path, "b/")
			path = strings.TrimPrefix(path, "a/")
			inHunk = false
			if path == "/dev/null" || path == "" {
				cur = nil
				continue
			}
			f, ok := byPath[path]
			if !ok {
				f = &acc{path: path}
				byPath[path] = f
				order = append(order, path)
			}
			cur = f
			continue
		}

		if strings.HasPrefix(line, "@@ ") {
			newLine = parseNewStart(line)
			inHunk = cur != nil
			continue
		}

		if !inHunk || cur == nil {
			continue
		}

		switch {
		case strings.HasPrefix(line, "+"):
			cur.lines = append(cur.lines, newLine)
			cur.added = append(cur.added, strings.TrimPrefix(line, "+"))
			newLine++
		case strings.HasPrefix(line, "-"):
			cur.removed = append(cur.removed, strings.TrimPrefix(line, "-"))
		case strings.HasPrefix(line, " "):
			newLine++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
	}

	out := make([]FileChanges, 0, len(order))
	for _, p := range order {
		f := byPath[p]
		out = append(out, FileChanges{Path: f.path, Lines: f.lines, Added: f.added, Removed: f.removed})
	}
	return out, nil
}

func parseNewStart(line string) int {
	m := hunkRe.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
