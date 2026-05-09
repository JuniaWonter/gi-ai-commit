package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// SearchResult 表示单个引用匹配结果
type SearchResult struct {
	File    string
	LineNum int
	Content string
}

// SearchReferences 搜索代码库中指定符号的引用位置。
// symbol: 要搜索的符号名称（函数名、类型名、变量名）
// pathFilter: 可选，限定搜索路径（如 "internal/service/"），为空则搜索全部
// maxResults: 最大结果数，默认 30
// 返回格式化的搜索结果的纯文本
func SearchReferences(symbol, pathFilter string, maxResults int) string {
	gitRoot, err := GetGitRoot()
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}

	if maxResults <= 0 {
		maxResults = 30
	}

	// 构建 grep 命令
	args := []string{
		"grep", "-rn",
		"--include=*.go",
		"-e", symbol,
	}

	// 排除 vendor 目录
	args = append(args, "--exclude-dir=vendor", "--exclude-dir=node_modules")

	if pathFilter != "" {
		args = append(args, filepath.Clean(pathFilter))
	} else {
		args = append(args, ".")
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = gitRoot

	out, err := cmd.Output()
	if err != nil {
		// grep returns exit code 1 when no matches found
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) == 0 {
			return fmt.Sprintf("INFO: 未找到符号 %q 的引用", symbol)
		}
		return fmt.Sprintf("ERROR: 搜索失败：%v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	// 去重（同一文件同一行可能被多次匹配）
	seen := make(map[string]bool)
	var results []string
	for _, line := range lines {
		if line == "" {
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true

		// 截断过长行
		if len(line) > 200 {
			line = line[:200] + "..."
		}
		results = append(results, line)

		if len(results) >= maxResults {
			results = append(results, fmt.Sprintf("... 还有 %d 个匹配结果（已截断）", len(lines)-len(results)))
			break
		}
	}

	if len(results) == 0 {
		return fmt.Sprintf("INFO: 未找到符号 %q 的引用", symbol)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("搜索 %q 共找到 %d 处引用：\n\n", symbol, len(lines)))
	for _, r := range results {
		b.WriteString(r)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}
