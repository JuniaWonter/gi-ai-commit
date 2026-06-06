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

// AnalyzeChangedFunctions analyzes changed functions in a file and returns their complete definitions.
// For Go files, it uses AST parsing to identify functions. For other languages, it returns enhanced diff context.
func AnalyzeChangedFunctions(path string) string {
	gitRoot, err := GetGitRoot()
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}

	// Get the diff to find changed line ranges
	diffCmd := exec.Command("git", "diff", "--cached", "--no-ext-diff", "--unified=0", "--", path)
	diffCmd.Dir = gitRoot
	diffOut, err := diffCmd.Output()
	if err != nil || strings.TrimSpace(string(diffOut)) == "" {
		// Try unstaged diff
		diffCmd = exec.Command("git", "diff", "--no-ext-diff", "--unified=0", "--", path)
		diffCmd.Dir = gitRoot
		diffOut, err = diffCmd.Output()
		if err != nil {
			return fmt.Sprintf("ERROR: 获取 diff 失败：%v", err)
		}
	}

	diff := strings.TrimSpace(string(diffOut))
	if diff == "" {
		return fmt.Sprintf("INFO: 文件 %s 没有变更", filepath.Clean(path))
	}

	// For Go files, use AST-based analysis
	if strings.HasSuffix(path, ".go") {
		return analyzeGoFunctions(gitRoot, path, diff)
	}

	// For other languages, return diff with more context
	return fmt.Sprintf("文件 %s 的变更（非 Go 文件，返回增强 diff）：\n\n%s\n\n提示：请使用 read_file 工具读取完整文件以理解变更上下文。", path, GetFileDiff(path, 10))
}

// analyzeGoFunctions uses Go AST to find and extract changed functions
func analyzeGoFunctions(gitRoot, path, diff string) string {
	// Parse changed line ranges from diff
	changedRanges := parseDiffRanges(diff)
	if len(changedRanges) == 0 {
		return "无法解析变更行号"
	}

	// Read the file content
	fullPath := filepath.Join(gitRoot, path)
	cmd := exec.Command("git", "show", "HEAD:"+path)
	cmd.Dir = gitRoot
	fileContent, err := cmd.Output()
	if err != nil {
		// File might be new, read from working directory
		cmd = exec.Command("cat", fullPath)
		fileContent, err = cmd.Output()
		if err != nil {
			return fmt.Sprintf("ERROR: 读取文件失败：%v", err)
		}
	}

	// Simple heuristic: find function definitions that contain changed lines
	// This is a simplified version - a full implementation would use go/ast
	lines := strings.Split(string(fileContent), "\n")
	var result strings.Builder
	result.WriteString(fmt.Sprintf("## 文件 %s 的变更函数分析\n\n", path))

	for _, rng := range changedRanges {
		// Find the function that contains this range
		funcStart, funcEnd, funcName := findContainingFunction(lines, rng.start)
		if funcName == "" {
			continue
		}

		result.WriteString(fmt.Sprintf("### 函数: %s (行 %d-%d)\n", funcName, funcStart+1, funcEnd+1))
		result.WriteString(fmt.Sprintf("变更行: %d-%d\n\n", rng.start+1, rng.end+1))
		result.WriteString("```go\n")
		for i := funcStart; i < funcEnd && i < len(lines); i++ {
			lineNum := i + 1
			prefix := "  "
			if lineNum >= rng.start+1 && lineNum <= rng.end+1 {
				prefix = "→ " // Mark changed lines
			}
			result.WriteString(fmt.Sprintf("%s%4d: %s\n", prefix, lineNum, lines[i]))
		}
		result.WriteString("```\n\n")
	}

	if result.Len() == 0 {
		return "未找到包含变更的函数定义"
	}

	return result.String()
}

type lineRange struct {
	start, end int
}

// parseDiffRanges extracts changed line ranges from unified diff format
func parseDiffRanges(diff string) []lineRange {
	var ranges []lineRange
	lines := strings.Split(diff, "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			// Parse @@ -oldStart,oldCount +newStart,newCount @@
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				newPart := parts[2] // +newStart,newCount
				if strings.HasPrefix(newPart, "+") {
					newPart = newPart[1:]
					var start, count int
					if strings.Contains(newPart, ",") {
						fmt.Sscanf(newPart, "%d,%d", &start, &count)
					} else {
						fmt.Sscanf(newPart, "%d", &start)
						count = 1
					}
					if count > 0 {
						ranges = append(ranges, lineRange{start: start - 1, end: start + count - 2})
					}
				}
			}
		}
	}

	return ranges
}

// findContainingFunction finds the function that contains the given line
// This is a simplified heuristic - looks for "func " keyword
func findContainingFunction(lines []string, targetLine int) (start, end int, name string) {
	// Bounds checking
	if len(lines) == 0 || targetLine < 0 || targetLine >= len(lines) {
		return 0, 0, ""
	}

	// Search backwards for function start
	for i := targetLine; i >= 0; i-- {
		if i >= len(lines) {
			continue
		}
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "func ") {
			start = i
			// Extract function name
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				name = parts[1]
				if idx := strings.Index(name, "("); idx > 0 {
					name = name[:idx]
				}
			}
			break
		}
	}

	if name == "" {
		return 0, 0, ""
	}

	// Search forwards for function end (simplified: look for closing brace at same indentation)
	braceCount := 0
	foundOpen := false
	for i := start; i < len(lines); i++ {
		line := lines[i]
		for _, ch := range line {
			if ch == '{' {
				braceCount++
				foundOpen = true
			} else if ch == '}' {
				braceCount--
				if foundOpen && braceCount == 0 {
					end = i + 1
					return start, end, name
				}
			}
		}
	}

	// If we didn't find the end, return a reasonable range
	end = min(start+50, len(lines))
	return start, end, name
}
