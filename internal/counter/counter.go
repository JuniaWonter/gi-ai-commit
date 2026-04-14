package counter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const counterFile = ".git/ai-commit-count"

func GetCounterPath() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("获取 git 目录失败：%w", err)
	}

	gitDir := strings.TrimSpace(string(output))
	return filepath.Join(gitDir, "ai-commit-count"), nil
}

func Get() (int, error) {
	path, err := GetCounterPath()
	if err != nil {
		return 0, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("读取计数文件失败：%w", err)
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("解析计数失败：%w", err)
	}

	return count, nil
}

func Increment() error {
	count, err := Get()
	if err != nil {
		return err
	}

	return Set(count + 1)
}

func Set(count int) error {
	path, err := GetCounterPath()
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, []byte(strconv.Itoa(count)), 0644); err != nil {
		return fmt.Errorf("写入计数文件失败：%w", err)
	}

	return nil
}

func Reset() error {
	return Set(0)
}
