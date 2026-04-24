package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/oliver/git-ai-commit/internal/ai"
	"github.com/oliver/git-ai-commit/internal/config"
	"github.com/oliver/git-ai-commit/internal/counter"
	"github.com/oliver/git-ai-commit/internal/description"
	"github.com/oliver/git-ai-commit/internal/diff"
	"github.com/oliver/git-ai-commit/internal/loading"
	"github.com/oliver/git-ai-commit/internal/project"
	"github.com/oliver/git-ai-commit/tui"
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

	fmt.Println("⚙️  加载配置...")
	cfg, err := config.Load()
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

	payloads, err := diffProcessor.BuildPayloadsForFiles(nil)
	if err != nil {
		return fmt.Errorf("获取代码变更失败：%w", err)
	}
	if len(payloads) == 0 {
		return fmt.Errorf("没有检测到任何代码变更")
	}

	diffContent := payloads[0].Content
	diffMode := payloads[0].Mode

	if strings.TrimSpace(diffContent) == "" {
		return fmt.Errorf("没有实际变更")
	}

	fmt.Println("📋 检查仓库描述...")
	desc, _, err := handleDescription(diffContent, cfg)
	if err != nil {
		return fmt.Errorf("处理描述失败：%w", err)
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
	client, err := ai.NewClient(ai.Config{
		APIKey:  modelCfg.APIKey,
		Model:   modelCfg.Model,
		BaseURL: modelCfg.BaseURL,
		Timeout: modelCfg.GetTimeout(),
	})
	if err != nil {
		return fmt.Errorf("初始化 AI 客户端失败：%w", err)
	}

	fmt.Println("🚀 进入交互界面...")
	result, err := tui.RunCommitFlow(files, tui.CommitFlowOptions{
		AutoConfirm: opts.AutoConfirm,
		DryRun:      opts.DryRun,
		Desc:        desc,
		DiffContent: diffContent,
		DiffMode:    diffMode,
		Client:      client,
	})
	if err != nil {
		return fmt.Errorf("TUI 运行失败：%w", err)
	}

	if !result.Success {
		return fmt.Errorf("用户取消提交")
	}

	fmt.Println("📊 更新计数...")
	if err := counter.Increment(); err != nil {
		return fmt.Errorf("更新计数失败：%w", err)
	}

	fmt.Println("✅ 提交成功!")
	if result.TotalTokens > 0 {
		fmt.Printf("   Token 消耗: prompt=%d  completion=%d  total=%d\n", result.PromptTokens, result.CompletionTokens, result.TotalTokens)
	}
	return nil
}

func isGitRepo() bool {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func handleDescription(diffContent string, cfg *config.Config) (string, bool, error) {
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

	fileInfo, err := project.GetFileSummary(projRoot, nil)
	if err != nil {
		return "", false, err
	}

	modelCfg, err := cfg.GetModelConfig(cfg.AI.DefaultModel)
	if err != nil {
		return "", false, err
	}

	client, err := ai.NewClient(ai.Config{
		APIKey:  modelCfg.APIKey,
		Model:   modelCfg.Model,
		BaseURL: modelCfg.BaseURL,
		Timeout: modelCfg.GetTimeout(),
	})
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

func getProjectRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("获取项目根目录失败：%w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
