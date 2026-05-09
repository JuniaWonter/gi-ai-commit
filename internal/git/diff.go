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
		b.WriteString("变更统计 (diff --stat):\n")
		b.WriteString(stat)
		b.WriteString("\n\n")
	}

	// diff --name-status
	nsCmd := exec.Command("git", "diff", "--cached", "--name-status", "--")
	nsCmd.Dir = gitRoot
	nsOut, _ := nsCmd.Output()
	ns := strings.TrimSpace(string(nsOut))
	if ns != "" {
		b.WriteString("变更文件列表 (diff --name-status):\n")
		b.WriteString(ns)
		b.WriteString("\n")
	}

	if b.Len() == 0 {
		return "没有暂存的变更"
	}

	return b.String()
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
