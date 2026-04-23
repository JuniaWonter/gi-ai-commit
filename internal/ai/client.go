package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/oliver/git-ai-commit/internal/git"
)

type Client struct {
	client *openai.Client
	config Config
}

type Config struct {
	APIKey  string
	Model   string
	BaseURL string
	Timeout time.Duration
}

func NewClient(config Config) (*Client, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("API Key 未配置")
	}

	c := openai.DefaultConfig(config.APIKey)
	if config.BaseURL != "" {
		c.BaseURL = config.BaseURL
	}

	client := openai.NewClientWithConfig(c)

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	return &Client{
		client: client,
		config: config,
	}, nil
}

func (c *Client) GenerateCommitMessage(diffContent, description string) (string, error) {
	prompt := buildPrompt(diffContent, description)

	req := openai.ChatCompletionRequest{
		Model: c.config.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "你是一个 Git commit message 生成助手，遵循 Conventional Commits 规范。",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Temperature: 0.3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.config.Timeout)
	defer cancel()

	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("调用 AI API 失败：%w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("API 返回空响应")
	}

	message := strings.TrimSpace(resp.Choices[0].Message.Content)
	return message, nil
}

type ToolCallResult struct {
	ToolName string
	Args     json.RawMessage
	Result   string
}

func (c *Client) CommitWithRetry(diffContent, description string, conventionInfo git.ConventionInfo, maxRetries int) (string, []ToolCallResult, error) {
	systemPrompt := buildSystemPrompt(conventionInfo)
	userPrompt := buildRetryPrompt(diffContent, description, conventionInfo)

	tools := buildOpenAITools()

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: userPrompt},
	}

	var toolResults []ToolCallResult
	var lastCommitMsg string
	retryCount := 0

	for retryCount <= maxRetries {
		req := openai.ChatCompletionRequest{
			Model:       c.config.Model,
			Messages:    messages,
			Tools:       tools,
			Temperature: 0.3,
		}

		ctx, cancel := context.WithTimeout(context.Background(), c.config.Timeout)
		resp, err := c.client.CreateChatCompletion(ctx, req)
		cancel()

		if err != nil {
			return "", toolResults, fmt.Errorf("调用 AI API 失败：%w", err)
		}

		if len(resp.Choices) == 0 {
			return "", toolResults, fmt.Errorf("API 返回空响应")
		}

		choice := resp.Choices[0]

		if choice.Message.ToolCalls == nil || len(choice.Message.ToolCalls) == 0 {
			lastCommitMsg = strings.TrimSpace(choice.Message.Content)
			if lastCommitMsg == "" {
				lastCommitMsg = "chore: 提交变更"
			}
			return lastCommitMsg, toolResults, nil
		}

		messages = append(messages, choice.Message)

		for _, toolCall := range choice.Message.ToolCalls {
			result := executeToolCall(toolCall.Function.Name, toolCall.Function.Arguments)
			toolResults = append(toolResults, ToolCallResult{
				ToolName: toolCall.Function.Name,
				Args:     json.RawMessage(toolCall.Function.Arguments),
				Result:   result,
			})

			if toolCall.Function.Name == "git_commit" || toolCall.Function.Name == "git_commit_amend" {
				var args struct {
					Message string `json:"message"`
				}
				json.Unmarshal(json.RawMessage(toolCall.Function.Arguments), &args)
				lastCommitMsg = args.Message
			}

			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result,
				ToolCallID: toolCall.ID,
			})
		}

		committed := false
		for _, tr := range toolResults {
			if tr.ToolName == "git_commit" || tr.ToolName == "git_commit_amend" {
				if strings.Contains(tr.Result, "SUCCESS") {
					committed = true
					break
				}
			}
		}

		if committed {
			return lastCommitMsg, toolResults, nil
		}

		retryCount++
		if retryCount > maxRetries {
			classified := git.ClassifyCommitError(findLastStderr(toolResults))
			if classified.Category == git.ErrorUnrecoverable {
				return "", toolResults, fmt.Errorf("不可恢复的错误：%s\n建议：%s", classified.Message, classified.Suggestion)
			}
			return lastCommitMsg, toolResults, fmt.Errorf("重试次数已达上限（%d 次），请手动处理\n最后错误：%s", maxRetries, classified.RawStderr)
		}
	}

	return lastCommitMsg, toolResults, nil
}

