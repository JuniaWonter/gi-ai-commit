package git

import (
	"fmt"
	"os/exec"
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
