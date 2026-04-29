package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oliver/git-ai-commit/internal/git"
	openai "github.com/sashabaranov/go-openai"
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
	client           *Client
	messages         []openai.ChatCompletionMessage
	tools            []openai.Tool
	retryCount       int
	maxRetries       int
	loopCount        int
	maxLoops         int
	toolResults      []ToolCallResult
	commitMsg        string
	streaming        bool
	promptTokens     int
	completionTokens int
	totalTokens      int
	readFileCalls    int
	listTreeCalls    int
	maxReadFileCalls int
	maxListTreeCalls int
	compactMode        bool
	noToolCallFallback bool
	toolCache          map[string]string
	readFiles          map[string]bool // tracks which files have been read to prevent re-reads
	mu                 sync.Mutex
}

type StreamChunk struct {
	Thinking string
	Content  string
	Done     bool
}

type CommitResult struct {
	Success          bool
	CommitMsg        string
	ToolResults      []ToolCallResult
	Error            error
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

func (c *Client) StartCommitSession(diffContent, description string, conventionInfo git.ConventionInfo, maxRetries int, selectedFiles []string) (*CommitSession, error) {
	scopeHints := inferScopeHints(selectedFiles)
	
	// 估计 token 数，决定是否使用紧凑模式
	systemPrompt := buildAuthSystemPrompt(conventionInfo, scopeHints)
	userPrompt := buildAuthPrompt(diffContent, description)
	
	estimatedTokens := estimateTokenCount(systemPrompt + userPrompt)
	compactMode := estimatedTokens > 6000 // 预留安全余量（假设 8K token 限制）
	
	// 如果接近超限，启用紧凑模式
	if compactMode {
		systemPrompt = buildAuthSystemPromptCompact(conventionInfo, scopeHints)
		userPrompt = buildAuthPromptCompact(diffContent, description)
	}

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: userPrompt},
	}

	sess := &CommitSession{
		client:           c,
		messages:         messages,
		tools:            buildOpenAITools(),
		maxRetries:       maxRetries,
		maxLoops:         10,
		maxReadFileCalls: envIntOrDefault("GIT_AI_MAX_READ_FILE_CALLS", 4),
		maxListTreeCalls: envIntOrDefault("GIT_AI_MAX_LIST_TREE_CALLS", 1),
		compactMode:      compactMode,
			toolCache:        make(map[string]string),
			readFiles:        make(map[string]bool),
	}

	return sess, nil
}

