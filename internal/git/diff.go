package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// GetDiffOverview returns a compact overview of staged changes:
// git diff --stat and git diff --name-status
func GetDiffOverview() string {
	gitRoot, err := GetGitRoot()
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}

	var b strings.Builder

	// diff --stat
	statCmd := exec.Command("git", "diff", "--cached", "--stat", "--")
	statCmd.Dir = gitRoot
	statOut, _ := statCmd.Output()
	stat := strings.TrimSpace(string(statOut))
	if stat != "" {
		b.WriteString("## 变更统计 (diff --stat):\n")
		b.WriteString(stat)
		b.WriteString("\n\n")
	}

	// diff --name-status
	nsCmd := exec.Command("git", "diff", "--cached", "--name-status", "--")
	nsCmd.Dir = gitRoot
	nsOut, _ := nsCmd.Output()
	ns := strings.TrimSpace(string(nsOut))
	if ns != "" {
		b.WriteString("## 变更文件列表 (diff --name-status):\n")
		b.WriteString(ns)
		b.WriteString("\n\n")
		
		// Add file type categorization
		b.WriteString("## 文件类型分析:\n")
		categorizeFiles(ns, &b)
		b.WriteString("\n")
	}

	if b.Len() == 0 {
		return "没有暂存的变更"
	}

	return b.String()
}

// categorizeFiles analyzes changed files and categorizes them by type
func categorizeFiles(nameStatus string, b *strings.Builder) {
	lines := strings.Split(strings.TrimSpace(nameStatus), "\n")
	
	var coreFiles, testFiles, configFiles, generatedFiles, docFiles []string
	
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		path := parts[len(parts)-1]
		
		// Categorize by file path and name
		switch {
		case strings.Contains(path, "_test.go") || strings.Contains(path, "/test/") || strings.Contains(path, "/tests/"):
			testFiles = append(testFiles, path)
		case strings.Contains(path, "/config/") || strings.Contains(path, ".yaml") || strings.Contains(path, ".yml") || 
		     strings.Contains(path, ".json") || strings.Contains(path, ".toml"):
			configFiles = append(configFiles, path)
		case strings.Contains(path, "generated") || strings.Contains(path, ".pb.go") || strings.Contains(path, "_gen.go"):
			generatedFiles = append(generatedFiles, path)
		case strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".txt") || strings.Contains(path, "/docs/"):
			docFiles = append(docFiles, path)
		default:
			coreFiles = append(coreFiles, path)
		}
	}
	
	if len(coreFiles) > 0 {
		b.WriteString(fmt.Sprintf("- **核心代码** (%d files): %s\n", len(coreFiles), strings.Join(coreFiles[:min(5, len(coreFiles))], ", ")))
		if len(coreFiles) > 5 {
			b.WriteString(fmt.Sprintf("  ... 及其他 %d 个核心文件\n", len(coreFiles)-5))
		}
	}
	if len(testFiles) > 0 {
		b.WriteString(fmt.Sprintf("- **测试文件** (%d files): %s\n", len(testFiles), strings.Join(testFiles[:min(3, len(testFiles))], ", ")))
	}
	if len(configFiles) > 0 {
		b.WriteString(fmt.Sprintf("- **配置文件** (%d files): %s\n", len(configFiles), strings.Join(configFiles[:min(3, len(configFiles))], ", ")))
	}
	if len(generatedFiles) > 0 {
		b.WriteString(fmt.Sprintf("- **生成代码** (%d files): 可跳过\n", len(generatedFiles)))
	}
	if len(docFiles) > 0 {
		b.WriteString(fmt.Sprintf("- **文档** (%d files): %s\n", len(docFiles), strings.Join(docFiles[:min(3, len(docFiles))], ", ")))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetFileDiff returns the diff for a single file (staged changes).
// When a file has no staged changes, falls back to unstaged diff.
// contextLines 控制变更行上下的上下文行数（默认 3）。
func GetFileDiff(path string, contextLines int) string {
	gitRoot, err := GetGitRoot()
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	if contextLines <= 0 {
		contextLines = 3
	}
	unified := fmt.Sprintf("--unified=%d", contextLines)

	// Try staged diff first
	cmd := exec.Command("git", "diff", "--cached", "--no-ext-diff", unified, "--", path)
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return strings.TrimSpace(string(out))
	}

	// Fall back to unstaged diff
	cmd = exec.Command("git", "diff", "--no-ext-diff", unified, "--", path)
	cmd.Dir = gitRoot
	out, err = cmd.Output()
	if err != nil {
		return fmt.Sprintf("ERROR: 获取 diff 失败：%v", err)
	}

	diff := strings.TrimSpace(string(out))
	if diff == "" {
		return fmt.Sprintf("INFO: 文件 %s 没有未提交的变更", filepath.Clean(path))
	}
	return diff
}
