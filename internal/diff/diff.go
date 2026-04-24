package diff

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oliver/git-ai-commit/internal/debug"
)

type FileChange struct {
	Path     string
	Staged   bool
	Selected bool
}

type DiffSummary struct {
	TotalFiles    int
	AddedFiles    int
	ModifiedFiles int
	DeletedFiles  int
	FileTypes     map[string]int
	LargeFiles    []string
}

func GetChangedFiles() ([]FileChange, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return nil, fmt.Errorf("获取 git 根目录失败：%w", err)
	}

	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = gitRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("获取 git 状态失败：%w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var files []FileChange

	for _, line := range lines {
		if len(line) < 3 {
			continue
		}

		status := line[:2]
		// 所有有效变更（新增、修改、删除、重命名等）都显示给用户选择
		// StageFiles 已处理已删除文件（git add -u）

		path := strings.TrimSpace(line[2:])
		staged := status[0] != ' '

		if strings.HasSuffix(path, "/") {
			expanded, err := expandUntrackedDir(gitRoot, path)
			if err != nil || len(expanded) == 0 {
				files = append(files, FileChange{
					Path:   path,
					Staged: staged,
				})
			} else {
				for _, fp := range expanded {
					files = append(files, FileChange{
						Path:   fp,
						Staged: false,
					})
				}
			}
		} else {
			files = append(files, FileChange{
				Path:   path,
				Staged: staged,
			})
		}
	}

	if len(files) == 0 {
		cmd = exec.Command("git", "diff", "--cached", "--name-status")
		cmd.Dir = gitRoot
		output, err = cmd.Output()
		if err == nil && len(output) > 0 {
			cachedLines := strings.Split(strings.TrimSpace(string(output)), "\n")
			for _, line := range cachedLines {
				if len(line) < 2 {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					files = append(files, FileChange{
						Path:   parts[1],
						Staged: true,
					})
				}
			}
		}
	}

	return files, nil
}

func GetFileDiff(filePath string) (string, error) {
	content, _, err := GetFileDiffFull(filePath, false)
	return content, err
}

func GetFileDiffFull(filePath string, ignoreWS bool) (string, string, error) {
	gitRoot, err := getGitRoot()
	if err != nil {
		return "", "", fmt.Errorf("获取 git 根目录失败：%w", err)
	}

	wsFlag := ""
	if ignoreWS {
		wsFlag = "-w"
	}

	argsCached := []string{"diff", "--cached"}
	if wsFlag != "" {
		argsCached = append(argsCached, wsFlag)
	}
	argsCached = append(argsCached, "--", filePath)
	cmd := exec.Command("git", argsCached...)
	cmd.Dir = gitRoot
	cachedOutput, _ := cmd.Output()

	argsUnstaged := []string{"diff"}
	if wsFlag != "" {
		argsUnstaged = append(argsUnstaged, wsFlag)
	}
	argsUnstaged = append(argsUnstaged, "--", filePath)
	cmd = exec.Command("git", argsUnstaged...)
	cmd.Dir = gitRoot
	unstagedOutput, _ := cmd.Output()

	raw := strings.TrimSpace(string(cachedOutput)) + "\n" + strings.TrimSpace(string(unstagedOutput))
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if isUntrackedFile(gitRoot, filePath) {
			untrackedDiff, err := getUntrackedDiff(gitRoot, filePath, ignoreWS)
			if err != nil {
				return "", "", err
			}
			return untrackedDiff, untrackedDiff, nil
		}
		return "", "", fmt.Errorf("文件 %s 没有变更", filePath)
	}

	return raw, raw, nil
}

func GetStagedDiff() (string, error) {
	cmd := exec.Command("git", "diff", "--cached")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("获取暂存区 diff 失败：%w", err)
	}
	return string(output), nil
}

func StageFiles(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	debug.Logf("diff.StageFiles begin pathCount=%d", len(paths))

	gitRoot, err := getGitRoot()
	if err != nil {
		debug.Logf("diff.StageFiles getGitRoot failed err=%v", err)
		return fmt.Errorf("获取 git 根目录失败：%w", err)
	}

	// 尝试清空暂存区，如果失败（如无 HEAD）则忽略
	resetCmd := exec.Command("git", "reset")
	resetCmd.Dir = gitRoot
	_ = resetCmd.Run()

	var toStage, toDelete []string
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(gitRoot, p)); os.IsNotExist(err) {
			toDelete = append(toDelete, p)
		} else {
			toStage = append(toStage, p)
		}
	}

	if len(toDelete) > 0 {
		debug.Logf("diff.StageFiles stage deleted files count=%d", len(toDelete))
		args := []string{"add", "-u"}
		args = append(args, toDelete...)
		cmd := exec.Command("git", args...)
		cmd.Dir = gitRoot
		if err := cmd.Run(); err != nil {
			debug.Logf("diff.StageFiles stage deleted failed err=%v", err)
			return fmt.Errorf("暂存删除文件失败：%w", err)
		}
	}

	if len(toStage) > 0 {
		debug.Logf("diff.StageFiles stage files count=%d", len(toStage))
		args := []string{"add"}
		args = append(args, toStage...)
		cmd := exec.Command("git", args...)
		cmd.Dir = gitRoot
		output, err := cmd.CombinedOutput()
		if err != nil {
			debug.Logf("diff.StageFiles git add failed err=%v output=%s", err, strings.TrimSpace(string(output)))
			return fmt.Errorf("暂存文件失败：%w, output: %s", err, string(output))
		}
	}
	debug.Logf("diff.StageFiles success")
	return nil
}