func executeToolCall(name, argsJSON string) string {
	switch name {
	case "git_commit":
		var args struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		result := git.Commit(args.Message)
		if result.Success {
			return fmt.Sprintf("SUCCESS: 提交成功 %s", result.Hash)
		}
		classified := git.ClassifyCommitError(result.Stderr)
		return fmt.Sprintf("FAILED: %s\n分类：%s\n建议：%s\n原始错误：%s",
			classified.Message, categoryLabel(classified.Category), classified.Suggestion, classified.RawStderr)

	case "git_commit_amend":
		var args struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		result := git.CommitAmend(args.Message)
		if result.Success {
			return fmt.Sprintf("SUCCESS: amend 成功 %s", result.Hash)
		}
		classified := git.ClassifyCommitError(result.Stderr)
		return fmt.Sprintf("FAILED: %s\n分类：%s\n建议：%s\n原始错误：%s",
			classified.Message, categoryLabel(classified.Category), classified.Suggestion, classified.RawStderr)

	case "git_log_recent":
		var args struct {
			Count int `json:"count"`
		}
		json.Unmarshal(json.RawMessage(argsJSON), &args)
		if args.Count <= 0 {
			args.Count = 5
		}
		entries := git.GetRecentCommits(args.Count)
		var b strings.Builder
		for _, e := range entries {
			b.WriteString(fmt.Sprintf("%s %s\n", e.Hash, e.Message))
		}
		return b.String()

	case "git_hook_check":
		info := git.DetectConventions()
		if info.HookExists {
			return fmt.Sprintf("EXISTS: commit-msg hook 存在于 %s\n内容摘要：\n%s", info.HookPath, truncate(info.HookContent, 500))
		}
		return "NOT_FOUND: 仓库没有 commit-msg hook"

	case "git_config_get":
		var args struct {
			Key string `json:"key"`
		}
		json.Unmarshal(json.RawMessage(argsJSON), &args)
		val, err := git.GetConfigValue(args.Key)
		if err != nil {
			return fmt.Sprintf("NOT_FOUND: 配置项 %s 不存在", args.Key)
		}
		return fmt.Sprintf("VALUE: %s=%s", args.Key, val)

	default:
		return fmt.Sprintf("ERROR: 未知工具 %s", name)
	}
}

func categoryLabel(cat git.ErrorCategory) string {
	switch cat {
	case git.ErrorRecoverable:
		return "可恢复"
	case git.ErrorUnrecoverable:
		return "不可恢复"
	default:
		return "未知"
	}
}

func findLastStderr(results []ToolCallResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].ToolName == "git_commit" || results[i].ToolName == "git_commit_amend" {
			if strings.Contains(results[i].Result, "FAILED") {
				return extractRawError(results[i].Result)
			}
		}
	}
	return ""
}

func extractRawError(result string) string {
	idx := strings.Index(result, "原始错误：")
	if idx >= 0 {
		return result[idx+len("原始错误："):]
	}
	return result
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(已截断)"
}

func buildOpenAITools() []openai.Tool {
	var tools []openai.Tool
	for _, td := range git.ToolDefinitions {
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.Parameters,
			},
		})
	}
	return tools
}

