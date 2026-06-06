package git

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func AddToGitignore(entry string) error {
	gitRoot, err := getGitRoot()
	if err != nil {
		return fmt.Errorf("获取 git 根目录失败：%w", err)
	}

	gitignorePath := filepath.Join(gitRoot, ".gitignore")

	var lines []string
	alreadyExists := false
	if fileExists(gitignorePath) {
		data, err := os.ReadFile(gitignorePath)
		if err != nil {
			return fmt.Errorf("读取 .gitignore 失败：%w", err)
		}
		lines = strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == entry {
				alreadyExists = true
				break
			}
		}
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
	} else {
		lines = []string{}
	}

	if !alreadyExists {
		lines = append(lines, entry)

		f, err := os.Create(gitignorePath)
		if err != nil {
			return fmt.Errorf("创建 .gitignore 失败：%w", err)
		}
		defer f.Close()

		w := bufio.NewWriter(f)
		for _, line := range lines {
			w.WriteString(line + "\n")
		}
		if err := w.Flush(); err != nil {
			return fmt.Errorf("写入 .gitignore 失败：%w", err)
		}
	}

	if isTracked(gitRoot, entry) {
		cmd := exec.Command("git", "rm", "--cached", "--", entry)
		cmd.Dir = gitRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("从 git 追踪中移除失败：%s", strings.TrimSpace(string(out)))
		}
		
		// Unstage the deletion so it doesn't show as a change
		cmd = exec.Command("git", "reset", "HEAD", "--", entry)
		cmd.Dir = gitRoot
		cmd.Run() // Ignore error - might not have anything to reset
	}

	return nil
}

func isTracked(gitRoot, path string) bool {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", "--", path)
	cmd.Dir = gitRoot
	return cmd.Run() == nil
}

func RemoveFromGitignore(entry string) error {
	gitRoot, err := getGitRoot()
	if err != nil {
		return fmt.Errorf("获取 git 根目录失败：%w", err)
	}

	gitignorePath := filepath.Join(gitRoot, ".gitignore")
	if !fileExists(gitignorePath) {
		return fmt.Errorf(".gitignore 不存在")
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		return fmt.Errorf("读取 .gitignore 失败：%w", err)
	}

	lines := strings.Split(string(data), "\n")
	var newLines []string
	found := false
	for _, line := range lines {
		if strings.TrimSpace(line) == entry {
			found = true
			continue
		}
		newLines = append(newLines, line)
	}

	if !found {
		return fmt.Errorf("条目 %s 不存在于 .gitignore", entry)
	}

	f, err := os.Create(gitignorePath)
	if err != nil {
		return fmt.Errorf("写入 .gitignore 失败：%w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, line := range newLines {
		if line == "" && w.Available() == 0 {
			continue
		}
		w.WriteString(line + "\n")
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("写入 .gitignore 失败：%w", err)
	}

	return nil
}