func (s *CommitSession) StreamAI(send func(chunk StreamChunk)) ([]PendingToolCall, error) {
	s.streaming = true
	maxTokens := envIntOrDefault("GIT_AI_MAX_COMPLETION_TOKENS", 2000)
	req := openai.ChatCompletionRequest{
		Model:                s.client.config.Model,
		Messages:             s.messages,
		Tools:                s.tools,
		Temperature:          0.3,
		Stream:               true,
		MaxCompletionTokens:  maxTokens,
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
	var finishReason openai.FinishReason

	for {
		resp, err := stream.Recv()
		if err != nil {
			break
		}

		// Accumulate usage from stream chunks
		if resp.Usage != nil {
			s.promptTokens += resp.Usage.PromptTokens
			s.completionTokens += resp.Usage.CompletionTokens
			s.totalTokens += resp.Usage.TotalTokens
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
			finishReason = resp.Choices[0].FinishReason
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
	
	// 【关键】检测是否因为输出超限被截断
	// finish_reason="length" 是 API 返回的最可靠截断信号
		// 启发式规则 isTruncationSignal 作为兜底
	isOutputTruncated := len(toolCalls) == 0 && (finishReason == openai.FinishReasonLength || isTruncationSignal(fullContent.String()))

	// 【关键】如果没有 tool_calls，需要诊断原因
	if len(toolCalls) == 0 {
		msg := strings.TrimSpace(fullContent.String())
		
		// 如果被检测到输出被截断
		if isOutputTruncated {
			s.noToolCallFallback = true
			// 使用提取的最后一条 commit message，或默认消息
			extractedMsg := extractCommitMessageFromTruncated(msg)
			if extractedMsg == "" {
				extractedMsg = "chore: 提交变更"
			}
			fallbackTC := PendingToolCall{
				ID:       "fallback_commit_1",
				Name:     "git_commit",
				ArgsJSON: fmt.Sprintf(`{"message": "%s"}`, escapeJSON(extractedMsg)),
				Args: map[string]interface{}{
					"message": extractedMsg,
				},
			}
			return []PendingToolCall{fallbackTC}, nil
		}
		
		// 如果内容为空或过短（< 20 字），也按降级处理
		if len(msg) < 20 {
			s.noToolCallFallback = true
			msg = "chore: 提交变更"
			fallbackTC := PendingToolCall{
				ID:       "fallback_commit_1",
				Name:     "git_commit",
				ArgsJSON: fmt.Sprintf(`{"message": "%s"}`, escapeJSON(msg)),
				Args: map[string]interface{}{
					"message": msg,
				},
			}
			return []PendingToolCall{fallbackTC}, nil
		}
		
		// 正常情况：没有 tool calls，存储为最终消息
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

func (s *CommitSession) ExecuteAndResume(pending []PendingToolCall, authorized []bool) ([]PendingToolCall, error) {
	return s.ExecuteAndResumeWithStream(pending, authorized, func(chunk StreamChunk) {})
}

func (s *CommitSession) ExecuteAndResumeWithStream(pending []PendingToolCall, authorized []bool, send func(chunk StreamChunk)) ([]PendingToolCall, error) {
	if s.loopCount > s.maxLoops {
		return nil, fmt.Errorf("工具调用轮次过多（%d 次），AI 可能陷入循环，请手动处理", s.loopCount)
	}
	if s.retryCount > s.maxRetries {
		classified := git.ClassifyCommitError(findLastStderr(s.toolResults))
		if classified.Category == git.ErrorUnrecoverable {
			return nil, fmt.Errorf("不可恢复的错误：%s\n建议：%s", classified.Message, classified.Suggestion)
		}
		return nil, fmt.Errorf("提交失败次数达上限（%d 次），请手动处理", s.maxRetries)
	}

	type execResult struct {
			index  int
			result string
		}

		results := make([]execResult, len(pending))
		rejected := make([]bool, len(pending))

		// Add rejection messages for unauthorized calls
		for i, auth := range authorized {
			if i >= len(pending) {
				break
			}
			if !auth {
				rejected[i] = true
				s.messages = append(s.messages, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    "用户拒绝了此工具调用",
					ToolCallID: pending[i].ID,
				})
			}
		}

		// Execute non-commit tools in parallel
		var wg sync.WaitGroup
		for i, tc := range pending {
			if i >= len(authorized) {
				break
			}
			if rejected[i] {
				continue
			}
			if tc.Name == "git_commit" || tc.Name == "git_commit_amend" {
				continue
			}
			wg.Add(1)
			go func(idx int, call PendingToolCall) {
				defer wg.Done()
				results[idx] = execResult{index: idx, result: s.executeToolCallWithLimit(call.Name, call.ArgsJSON)}
			}(i, tc)
		}
		wg.Wait()

		// Execute commit tools sequentially
		for i, tc := range pending {
			if i >= len(authorized) {
				break
			}
			if rejected[i] {
				continue
			}
			if tc.Name != "git_commit" && tc.Name != "git_commit_amend" {
				continue
			}
			results[i] = execResult{index: i, result: s.executeToolCallWithLimit(tc.Name, tc.ArgsJSON)}
		}

		// Append results in original order
		for _, r := range results {
			if r.result == "" && rejected[r.index] {
				continue
			}
			tc := pending[r.index]
			s.toolResults = append(s.toolResults, ToolCallResult{
				ToolName: tc.Name,
				Args:     json.RawMessage(tc.ArgsJSON),
				Result:   r.result,
			})

			if tc.Name == "git_commit" || tc.Name == "git_commit_amend" {
				var args struct {
					Message string `json:"message"`
				}
				json.Unmarshal(json.RawMessage(tc.ArgsJSON), &args)
				s.commitMsg = args.Message
			}

			s.messages = append(s.messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    r.result,
				ToolCallID: tc.ID,
			})
		}

	committed := false
	commitFailed := false
	for _, tr := range s.toolResults {
		if tr.ToolName == "git_commit" || tr.ToolName == "git_commit_amend" {
			if strings.Contains(tr.Result, "SUCCESS") {
				committed = true
			} else if strings.Contains(tr.Result, "FAILED") {
				commitFailed = true
			}
		}
	}

	if committed {
		return nil, nil
	}

	s.loopCount++
	if commitFailed {
		s.retryCount++
	}
	return s.StreamAI(send)
}

func (s *CommitSession) GetResult() CommitResult {
	if s.commitMsg != "" {
		return CommitResult{
			Success:          true,
			CommitMsg:        s.commitMsg,
			ToolResults:      s.toolResults,
			PromptTokens:     s.promptTokens,
			CompletionTokens: s.completionTokens,
			TotalTokens:      s.totalTokens,
		}
	}
	return CommitResult{
		Success:          false,
		ToolResults:      s.toolResults,
		Error:            fmt.Errorf("提交未完成"),
		PromptTokens:     s.promptTokens,
		CompletionTokens: s.completionTokens,
		TotalTokens:      s.totalTokens,
	}
}

func executeToolCall(name, argsJSON string) string {
	switch name {
	case "list_tree":
		var args struct {
			MaxDepth int `json:"max_depth"`
		}
		json.Unmarshal(json.RawMessage(argsJSON), &args)
		if args.MaxDepth <= 0 {
			args.MaxDepth = 3
		}
		tree := git.GetProjectTree(args.MaxDepth)
		return fmt.Sprintf("PROJECT TREE (depth=%d):\n%s", args.MaxDepth, tree)

	case "read_file":
		var args struct {
			Path      string `json:"path"`
			StartLine int    `json:"start_line"`
			EndLine   int    `json:"end_line"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		content, err := git.ReadFileContent(args.Path, args.StartLine, args.EndLine)
		if err != nil {
			return fmt.Sprintf("ERROR: %v", err)
		}
		return content

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

func (s *CommitSession) executeToolCallWithLimit(name, argsJSON string) string {
	cacheKey := name + ":" + argsJSON

	s.mu.Lock()
	if cached, ok := s.toolCache[cacheKey]; ok {
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	// For read_file: prevent re-reading the same file (wastes LLM context tokens)
	if name == "read_file" {
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(json.RawMessage(argsJSON), &args)
		if args.Path != "" {
			s.mu.Lock()
			if s.readFiles[args.Path] {
				s.mu.Unlock()
				return fmt.Sprintf("SKIPPED: %s 已在之前轮次读取，内容保留在对话历史中，请直接参考。", args.Path)
			}
			s.readFiles[args.Path] = true
			s.mu.Unlock()
		}
	}

	switch name {
	case "list_tree":
		s.mu.Lock()
		if s.listTreeCalls >= s.maxListTreeCalls {
			s.mu.Unlock()
			return fmt.Sprintf("SKIPPED: list_tree 调用已达上限（%d）", s.maxListTreeCalls)
		}
		s.listTreeCalls++
		s.mu.Unlock()
	case "read_file":
		s.mu.Lock()
		if s.readFileCalls >= s.maxReadFileCalls {
			s.mu.Unlock()
			return fmt.Sprintf("SKIPPED: read_file 调用已达上限（%d）", s.maxReadFileCalls)
		}
		s.readFileCalls++
		s.mu.Unlock()
	}

	result := executeToolCall(name, argsJSON)

	if name != "git_commit" && name != "git_commit_amend" {
		s.mu.Lock()
		s.toolCache[cacheKey] = result
		s.mu.Unlock()
	}

	return result
}

func envIntOrDefault(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
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
	b.WriteString("必须遵循 Conventional Commits 1.0.0 规范：\n")
	b.WriteString("1. 标题格式：<type>(<scope>): <subject>，scope 可选\n")
	b.WriteString("2. 允许的 type：feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert\n")
	b.WriteString("3. subject 使用中文，简洁明确，避免空泛词（如\"修改\"、\"调整\"）\n")
	b.WriteString("4. 标题尽量不超过 72 个字符\n")
	b.WriteString("5. 若是破坏性变更，使用 type(scope)!: subject 或在 footer 使用 BREAKING CHANGE:\n")
	b.WriteString("6. 只基于本次变更生成，不编造未发生的改动\n\n")

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

func buildAuthSystemPrompt(conventionInfo git.ConventionInfo, scopeHints []string) string {
	var b strings.Builder
	b.WriteString("【角色】Git commit 生成助手 + 代码审查\n\n")
	b.WriteString("【Conventional Commits 1.0.0】\n")
	b.WriteString("<type>(<scope>): <subject>\n")
	b.WriteString("- type: feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert\n")
	b.WriteString("- scope: 可选（无法判断可省略）\n")
	b.WriteString("- subject: 中文，具体，≤72 字符\n")
	b.WriteString("- 破坏性变更: type!: 或 BREAKING CHANGE:\n\n")

	if len(scopeHints) > 0 {
		b.WriteString("【推荐 scope】")
		for i, s := range scopeHints {
			if i < 3 {
				b.WriteString(s)
				if i < 2 && i < len(scopeHints)-1 {
					b.WriteString(", ")
				}
			}
		}
		b.WriteString("\n\n")
	}

	if conventionInfo.HookExists {
		b.WriteString("【Hook 约束】\n")
		b.WriteString(truncate(conventionInfo.HookContent, 300))
		b.WriteString("\n\n")
	}


	if len(conventionInfo.RecentMessages) > 0 {
		b.WriteString("【参考风格】\n")
		for i, entry := range conventionInfo.RecentMessages {
			if i < 1 {
				b.WriteString(fmt.Sprintf("%s\n", entry.Message))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("【执行规则】\n")
	b.WriteString("- 每个文件只调用一次 read_file，工具结果在对话历史中持久保留，重复读取浪费 token\n")
	b.WriteString("- 失败时调用 git_commit_amend，最多重试 3 次\n")
	b.WriteString("- 不可恢复错误时不重试，返回文本说明\n\n")
	b.WriteString("【输出格式】为避免超限截断，严格遵循：\n")
	b.WriteString("【最终提交信息】\n")
	b.WriteString("```commit\n")
	b.WriteString("<type>(<scope>): <subject>\n")
	b.WriteString("```\n")
	b.WriteString("7. 使用中文\n")
	b.WriteString("8. 【关键】为避免输出超限被截断，输出格式必须简洁：\n")
	b.WriteString("【最终提交信息】\\n")
	b.WriteString("```commit\\n")
	b.WriteString("<type>(<scope>): <subject>\\n")
	b.WriteString("```\n")

	return b.String()
}

func buildAuthPrompt(diffContent, description string) string {
	var b strings.Builder

	b.WriteString("请根据以下代码变更进行代码审查并提交。\n\n")

	if description != "" {
		b.WriteString("项目描述：\n")
		b.WriteString(description)
		b.WriteString("\n\n")
	}

	b.WriteString("代码变更：\n")
	// 主动截断过大的 diff（保留 4000 字符）
	truncatedDiff := truncate(diffContent, 4000)
	b.WriteString(truncatedDiff)
	b.WriteString("\n\n")

	b.WriteString("请先审查变更（建议先调用 list_tree 了解项目结构，再使用 read_file 读取相关文件），给出审查意见，然后必须调用 git_commit 工具提交。\n")
	b.WriteString("注意：read_file 结果保留在对话历史中，勿重复读取同一文件，浪费 token。\n")
	b.WriteString("提交成功后，在最后输出【最终提交信息】并使用 commit 代码块高亮展示，内容简洁，不要过多解释。\n")

	return b.String()
}

// 【紧凑版】当 token 接近上限时使用，减少冗余
func buildAuthSystemPromptCompact(conventionInfo git.ConventionInfo, scopeHints []string) string {
	var b strings.Builder
	b.WriteString("你是 Git commit 生成助手。分两步：\n")
	b.WriteString("1. 调用 list_tree 看项目结构，必要时 read_file 审查相关文件\n")
	b.WriteString("2. 生成 Conventional Commits 格式的消息并调用 git_commit\n\n")
	b.WriteString("格式：<type>(<scope>): <subject> | type: feat/fix/docs/style/refactor/test/build/ci/chore/revert\n")
	
	if len(scopeHints) > 0 {
		b.WriteString("推荐 scope: " + strings.Join(scopeHints[:minInt(3, len(scopeHints))], ", ") + "\n")
	}
	
	if conventionInfo.HookExists {
		b.WriteString("Hook 约束: " + truncate(conventionInfo.HookContent, 200) + "\n")
	}
	
	b.WriteString("规则：1. read_file 结果持久保留在历史中，每个文件只读一次 2. git_commit 需授权 3. 失败用 git_commit_amend 修正 4. 最后输出【最终提交信息】\n")
	
	return b.String()
}

// 【紧凑版】user prompt - 更激进的 diff 截断
func buildAuthPromptCompact(diffContent, description string) string {
	var b strings.Builder
	
	b.WriteString("代码变更：\n")
	// 更激进的截断：2000 字而不是 4000
	truncatedDiff := truncateCompact(diffContent, 2000)
	b.WriteString(truncatedDiff)
	b.WriteString("\n\n")
	
	b.WriteString("生成 commit message 并调用 git_commit。\n")
	
	return b.String()
}

// 估计 token 数（简单启发式算法）
// 1 token ≈ 4 个中文字符或 1.3 个英文单词
func estimateTokenCount(text string) int {
	// 计数中文字符和英文单词
	chineseCount := 0
	englishCount := 0
	
	for _, r := range text {
		if r >= 0x4e00 && r <= 0x9fff { // CJK 统一表意文字
			chineseCount++
		}
	}
	
	englishCount = len(text) - chineseCount
	
	// 简单估计：中文 4 个字 = 1 token，英文 4 个字 = 1 token
	return (chineseCount + englishCount) / 4
}

// truncateCompact - 比 truncate 更激进的截断
func truncateCompact(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n...(diff 已大幅截断，仅保留关键部分)"
}

// escapeJSON - 转义 JSON 字符串
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// isTruncationSignal - 检测输出是否因为超限被截断
// 特征：内容以不完整的句子结尾、缺少关键结束标记等
func isTruncationSignal(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	
	// 特征 1: 内容以 【最终提交信息】 开头但没有结尾（说明被截断了）
	if strings.Contains(content, "【最终提交信息】") && !strings.Contains(content, "```") {
		return true
	}
	
	// 特征 2: 内容以不完整的 JSON 结尾（工具调用被截断）
	if strings.HasSuffix(content, "{") || strings.HasSuffix(content, "[") || strings.HasSuffix(content, "\"") {
		return true
	}
	
	// 特征 3: 内容以 "git_commit" 开头但没有 "message" 字段（工具调用不完整）
	if strings.Contains(content, "git_commit") && !strings.Contains(content, "\"message\"") {
		return true
	}
	
	// 特征 4: 内容非常长且以省略号结尾（常见的截断标记）
	if len(content) > 1900 && strings.HasSuffix(content, "...") {
		return true
	}
	
	// 特征 5: 内容以 "commit" 或 "修" 等中文字结尾（生成被中断）
	if strings.HasSuffix(content, "commit") || strings.HasSuffix(content, "修") || 
	   strings.HasSuffix(content, "改") || strings.HasSuffix(content, "的") {
		return true
	}
	
	return false
}

// extractCommitMessageFromTruncated - 从被截断的输出中提取 commit message
// 尝试查找已生成的部分消息
func extractCommitMessageFromTruncated(content string) string {
	content = strings.TrimSpace(content)
	
	// 尝试从特定格式中提取：【最终提交信息】\n```commit\n...\n```
	if idx := strings.Index(content, "【最终提交信息】"); idx >= 0 {
		rest := content[idx+len("【最终提交信息】"):]
		rest = strings.TrimSpace(rest)
		
		// 查找 commit 代码块内的内容
		if strings.HasPrefix(rest, "```") {
			rest = strings.TrimPrefix(rest, "```commit")
			rest = strings.TrimPrefix(rest, "```")
			rest = strings.TrimSpace(rest)
			
			if idx := strings.Index(rest, "```"); idx > 0 {
				msg := rest[:idx]
				msg = strings.TrimSpace(msg)
				if msg != "" && len(msg) > 5 {
					return msg
				}
			} else if rest != "" && len(rest) > 5 {
				// 代码块还没关闭（被截断了），但至少有些内容
				lines := strings.Split(rest, "\n")
				if len(lines) > 0 && len(lines[0]) > 5 {
					return lines[0]
				}
			}
		}
	}
	
	// 尝试从最后几行中查找可能的 commit message
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "【") || strings.HasPrefix(line, "```") {
			continue
		}
		// 检查是否看起来像 commit message（通常以 type: 或 type(...): 开头）
		if (strings.Contains(line, ":") || strings.Contains(line, "：")) && 
		   len(line) > 5 && len(line) < 200 {
			return line
		}
	}
	
	return ""
}

func inferScopeHints(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	lang := detectPrimaryLanguage(paths)
	generic := map[string]bool{
		"src": true, "lib": true, "app": true, "apps": true,
		"pkg": true, "internal": true, "cmd": true,
		"services": true, "service": true, "modules": true, "module": true,
		"main": true, "java": true, "python": true,
	}

	counts := make(map[string]int)
	for _, p := range paths {
		n := strings.TrimSpace(p)
		if n == "" {
			continue
		}
		n = filepath.ToSlash(filepath.Clean(n))
		n = strings.TrimPrefix(n, "./")
		parts := strings.Split(n, "/")
		if len(parts) == 0 {
			continue
		}

		candidate := extractScopeByLanguage(parts, lang, generic)
		if candidate == "" || generic[candidate] {
			continue
		}
		counts[candidate]++
	}

	type pair struct {
		name  string
		count int
	}
	items := make([]pair, 0, len(counts))
	for k, v := range counts {
		items = append(items, pair{name: k, count: v})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].name < items[j].name
		}
		return items[i].count > items[j].count
	})
	var res []string
	for _, item := range items {
		res = append(res, item.name)
	}
	return res
}