func buildSystemPrompt(conventionInfo git.ConventionInfo) string {
	var b strings.Builder
	b.WriteString("你是一个 Git commit message 生成助手。\n")
	b.WriteString("你的职责是根据代码变更生成合适的 commit message 并使用 git_commit 工具提交。\n")
	b.WriteString("如果提交失败，你需要根据错误信息调整 commit message 格式，然后使用 git_commit_amend 修正。\n\n")

	if conventionInfo.HookExists {
		b.WriteString("重要：该仓库有 commit-msg hook 约束！\n")
		b.WriteString("Hook 内容：\n")
		b.WriteString(truncate(conventionInfo.HookContent, 800))
		b.WriteString("\n\n")
		b.WriteString("你必须确保 commit message 符合 hook 要求，否则提交会被拒绝。\n\n")
	}

	if conventionInfo.TemplateExists {
		b.WriteString("该仓库有 commit message 模板：\n")
		b.WriteString(conventionInfo.TemplateContent)
		b.WriteString("\n\n")
		b.WriteString("请参考模板格式生成 commit message。\n\n")
	}

	if len(conventionInfo.RecentMessages) > 0 {
		b.WriteString("该仓库最近几次 commit message（参考风格）：\n")
		for _, entry := range conventionInfo.RecentMessages {
			b.WriteString(fmt.Sprintf("- %s\n", entry.Message))
		}
		b.WriteString("\n请遵循已有的风格格式。\n\n")
	}

	b.WriteString("规则：\n")
	b.WriteString("1. 先生成 commit message，然后调用 git_commit 工具提交\n")
	b.WriteString("2. 如果提交失败且错误是可恢复的，调整格式后调用 git_commit_amend\n")
	b.WriteString("3. 如果是不可恢复的错误（如没有变更可提交），不要重试，直接告知用户\n")
	b.WriteString("4. 最多重试 3 次\n")
	b.WriteString("5. 使用中文\n")
	b.WriteString("6. 只返回最终确认的 commit message 内容\n")

	return b.String()
}

func buildRetryPrompt(diffContent, description string, conventionInfo git.ConventionInfo) string {
	var b strings.Builder

	b.WriteString("请根据以下代码变更生成 commit message 并提交。\n\n")

	if description != "" {
		b.WriteString("项目描述：\n")
		b.WriteString(description)
		b.WriteString("\n\n")
	}

	b.WriteString("代码变更：\n")
	b.WriteString(diffContent)
	b.WriteString("\n\n")

	b.WriteString("请先分析变更内容，然后调用 git_commit 工具提交。\n")
	b.WriteString("如果需要了解仓库格式约束，可以先调用 git_hook_check 或 git_log_recent。\n")

	return b.String()
}

func (c *Client) GenerateDescription(projectInfo, fileInfo, diffContent string) (string, error) {
	prompt := fmt.Sprintf(`请分析以下项目信息，生成一个简洁的项目描述（100-200 字）。

项目信息：
%s

变更文件信息：
%s

代码变更（参考）：
%s

请描述：
1. 这是什么类型的项目
2. 主要功能是什么
3. 使用了什么技术栈

只返回描述内容，不要其他说明。`, projectInfo, fileInfo, diffContent)

	req := openai.ChatCompletionRequest{
		Model: c.config.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "你是一个代码分析助手，擅长理解项目结构和功能。",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Temperature: 0.5,
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.config.Timeout)
	defer cancel()

	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("调用 AI API 失败：%w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("API 返回空响应")
	}

	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func buildPrompt(diffContent, description string) string {
	var prompt strings.Builder

	prompt.WriteString("请根据以下代码变更生成 Git commit message。\n\n")

	if description != "" {
		prompt.WriteString("项目描述：\n")
		prompt.WriteString(description)
		prompt.WriteString("\n\n")
	}

	prompt.WriteString("代码变更：\n")
	prompt.WriteString(diffContent)
	prompt.WriteString("\n\n")

	prompt.WriteString("要求：\n")
	prompt.WriteString("1. 使用中文\n")
	prompt.WriteString("2. 格式：<type>(<scope>): <subject>\n")
	prompt.WriteString("3. type 可选：feat, fix, docs, style, refactor, test, chore\n")
	prompt.WriteString("4. scope 根据变更内容填写（如 auth, user, api 等）\n")
	prompt.WriteString("5. 如有必要添加 body 详细说明\n")
	prompt.WriteString("6. 只返回 commit message 内容，不要其他说明\n")

	return prompt.String()
}