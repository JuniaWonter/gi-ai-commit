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

func (c *Client) GenerateCommitMessageWithConventions(diffContent, description string, conventionInfo git.ConventionInfo) (string, error) {
	systemPrompt := buildGenerateSystemPrompt(conventionInfo)
	userPrompt := buildGeneratePrompt(diffContent, description)

	req := openai.ChatCompletionRequest{
		Model: c.config.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
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

	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

type ToolCallResult struct {
	ToolName string
	Args     json.RawMessage
	Result   string
}

type PendingToolCall struct {
	ID       string
	Name     string
	ArgsJSON string
	Args     map[string]interface{}
}

func (p PendingToolCall) ArgString(key string) string {
	if v, ok := p.Args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

type CommitSession struct {
	client      *Client
	messages    []openai.ChatCompletionMessage
	tools       []openai.Tool
	retryCount  int
	maxRetries  int
	toolResults []ToolCallResult
	commitMsg   string
	streaming   bool
}

type StreamChunk struct {
	Thinking string
	Content  string
	Done     bool
}

type CommitResult struct {
	Success     bool
	CommitMsg   string
	ToolResults []ToolCallResult
	Error       error
}

func (c *Client) StartCommitSession(diffContent, description string, conventionInfo git.ConventionInfo, maxRetries int) (*CommitSession, error) {
	systemPrompt := buildAuthSystemPrompt(conventionInfo)
	userPrompt := buildAuthPrompt(diffContent, description)

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: userPrompt},
	}

	sess := &CommitSession{
		client:     c,
		messages:   messages,
		tools:      buildOpenAITools(),
		maxRetries: maxRetries,
	}

	return sess, nil
}

func (s *CommitSession) StreamAI(send func(chunk StreamChunk)) ([]PendingToolCall, error) {
	s.streaming = true
	req := openai.ChatCompletionRequest{
		Model:       s.client.config.Model,
		Messages:    s.messages,
		Tools:       s.tools,
		Temperature: 0.3,
		Stream:      true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.client.config.Timeout)
	defer cancel()

	stream, err := s.client.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("调用 AI API 失败：%w", err)
	}
	defer stream.Close()

	var fullContent strings.Builder
	var fullThinking strings.Builder
	var toolCalls []openai.ToolCall

	for {
		resp, err := stream.Recv()
		if err != nil {
			break
		}

		if len(resp.Choices) == 0 {
			continue
		}

		delta := resp.Choices[0].Delta

		// Handle thinking (reasoning_content)
		if delta.ReasoningContent != "" {
			fullThinking.WriteString(delta.ReasoningContent)
			send(StreamChunk{Thinking: delta.ReasoningContent})
		}

		// Handle tool calls
		if len(delta.ToolCalls) > 0 {
			for _, tc := range delta.ToolCalls {
				if tc.Function.Name != "" {
					toolCalls = append(toolCalls, openai.ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: openai.FunctionCall{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					})
				} else if len(toolCalls) > 0 && tc.Function.Arguments != "" {
					toolCalls[len(toolCalls)-1].Function.Arguments += tc.Function.Arguments
				}
			}
			continue
		}

		// Handle content
		if delta.Content != "" {
			fullContent.WriteString(delta.Content)
			send(StreamChunk{Content: delta.Content})
		}

		if resp.Choices[0].FinishReason != "" {
			break
		}
	}

	send(StreamChunk{Done: true})
	s.streaming = false

	// Build the assistant message
	assistantMsg := openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleAssistant,
		Content: strings.TrimSpace(fullContent.String()),
	}
	if len(toolCalls) > 0 {
		assistantMsg.ToolCalls = toolCalls
	}
	s.messages = append(s.messages, assistantMsg)

	// If no tool calls, store the commit message
	if len(toolCalls) == 0 {
		msg := strings.TrimSpace(fullContent.String())
		if msg == "" {
			msg = "chore: 提交变更"
		}
		s.commitMsg = msg
		return nil, nil
	}

	var pending []PendingToolCall
	for _, tc := range toolCalls {
		var args map[string]interface{}
		json.Unmarshal(json.RawMessage(tc.Function.Arguments), &args)
		pending = append(pending, PendingToolCall{
			ID:       tc.ID,
			Name:     tc.Function.Name,
			ArgsJSON: tc.Function.Arguments,
			Args:     args,
		})
	}

	return pending, nil
}

func (s *CommitSession) ExecuteAndResume(authorized []bool) ([]PendingToolCall, error) {
	return s.ExecuteAndResumeWithStream(authorized, func(chunk StreamChunk) {})
}

func (s *CommitSession) ExecuteAndResumeWithStream(authorized []bool, send func(chunk StreamChunk)) ([]PendingToolCall, error) {
	if s.retryCount > s.maxRetries {
		classified := git.ClassifyCommitError(findLastStderr(s.toolResults))
		if classified.Category == git.ErrorUnrecoverable {
			return nil, fmt.Errorf("不可恢复的错误：%s\n建议：%s", classified.Message, classified.Suggestion)
		}
		return nil, fmt.Errorf("重试次数已达上限（%d 次），请手动处理", s.maxRetries)
	}

	for i, auth := range authorized {
		if !auth {
			s.messages = append(s.messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    "用户拒绝了此工具调用",
				ToolCallID: s.messages[len(s.messages)-1].ToolCalls[i].ID,
			})
			continue
		}

		tc := s.messages[len(s.messages)-1].ToolCalls[i]
		result := executeToolCall(tc.Function.Name, tc.Function.Arguments)
		s.toolResults = append(s.toolResults, ToolCallResult{
			ToolName: tc.Function.Name,
			Args:     json.RawMessage(tc.Function.Arguments),
			Result:   result,
		})

		if tc.Function.Name == "git_commit" || tc.Function.Name == "git_commit_amend" {
			var args struct {
				Message string `json:"message"`
			}
			json.Unmarshal(json.RawMessage(tc.Function.Arguments), &args)
			s.commitMsg = args.Message
		}

		s.messages = append(s.messages, openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    result,
			ToolCallID: tc.ID,
		})
	}

	committed := false
	for _, tr := range s.toolResults {
		if (tr.ToolName == "git_commit" || tr.ToolName == "git_commit_amend") && strings.Contains(tr.Result, "SUCCESS") {
			committed = true
			break
		}
	}

	if committed {
		return nil, nil
	}

	s.retryCount++
	return s.StreamAI(send)
}

