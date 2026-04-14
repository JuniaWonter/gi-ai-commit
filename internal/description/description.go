package description

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const descriptionFile = ".git/ai-description"

func GetDescriptionPath() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("获取 git 目录失败：%w", err)
	}

	gitDir := strings.TrimSpace(string(output))
	return filepath.Join(gitDir, "ai-description"), nil
}

func Exists() (bool, error) {
	path, err := GetDescriptionPath()
	if err != nil {
		return false, err
	}

	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func Read() (string, error) {
	path, err := GetDescriptionPath()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取描述文件失败：%w", err)
	}

	return strings.TrimSpace(string(data)), nil
}

func Write(content string) error {
	path, err := GetDescriptionPath()
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入描述文件失败：%w", err)
	}

	return nil
}

func ShouldUpdate(commitCount, interval int) bool {
	return commitCount%interval == 0
}
