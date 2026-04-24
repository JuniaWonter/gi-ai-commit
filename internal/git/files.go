package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var treeIgnoreDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".next":        true,
	"dist":         true,
	"build":        true,
	"vendor":       true,
	".venv":        true,
	"__pycache__":  true,
}

var treeIgnoreExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".svg":  true,
	".ico":  true,
	".woff": true,
	".woff2": true,
	".ttf":  true,
	".eot":  true,
}

func GetProjectTree(maxDepth int) string {
	root, err := GetGitRoot()
	if err != nil {
		return "(无法获取项目根目录)"
	}

	var b strings.Builder
	b.WriteString(filepath.Base(root) + "/\n")
	walkTree(root, 1, maxDepth, &b)

	result := b.String()
	if len(result) > 3000 {
		result = result[:3000] + "\n...(目录树已截断)"
	}
	return result
}

func walkTree(dir string, depth, maxDepth int, b *strings.Builder) {
	if depth > maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		name := entry.Name()
		if treeIgnoreDirs[name] {
			continue
		}

		if treeIgnoreExts[filepath.Ext(name)] {
			continue
		}

		prefix := strings.Repeat("  ", depth) + "|-- "
		b.WriteString(prefix + name + "\n")

		if entry.IsDir() {
			walkTree(filepath.Join(dir, name), depth+1, maxDepth, b)
		}
	}
}

func ReadFileContent(relPath string, startLine, endLine int) (string, error) {
	root, err := GetGitRoot()
	if err != nil {
		return "", fmt.Errorf("获取 git 根目录失败：%w", err)
	}

	fullPath := filepath.Join(root, relPath)

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("文件不存在：%s", relPath)
		}
		return "", fmt.Errorf("读取文件失败：%w", err)
	}

	if info.IsDir() {
		return "", fmt.Errorf("%s 是目录，不是文件", relPath)
	}

	if info.Size() > 100*1024 {
		return "", fmt.Errorf("文件过大（%d bytes），无法读取", info.Size())
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("读取文件失败：%w", err)
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > totalLines {
		endLine = totalLines
	}
	if startLine > endLine {
		return "", fmt.Errorf("起始行号 %d 大于结束行号 %d", startLine, endLine)
	}
	if startLine > totalLines {
		return "", fmt.Errorf("起始行号 %d 超出文件范围（共 %d 行）", startLine, totalLines)
	}

	var b strings.Builder
	lineCount := 0
	for i := startLine - 1; i < endLine && i < totalLines; i++ {
		lineNum := i + 1
		b.WriteString(fmt.Sprintf("%4d: %s\n", lineNum, lines[i]))
		lineCount++
		if b.Len() > 5000 {
			b.WriteString("...(已截断)")
			break
		}
	}

	if lineCount == 0 {
		return "", fmt.Errorf("指定行范围无内容")
	}

	header := fmt.Sprintf("FILE: %s (共 %d 行，显示 %d-%d 行)\n", relPath, totalLines, startLine, min(endLine, totalLines))
	return header + b.String(), nil
}