func (s *CommitSession) GetResult() CommitResult {
	if s.commitMsg != "" {
		return CommitResult{
			Success:     true,
			CommitMsg:   s.commitMsg,
			ToolResults: s.toolResults,
		}
	}
	return CommitResult{
		Success:     false,
		ToolResults: s.toolResults,
		Error:       fmt.Errorf("提交未完成"),
	}
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
			return fmt.Sprintf("EXISTS: commit-msg hook 存在于 %s", info.HookPath)
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

func buildGenerateSystemPrompt(conventionInfo git.ConventionInfo) string {
	var b strings.Builder
	b.WriteString("你是一个 Git commit message 生成助手。\n")
	b.WriteString("你的职责是根据代码变更生成合适的 commit message。\n")
	b.WriteString("只返回 commit message 内容，不要其他说明。\n")
	b.WriteString("不要调用任何工具，直接返回文本。\n\n")

	if conventionInfo.HookExists {
		b.WriteString("重要：该仓库有 commit-msg hook 约束！\n")
		b.WriteString("Hook 内容摘要：\n")
		b.WriteString(truncate(conventionInfo.HookContent, 800))
		b.WriteString("\n\n")
		b.WriteString("你必须确保 commit message 符合 hook 要求。\n\n")
	}

	if conventionInfo.TemplateExists {
		b.WriteString("该仓库有 commit message 模板：\n")
		b.WriteString(conventionInfo.TemplateContent)
		b.WriteString("\n\n")
		b.WriteString("请参考模板格式生成。\n")
	}

	if len(conventionInfo.RecentMessages) > 0 {
		b.WriteString("该仓库最近几次 commit message（参考风格）：\n")
		for _, entry := range conventionInfo.RecentMessages {
			b.WriteString(fmt.Sprintf("- %s\n", entry.Message))
		}
		b.WriteString("\n请遵循已有的风格格式。\n")
	}

	return b.String()
}

func buildGeneratePrompt(diffContent, description string) string {
	var b strings.Builder

	b.WriteString("请根据以下代码变更生成 commit message。\n\n")

	if description != "" {
		b.WriteString("项目描述：\n")
		b.WriteString(description)
		b.WriteString("\n\n")
	}

	b.WriteString("代码变更：\n")
	b.WriteString(diffContent)
	b.WriteString("\n\n")

	b.WriteString("要求：\n")
	b.WriteString("1. 使用中文，只返回 commit message 内容\n")
	b.WriteString("2. 格式：<type>(<scope>): <subject>\n")
	b.WriteString("3. type 可选：feat, fix, docs, style, refactor, test, chore\n")

	return b.String()
}

func buildAuthSystemPrompt(conventionInfo git.ConventionInfo) string {
	var b strings.Builder
	b.WriteString("你是一个 Git commit message 生成助手。\n")
	b.WriteString("你的职责是分析代码变更，生成 commit message，并使用工具提交。\n")
	b.WriteString("每次调用工具前，用户会审核并授权。\n")
	b.WriteString("如果提交失败，根据错误信息调整后使用 git_commit_amend 修正。\n\n")

	if conventionInfo.HookExists {
		b.WriteString("重要：该仓库有 commit-msg hook 约束！\n")
		b.WriteString("Hook 内容：\n")
		b.WriteString(truncate(conventionInfo.HookContent, 800))
		b.WriteString("\n\n")
		b.WriteString("你必须确保 commit message 符合 hook 要求。\n\n")
	}

	if conventionInfo.TemplateExists {
		b.WriteString("该仓库有 commit message 模板：\n")
		b.WriteString(conventionInfo.TemplateContent)
		b.WriteString("\n\n")
		b.WriteString("请参考模板格式生成。\n")
	}

	if len(conventionInfo.RecentMessages) > 0 {
		b.WriteString("该仓库最近几次 commit message（参考风格）：\n")
		for _, entry := range conventionInfo.RecentMessages {
			b.WriteString(fmt.Sprintf("- %s\n", entry.Message))
		}
		b.WriteString("\n请遵循已有的风格格式。\n\n")
	}

	b.WriteString("规则：\n")
	b.WriteString("1. 分析变更后调用 git_commit 工具提交\n")
	b.WriteString("2. 如果提交失败且错误可恢复，调整后调用 git_commit_amend\n")
	b.WriteString("3. 如果是不可恢复的错误，不要重试，返回文本说明原因\n")
	b.WriteString("4. 最多重试 3 次\n")
	b.WriteString("5. 使用中文\n")

	return b.String()
}

func buildAuthPrompt(diffContent, description string) string {
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

	b.WriteString("请分析变更，调用 git_commit 工具提交。如果需要了解仓库格式，可以调用 git_hook_check 或 git_log_recent。\n")

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
