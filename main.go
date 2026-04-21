package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/oliver/git-ai-commit/cmd"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "commit":
		commitCmd := flag.NewFlagSet("commit", flag.ExitOnError)
		autoConfirm := commitCmd.Bool("y", false, "自动确认提交")
		autoConfirmLong := commitCmd.Bool("yes", false, "自动确认提交")
		dryRun := commitCmd.Bool("dry-run", false, "只预览不提交")
		dryRunShort := commitCmd.Bool("d", false, "只预览不提交")
		model := commitCmd.String("model", "", "指定使用的 AI 模型")

		commitCmd.Parse(os.Args[2:])

		opts := cmd.CommitOptions{
			AutoConfirm: *autoConfirm || *autoConfirmLong,
			DryRun:      *dryRun || *dryRunShort,
			Model:       *model,
		}

		if err := cmd.RunCommit(opts); err != nil {
			if errors.Is(err, cmd.ErrUserCancelled) {
				fmt.Println("❌ 已取消提交")
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "❌ 错误：%v\n", err)
			os.Exit(1)
		}

	case "version", "-v", "--version":
		fmt.Printf("git-ai-commit version %s\n", version)

	case "help", "-h", "--help":
		printUsage()

	default:
		fmt.Fprintf(os.Stderr, "未知命令：%s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`git-ai-commit - AI 驱动 Git commit 工具

用法:
  git-ai-commit <command> [options]

命令:
  commit      使用 AI 生成 commit message 并提交
  version     显示版本信息
  help        显示帮助信息

选项:
  -y, --yes       自动确认提交，不显示预览
  -d, --dry-run   只预览不提交
  --model         指定使用的 AI 模型（覆盖配置文件）

示例:
  git ai commit                # 预览模式
  git ai commit -y             # 自动提交
  git ai commit --model qwen-plus  # 使用通义千问模型
  git ai commit --model deepseek-chat  # 使用 DeepSeek 模型

配置文件:
  ~/.config/ai-commit/config.yaml`)
}