func detectPrimaryLanguage(paths []string) string {
	counts := map[string]int{
		"go": 0, "tsjs": 0, "python": 0, "java": 0,
	}
	for _, p := range paths {
		ext := strings.ToLower(filepath.Ext(strings.TrimSpace(p)))
		switch ext {
		case ".go":
			counts["go"]++
		case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
			counts["tsjs"]++
		case ".py":
			counts["python"]++
		case ".java", ".kt", ".kts", ".groovy":
			counts["java"]++
		}
	}

	best := ""
	bestCount := 0
	for k, v := range counts {
		if v > bestCount {
			best = k
			bestCount = v
		}
	}
	if bestCount == 0 {
		return "generic"
	}
	return best
}

func extractScopeByLanguage(parts []string, lang string, generic map[string]bool) string {
	if len(parts) == 0 {
		return ""
	}

	switch lang {
	case "go":
		if len(parts) >= 2 && (parts[0] == "cmd" || parts[0] == "internal" || parts[0] == "pkg" || parts[0] == "api") {
			return normalizeScope(parts[1])
		}
	case "tsjs":
		if len(parts) >= 3 && parts[0] == "src" {
			if parts[1] == "features" || parts[1] == "modules" || parts[1] == "domains" {
				return normalizeScope(parts[2])
			}
			return normalizeScope(parts[1])
		}
		if len(parts) >= 2 && (parts[0] == "apps" || parts[0] == "packages" || parts[0] == "services") {
			return normalizeScope(parts[1])
		}
	case "python":
		if len(parts) >= 2 && (parts[0] == "src" || parts[0] == "app") {
			return normalizeScope(parts[1])
		}
		if len(parts) >= 1 && (parts[0] == "tests" || parts[0] == "test") {
			return "test"
		}
	case "java":
		if len(parts) >= 5 && parts[0] == "src" && (parts[1] == "main" || parts[1] == "test") && parts[2] == "java" {
			for i := 3; i < len(parts); i++ {
				p := normalizeScope(parts[i])
				if p == "" || p == "com" || p == "org" || p == "net" || p == "io" {
					continue
				}
				return p
			}
		}
	}

	idx := 0
	if len(parts) > 1 && generic[parts[0]] {
		idx = 1
		if len(parts) > 2 && generic[parts[1]] {
			idx = 2
		}
	}
	return normalizeScope(parts[idx])
}

func normalizeScope(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	if strings.Contains(s, ".") {
		s = strings.TrimSuffix(s, filepath.Ext(s))
	}
	s = strings.ReplaceAll(s, "_", "-")
	return s
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
