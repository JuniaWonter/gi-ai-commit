package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/oliver/git-ai-commit/internal/ai"
	"github.com/oliver/git-ai-commit/internal/config"
	"github.com/oliver/git-ai-commit/internal/counter"
	"github.com/oliver/git-ai-commit/internal/description"
	"github.com/oliver/git-ai-commit/internal/diff"
	"github.com/oliver/git-ai-commit/internal/git"
	"github.com/oliver/git-ai-commit/internal/loading"
	"github.com/oliver/git-ai-commit/internal/project"
	"github.com/oliver/git-ai-commit/tui"
)

var (
	errUserCancelled = errors.New("用户取消提交")
	ErrUserCancelled = errUserCancelled
)

type CommitOptions struct {
	AutoConfirm bool
	DryRun      bool
	Model       string
}

func RunCommit(opts CommitOptions) error {
	fmt.Println("📊 检查 Git 仓库...")
	if !isGitRepo() {
		return fmt.Errorf("当前目录不是 Git 仓库")
	}

	fmt.Println("📊 获取变更文件...")
	files, err := diff.GetChangedFiles()
	if err != nil {
		return fmt.Errorf("获取变更文件失败：%w", err)
	}

	if len(files) == 0 {
		return fmt.Errorf("没有变更的文件")
	}

	var selectedFiles []string

	if opts.DryRun {
		selectedFiles, _ = selectFilesSimple(files)
	} else {
		selectedFiles, _ = tui.SelectFiles(files)
		if err != nil {
			if strings.Contains(err.Error(), "no such device") || strings.Contains(err.Error(), "TTY") {
				selectedFiles, _ = selectFilesSimple(files)
			} else {
				return fmt.Errorf("选择文件失败：%w", err)
			}
		}
	}

	if len(selectedFiles) == 0 {
		return fmt.Errorf("未选择任何文件")
	}

	fmt.Println("📦 暂存文件...")
	if err := diff.StageFiles(selectedFiles); err != nil {
		return err
	}

	var diffContent string
	var cfg *config.Config
	var client *ai.Client
	var desc string

	success := false
	defer func() {
		if !success && !opts.DryRun {
			fmt.Println("🔄 回滚暂存的文件...")
			if err := resetStagedFiles(selectedFiles); err != nil {
				fmt.Printf("⚠️  回滚失败: %v\n", err)
			} else {
				fmt.Println("✅ 已回滚")
			}
		}
	}()

	fmt.Println("⚙️  加载配置...")
	cfg, err = config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败：%w", err)
	}

	fmt.Println("📊 获取代码变更...")
	gitRoot, err := getProjectRoot()
	if err != nil {
		return fmt.Errorf("获取项目根目录失败：%w", err)
	}

	diffProcessor := diff.NewDiffProcessor(diff.DiffPromptConfig{
		MaxFullDiffBytes:    cfg.DiffPrompt.MaxFullDiffBytes,
		MaxCompactDiffBytes: cfg.DiffPrompt.MaxCompactDiffBytes,
		MaxPerFileDiffBytes: cfg.DiffPrompt.MaxPerFileDiffBytes,
		MaxCompactDiffFiles: cfg.DiffPrompt.MaxCompactDiffFiles,
	}, gitRoot)

	payloads, err := diffProcessor.BuildPayloadsForFiles(selectedFiles)
	if err != nil {
		return fmt.Errorf("获取代码变更失败：%w", err)
	}
	if len(payloads) == 0 {
		return fmt.Errorf("没有检测到任何代码变更")
	}

	diffContent = payloads[0].Content
	diffMode := payloads[0].Mode

	if strings.TrimSpace(diffContent) == "" {
		return fmt.Errorf("选中的文件没有实际变更")
	}

	fmt.Println("🤖 初始化 AI 客户端...")
	modelName := opts.Model
	if modelName == "" {
		modelName = cfg.AI.DefaultModel
	}
	modelCfg, err := cfg.GetModelConfig(modelName)
	if err != nil {
		return fmt.Errorf("获取模型配置失败：%w", err)
	}
	client, err = ai.NewClient(ai.Config{
		APIKey:  modelCfg.APIKey,
		Model:   modelCfg.Model,
		BaseURL: modelCfg.BaseURL,
		Timeout: modelCfg.GetTimeout(),
	})
	if err != nil {
		return fmt.Errorf("初始化 AI 客户端失败：%w", err)
	}

	fmt.Println("📋 检查仓库描述...")
	desc, _, err = handleDescription(client, diffContent, selectedFiles, cfg)
	if err != nil {
		return fmt.Errorf("处理描述失败：%w", err)
	}

	fmt.Println("🤖 生成 commit message...")
	if diffMode != "完整 diff" {
		fmt.Printf("ℹ️  变更较大，已使用 %s 模式\n", diffMode)
	}

	conventionInfo := git.DetectConventions()

	spinner := loading.New("正在生成并提交 commit...")
	spinner.Start()
	commitMessage, toolResults, err := client.CommitWithRetry(diffContent, desc, conventionInfo, 3)
	spinner.Stop("处理完成")

	if err != nil {
		for _, tr := range toolResults {
			fmt.Printf("  工具 %s → %s\n", tr.ToolName, truncateResult(tr.Result, 200))
		}
		return fmt.Errorf("提交失败：%w", err)
	}

	fmt.Println("\n📝 最终 commit message:")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Println(commitMessage)
	fmt.Println(strings.Repeat("─", 50))

	if !opts.AutoConfirm && !opts.DryRun {
		fmt.Print("\n确认提交结果？(Y/n/e=编辑): ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("读取输入失败：%w", err)
		}

		input = strings.TrimSpace(strings.ToLower(input))
		if input == "n" || input == "no" {
			git.CommitAmend("chore: 回滚 AI 提交")
			return errUserCancelled
		}
		if input == "e" || input == "edit" {
			fmt.Print("输入新的 commit message: ")
			newMsg, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("读取输入失败：%w", err)
			}
			newMsg = strings.TrimSpace(newMsg)
			if newMsg != "" {
				amendResult := git.CommitAmend(newMsg)
				if !amendResult.Success {
					return fmt.Errorf("修改提交失败：%s", amendResult.Stderr)
				}
				commitMessage = newMsg
			}
		}
	}

	if opts.DryRun {
		fmt.Println("🔍 Dry-run 模式，不执行提交")
		success = true
		return nil
	}

	fmt.Println("📊 更新计数...")
	if err := counter.Increment(); err != nil {
		return fmt.Errorf("更新计数失败：%w", err)
	}

	fmt.Println("✅ 提交成功!")
	success = true
	return nil
}