func expandUntrackedDir(gitRoot, dirPath string) ([]string, error) {
	dirPath = strings.TrimSuffix(dirPath, "/")
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard", dirPath)
	cmd.Dir = gitRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result, nil
}

func isUntrackedFile(gitRoot, filePath string) bool {
	cmd := exec.Command("git", "ls-files", "--", filePath)
	cmd.Dir = gitRoot
	output, _ := cmd.Output()
	return strings.TrimSpace(string(output)) == ""
}

func getUntrackedDiff(gitRoot, filePath string, ignoreWS bool) (string, error) {
	args := []string{"diff", "--no-index"}
	if ignoreWS {
		args = append(args, "-w")
	}
	args = append(args, "/dev/null", filePath)
	cmd := exec.Command("git", args...)
	cmd.Dir = gitRoot
	output, _ := cmd.Output()
	diff := strings.TrimSpace(string(output))
	if diff == "" {
		return "", fmt.Errorf("文件 %s 没有变更", filePath)
	}
	lines := strings.Split(diff, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			lines[i] = "diff --git a/" + filePath + " b/" + filePath
		} else if strings.HasPrefix(line, "--- a/") {
			lines[i] = "--- /dev/null"
		} else if strings.HasPrefix(line, "+++ b/") {
			lines[i] = "+++ b/" + filePath
		}
	}
	return strings.Join(lines, "\n"), nil
}

func getGitRoot() (string, error) {
	debug.Logf("diff.getGitRoot start")
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		debug.Logf("diff.getGitRoot failed err=%v", err)
		return "", fmt.Errorf("获取 git 根目录失败：%w", err)
	}
	root := strings.TrimSpace(string(output))
	debug.Logf("diff.getGitRoot success root=%s", root)
	return root, nil
}

func LimitDiffLines(diff string, maxLines int) string {
	lines := strings.Split(diff, "\n")
	if len(lines) <= maxLines {
		return diff
	}

	truncated := strings.Join(lines[:maxLines], "\n")
	return truncated + fmt.Sprintf("\n... (还有 %d 行被截断)", len(lines)-maxLines)
}

func AnalyzeDiffSummary(diffContent string) *DiffSummary {
	summary := &DiffSummary{
		FileTypes: make(map[string]int),
	}

	scanner := bufio.NewScanner(strings.NewReader(diffContent))
	var currentFile string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "diff --git") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				currentFile = parts[2]
				if strings.HasPrefix(currentFile, "a/") {
					currentFile = currentFile[2:]
				}
				summary.TotalFiles++
			}
		} else if strings.HasPrefix(line, "+++") && currentFile != "" {
			ext := getFileExt(currentFile)
			summary.FileTypes[ext]++
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			summary.ModifiedFiles++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			summary.DeletedFiles++
		}
	}

	return summary
}

func getFileExt(path string) string {
	parts := strings.Split(path, ".")
	if len(parts) >= 2 {
		return "." + parts[len(parts)-1]
	}
	return ""
}

func GetSmartDiffSummary(files []string) string {
	var summary strings.Builder

	cmd := exec.Command("git", "diff", "--stat")
	output, err := cmd.Output()
	if err == nil {
		summary.WriteString("变更统计：\n")
		summary.WriteString(string(output))
	}

	cmd = exec.Command("git", "diff", "--numstat")
	output, err = cmd.Output()
	if err == nil {
		summary.WriteString("\n\n行数统计：\n")

		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		var largeChanges []string
		totalAdded := 0
		totalDeleted := 0

		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				var added, deleted int
				fmt.Sscanf(parts[0], "%d", &added)
				fmt.Sscanf(parts[1], "%d", &deleted)
				totalAdded += added
				totalDeleted += deleted

				if added+deleted > 200 {
					largeChanges = append(largeChanges, fmt.Sprintf("  %s: +%d -%d", parts[2], added, deleted))
				}
			}
		}

		summary.WriteString(fmt.Sprintf("总计：新增 %d 行，删除 %d 行\n", totalAdded, totalDeleted))

		if len(largeChanges) > 0 {
			summary.WriteString("\n较大变更文件：\n")
			for _, f := range largeChanges {
				summary.WriteString(f + "\n")
			}
		}
	}

	return summary.String()
}

func GetDetailedDiffInfo(files []string) string {
	var info strings.Builder

	info.WriteString("变更文件列表：\n")
	for i, f := range files {
		info.WriteString(fmt.Sprintf("  %d. %s\n", i+1, f))
	}

	stat := GetSmartDiffSummary(files)
	if stat != "" {
		info.WriteString("\n" + stat)
	}

	return info.String()
}

func FormatDiffForAI(diffContent string, maxLines int) string {
	lines := strings.Split(diffContent, "\n")

	if len(lines) <= maxLines {
		return diffContent
	}

	var result strings.Builder

	result.WriteString("以下是变更的摘要：\n\n")

	summary := AnalyzeDiffSummary(diffContent)
	result.WriteString(fmt.Sprintf("变更文件数：%d\n", summary.TotalFiles))
	result.WriteString("文件类型分布：\n")
	for ext, count := range summary.FileTypes {
		result.WriteString(fmt.Sprintf("  %s: %d 个文件\n", ext, count))
	}

	result.WriteString(fmt.Sprintf("\n详细 diff (前 %d 行):\n", maxLines))
	result.WriteString(strings.Join(lines[:maxLines], "\n"))
	result.WriteString(fmt.Sprintf("\n\n... (共 %d 行，截断 %d 行)", len(lines), len(lines)-maxLines))

	return result.String()
}
