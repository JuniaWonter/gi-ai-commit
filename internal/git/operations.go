package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func GetStatus() (string, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return "", err
	}

	cmd := exec.Command("git", "status", "--short")
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git status 失败: %w", err)
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return "工作区干净，没有待提交的变更", nil
	}
	return result, nil
}

func GetLog(count int, oneline bool) (string, error) {
	if count <= 0 {
		count = 10
	}
	if count > 50 {
		count = 50
	}

	gitRoot, err := getGitRoot()
	if err != nil {
		return "", err
	}

	args := []string{"log", fmt.Sprintf("-%d", count)}
	if oneline {
		args = append(args, "--oneline")
	} else {
		args = append(args, "--pretty=format:%h %an %ad %s", "--date=short")
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git log 失败: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

func GetBranch(all bool) (string, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return "", err
	}

	args := []string{"branch"}
	if all {
		args = append(args, "-a")
	}
	args = append(args, "-v")

	cmd := exec.Command("git", args...)
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git branch 失败: %w", err)
	}

	currentCmd := exec.Command("git", "branch", "--show-current")
	currentCmd.Dir = gitRoot
	currentOut, _ := currentCmd.Output()
	current := strings.TrimSpace(string(currentOut))

	result := fmt.Sprintf("当前分支: %s\n\n%s", current, strings.TrimSpace(string(out)))
	return result, nil
}

func GetDiffUnstaged(path string) (string, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return "", err
	}

	args := []string{"diff", "--no-ext-diff"}
	if path != "" {
		args = append(args, "--", path)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff 失败: %w", err)
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return "没有未暂存的变更", nil
	}
	return result, nil
}

func AddFiles(paths []string) (string, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return "", err
	}

	args := append([]string{"add"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = gitRoot

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git add 失败: %s", stderr.String())
	}

	return fmt.Sprintf("已暂存 %d 个文件: %s", len(paths), strings.Join(paths, ", ")), nil
}

func RestoreFiles(paths []string, staged bool) (string, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return "", err
	}

	args := []string{"restore"}
	if staged {
		args = append(args, "--staged")
	}
	args = append(args, paths...)

	cmd := exec.Command("git", args...)
	cmd.Dir = gitRoot

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git restore 失败: %s", stderr.String())
	}

	action := "已从暂存区移除"
	if !staged {
		action = "已恢复工作区文件"
	}
	return fmt.Sprintf("%s: %s", action, strings.Join(paths, ", ")), nil
}

func Stash(action, message string, index int) (string, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return "", err
	}

	var args []string
	switch action {
	case "push":
		args = []string{"stash", "push"}
		if message != "" {
			args = append(args, "-m", message)
		}
	case "pop":
		args = []string{"stash", "pop"}
	case "list":
		args = []string{"stash", "list"}
	case "drop":
		args = []string{"stash", "drop", fmt.Sprintf("stash@{%d}", index)}
	default:
		return "", fmt.Errorf("未知的 stash action: %s", action)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = gitRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git stash %s 失败: %s", action, stderr.String())
	}

	result := strings.TrimSpace(stdout.String())
	if result == "" {
		switch action {
		case "push":
			result = "已保存工作区变更到 stash"
		case "pop":
			result = "已恢复最近一次 stash"
		case "drop":
			result = fmt.Sprintf("已删除 stash@{%d}", index)
		}
	}
	return result, nil
}

func GetBlame(path string, startLine, endLine int) (string, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return "", err
	}

	args := []string{"blame", "--line-porcelain"}
	if startLine > 0 && endLine > 0 {
		args = append(args, fmt.Sprintf("-L%d,%d", startLine, endLine))
	}
	args = append(args, path)

	cmd := exec.Command("git", args...)
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git blame 失败: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	var result []string
	var currentLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "author ") {
			author := strings.TrimPrefix(line, "author ")
			if currentLine != "" {
				result = append(result, fmt.Sprintf("%s (%s)", currentLine, author))
			}
		} else if strings.HasPrefix(line, "summary ") {
			// skip
		} else if strings.HasPrefix(line, "\t") {
			currentLine = strings.TrimPrefix(line, "\t")
		}
	}

	if len(result) == 0 {
		return "没有 blame 信息", nil
	}

	output := strings.Join(result, "\n")
	if len(output) > 3000 {
		output = output[:3000] + "\n...(truncated)"
	}
	return output, nil
}

func Tag(action, name, message string) (string, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return "", err
	}

	switch action {
	case "list":
		cmd := exec.Command("git", "tag", "-l", "--sort=-creatordate")
		cmd.Dir = gitRoot
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git tag list 失败: %w", err)
		}
		result := strings.TrimSpace(string(out))
		if result == "" {
			return "没有标签", nil
		}
		return result, nil

	case "create":
		if name == "" {
			return "", fmt.Errorf("标签名称不能为空")
		}
		var args []string
		if message != "" {
			args = []string{"tag", "-a", name, "-m", message}
		} else {
			args = []string{"tag", name}
		}
		cmd := exec.Command("git", args...)
		cmd.Dir = gitRoot

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git tag create 失败: %s", stderr.String())
		}
		return fmt.Sprintf("已创建标签: %s", name), nil

	default:
		return "", fmt.Errorf("未知的 tag action: %s", action)
	}
}