func isGitRepo() bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func selectFilesSimple(files []diff.FileChange) ([]string, bool) {
	fmt.Println("📝 选择要提交的文件:")
	for i, f := range files {
		fmt.Printf("  %d. %s\n", i+1, f.Path)
	}
	fmt.Print("输入要提交的文件编号（多个用逗号分隔，直接回车选择全部）: ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		var all []string
		for _, f := range files {
			all = append(all, f.Path)
		}
		return all, true
	}

	var selected []string
	parts := strings.Split(input, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if idx, err := strconv.Atoi(p); err == nil && idx > 0 && idx <= len(files) {
			selected = append(selected, files[idx-1].Path)
		}
	}
	return selected, len(selected) > 0
}

func handleDescription(client *ai.Client, diffContent string, files []string, cfg *config.Config) (string, bool, error) {
	exists, err := description.Exists()
	if err != nil {
		return "", false, err
	}

	count, err := counter.Get()
	if err != nil {
		return "", false, err
	}

	const updateInterval = 10

	projRoot, err := getProjectRoot()
	if err != nil {
		return "", false, err
	}

	projectInfo, err := project.GetSummary(projRoot)
	if err != nil {
		return "", false, err
	}

	fileInfo, err := project.GetFileSummary(projRoot, files)
	if err != nil {
		return "", false, err
	}

	if !exists {
		fmt.Println("📝 首次提交，生成仓库描述...")
		limitedDiff := diff.LimitDiffLines(diffContent, 100)

		spinner := loading.New("正在生成项目描述...")
		spinner.Start()
		desc, err := client.GenerateDescription(projectInfo, fileInfo, limitedDiff)
		spinner.Stop("生成完成")
		if err != nil {
			return "", false, fmt.Errorf("生成描述失败：%w", err)
		}

		if err := description.Write(desc); err != nil {
			return "", false, fmt.Errorf("保存描述失败：%w", err)
		}

		fmt.Println("✅ 仓库描述已创建")
		return desc, true, nil
	}

	if description.ShouldUpdate(count, updateInterval) {
		fmt.Println("📝 达到更新间隔，更新仓库描述...")
		limitedDiff := diff.LimitDiffLines(diffContent, 100)

		spinner := loading.New("正在更新项目描述...")
		spinner.Start()
		desc, err := client.GenerateDescription(projectInfo, fileInfo, limitedDiff)
		spinner.Stop("更新完成")
		if err != nil {
			desc, _ = description.Read()
			return desc, true, nil
		}

		if err := description.Write(desc); err != nil {
			return "", false, fmt.Errorf("更新描述失败：%w", err)
		}

		fmt.Println("✅ 仓库描述已更新")
		return desc, true, nil
	}

	desc, err := description.Read()
	if err != nil {
		return "", false, err
	}

	return desc, true, nil
}

func truncateResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func executeCommit(message string) error {
	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func getProjectRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("获取项目根目录失败：%w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func resetStagedFiles(files []string) error {
	if len(files) == 0 {
		return nil
	}

	gitRoot, err := getProjectRoot()
	if err != nil {
		return err
	}

	args := append([]string{"reset"}, files...)
	cmd := exec.Command("git", args...)
	cmd.Dir = gitRoot
	return cmd.Run()
}
