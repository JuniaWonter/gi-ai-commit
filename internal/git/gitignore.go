package git

import (
	"bufio"
	"fmt"
	"os"
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
	if fileExists(gitignorePath) {
		data, err := os.ReadFile(gitignorePath)
		if err != nil {
			return fmt.Errorf("读取 .gitignore 失败：%w", err)
		}
		lines = strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == entry {
				return fmt.Errorf("条目 %s 已存在于 .gitignore", entry)
			}
		}
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
	} else {
		lines = []string{}
	}

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

	return nil
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
	w.Flush()

	return nil
}