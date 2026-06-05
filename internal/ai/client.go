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
	"github.com/oliver/git-ai-commit/internal/logger"
	"github.com/oliver/git-ai-commit/internal/memory"
	"github.com/oliver/git-ai-commit/internal/skill"
	openai "github.com/sashabaranov/go-openai"
)

type Client struct {
	client *openai.Client
	config Config
}

type Config struct {
	APIKey        string
	Model         string
	BaseURL       string
	Timeout       time.Duration
	ContextWindow int
}

const (
	PhaseUnderstand = 1
	PhaseCommit     = 2
)

func NewClient(config Config) (*Client, error) {
	if config.APIKey == "" {
		logger.Error("API Key 未配置")
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

	logger.Info("AI client initialized model=%s baseURL=%s timeout=%s", config.Model, config.BaseURL, config.Timeout)
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
		logger.Error("调用 AI API 失败: %v", err)
		return "", fmt.Errorf("调用 AI API 失败：%w", err)
	}

	if len(resp.Choices) == 0 {
		logger.Error("API 返回空响应")
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
		logger.Error("调用 AI API 失败: %v", err)
		return "", fmt.Errorf("调用 AI API 失败：%w", err)
	}

	if len(resp.Choices) == 0 {
		logger.Error("API 返回空响应")
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
	client               *Client
	messages             []openai.ChatCompletionMessage
	tools                []openai.Tool
	retryCount           int
	maxRetries           int
	loopCount            int
	maxLoops             int
	toolResults          []ToolCallResult
	commitMsg            string
	streaming            bool
	promptTokens         int
	completionTokens     int
	totalTokens          int
	readFileCalls        int
	listTreeCalls        int
	updateMemoryCalls    int
	maxReadFileCalls     int
	maxListTreeCalls     int
	maxUpdateMemoryCalls int
	compactMode          bool
	noToolCallFallback   bool
	toolCache            map[string]string
	readFiles            map[string]bool
	mu                   sync.Mutex
	// 两阶段架构
	phase              int
	understanding      string
	phase2SystemPrompt string
	// 审查结果
	ReviewResult *ReviewResult
	// Skill 系统
	skillManager *skill.Manager
	// 用户输入（用于 ask_user 工具）
	askUserAnswer string
}

type ReviewResult struct {
	Summary         string
	HasRisk         bool
	IsSimple        bool
	Recommendation  string
	Highlights      []string
	BreakingChanges bool
	Risks           []ReviewRisk
}

type ReviewRisk struct {
	Severity    string
	Category    string
	File        string
	Line        int
	Description string
	Suggestion  string
}

func (s *CommitSession) SetAskUserAnswer(answer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.askUserAnswer = answer
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

// StartCommitSession 启动两阶段提交会话。
// Phase 1（理解阶段）：AI 阅读代码变更，输出结构化理解，调用 summarize_changes 进入 Phase 2。
// Phase 2（审查提交阶段）：AI 基于已有理解进行审查并提交。
func (c *Client) StartCommitSession(diffContent, description string, conventionInfo git.ConventionInfo, maxRetries int, selectedFiles []string, skillMgr *skill.Manager) (*CommitSession, error) {
	scopeHints := inferScopeHints(selectedFiles)

	memoryContent, _ := memory.Read()

	systemPrompt := buildUnderstandSystemPrompt(conventionInfo, memoryContent)
	userPrompt := buildUnderstandUserPrompt(diffContent, description)

	estimatedTokens := estimateTokenCount(systemPrompt + userPrompt)
	compactMode := estimatedTokens > 6000

	if compactMode {
		systemPrompt = buildUnderstandSystemPromptCompact(conventionInfo, memoryContent)
		userPrompt = buildUnderstandUserPromptCompact(diffContent)
	}

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: userPrompt},
	}

	phase2Prompt := buildAuthSystemPrompt(conventionInfo, scopeHints, memoryContent, skillMgr)
	if compactMode {
		phase2Prompt = buildAuthSystemPromptCompact(conventionInfo, scopeHints, memoryContent, skillMgr)
	}

	tools := buildOpenAITools()
	if skillMgr != nil {
		tools = append(tools, convertSkillTools(skillMgr.AllTools())...)
	}

	sess := &CommitSession{
		client:               c,
		messages:             messages,
		tools:                tools,
		maxRetries:           maxRetries,
		maxLoops:             100,
		maxReadFileCalls:     dynReadFileLimit(len(selectedFiles)),
		maxListTreeCalls:     envIntOrDefault("GIT_AI_MAX_LIST_TREE_CALLS", 1),
		maxUpdateMemoryCalls: 1,
		compactMode:          compactMode,
		toolCache:            make(map[string]string),
		readFiles:            make(map[string]bool),
		phase:                PhaseUnderstand,
		phase2SystemPrompt:   phase2Prompt,
		skillManager:         skillMgr,
	}

	logger.Info("AI session started model=%s compactMode=%v selectedFiles=%d estimatedTokens=%d memoryLen=%d skills=%d", c.config.Model, compactMode, len(selectedFiles), estimatedTokens, len(memoryContent), len(skillMgr.SkillNames()))
	return sess, nil
}

// ContinueCommitSession 从持久化会话继续，直接进入 Phase 2（审查提交阶段）。
func (c *Client) ContinueCommitSession(ps *PersistableSession, diffContent string, conventionInfo git.ConventionInfo, selectedFiles []string, skillMgr *skill.Manager) (*CommitSession, error) {
	messages := make([]openai.ChatCompletionMessage, len(ps.Messages))
	copy(messages, ps.Messages)

	continuePrompt := BuildContinuePrompt(diffContent)
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: continuePrompt,
	})

	memoryContent, _ := memory.Read()

	scopeHints := inferScopeHints(selectedFiles)
	systemPrompt := buildAuthSystemPrompt(conventionInfo, scopeHints, memoryContent, skillMgr)
	if ps.CompactMode {
		systemPrompt = buildAuthSystemPromptCompact(conventionInfo, scopeHints, memoryContent, skillMgr)
	}
	if len(messages) > 0 && messages[0].Role == openai.ChatMessageRoleSystem {
		messages[0].Content = systemPrompt
	} else {
		messages = append([]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleSystem, Content: systemPrompt}}, messages...)
	}

	tools := buildOpenAITools()
	if skillMgr != nil {
		tools = append(tools, convertSkillTools(skillMgr.AllTools())...)
	}

	sess := &CommitSession{
		client:               c,
		messages:             messages,
		tools:                tools,
		maxRetries:           3,
		maxLoops:             100,
		maxReadFileCalls:     dynReadFileLimit(len(selectedFiles)),
		maxListTreeCalls:     envIntOrDefault("GIT_AI_MAX_LIST_TREE_CALLS", 1),
		maxUpdateMemoryCalls: 1,
		compactMode:          ps.CompactMode,
		toolCache:            make(map[string]string),
		readFiles:            make(map[string]bool),
		phase:                PhaseCommit,
		skillManager:         skillMgr,
	}

	return sess, nil
}

func (s *CommitSession) StreamAI(send func(chunk StreamChunk)) ([]PendingToolCall, error) {
	s.streaming = true
	maxTokens := envIntOrDefault("GIT_AI_MAX_COMPLETION_TOKENS", 0)
	req := openai.ChatCompletionRequest{
		Model:       s.client.config.Model,
		Messages:    s.messages,
		Tools:       s.tools,
		Temperature: 0.3,
		Stream:      true,
	}
	if maxTokens > 0 {
		req.MaxCompletionTokens = maxTokens
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.client.config.Timeout)
	defer cancel()

	stream, err := s.client.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		logger.Error("调用 AI API 失败: %v", err)
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

	// 如果流式响应未返回 token 用量（部分 API 提供商不支持），用本地估算兜底
	if s.totalTokens == 0 {
		s.fillFallbackTokenEstimate(fullContent.String())
	}

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
			// 截断时移除无效消息，启用紧凑模式重试，再失败则提取提交
			s.messages = s.messages[:len(s.messages)-1]
			if !s.compactMode {
				s.compactMode = true
				return s.StreamAI(send)
			}
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

	// 检查此轮是否包含 summarize_changes（用于 Phase 1 → Phase 2 转换）
	hasSummarize := false
	for _, tc := range pending {
		if tc.Name == "summarize_changes" {
			hasSummarize = true
			break
		}
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

		if tc.Name == "report_review" && s.ReviewResult == nil {
			s.ReviewResult = parseReviewResult(tc.ArgsJSON)
		}

		s.messages = append(s.messages, openai.ChatCompletionMessage{
			Role:       openai.ChatMessageRoleTool,
			Content:    r.result,
			ToolCallID: tc.ID,
		})
	}

	// Phase transition: summarize_changes 触发 Phase 1 → Phase 2
	if s.phase == PhaseUnderstand && hasSummarize {
		for _, tr := range s.toolResults {
			if tr.ToolName == "summarize_changes" {
				if idx := strings.Index(tr.Result, "UNDERSTANDING_RECORDED: "); idx >= 0 {
					s.understanding = tr.Result[idx+len("UNDERSTANDING_RECORDED: "):]
				}
				break
			}
		}
		s.transitionToPhase2()
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

	// 【二次验证】即使 AI 报告 FAILED，git 可能因 hook 警告等非零退出但提交已成功
	// 用独立 git 命令确认，避免误判
	if commitFailed {
		vResult := git.VerifyCommit()
		if vResult.Error == "" && vResult.Hash != "" {
			// 提交实际已成功，更新 tool result 以便后续流程正确处理
			for i, tr := range s.toolResults {
				if tr.ToolName == "git_commit" || tr.ToolName == "git_commit_amend" {
					if !strings.Contains(tr.Result, "SUCCESS") {
						s.toolResults[i].Result = fmt.Sprintf("SUCCESS: 提交成功 %s", vResult.Hash)
					}
				}
			}
			return nil, nil
		}
	}

	s.loopCount++
	if commitFailed {
		s.retryCount++
	}
	return s.StreamAI(send)
}

// transitionToPhase2 从理解阶段切换到审查提交阶段
func (s *CommitSession) transitionToPhase2() {
	s.phase = PhaseCommit
	s.loopCount = 0 // Phase 2 有独立的轮次预算
	for i, msg := range s.messages {
		if msg.Role == openai.ChatMessageRoleSystem {
			s.messages[i].Content = s.phase2SystemPrompt
			return
		}
	}
}

func (s *CommitSession) GetResult() CommitResult {
	// 检查 tool results 中是否有成功的提交
	committed := false
	for _, tr := range s.toolResults {
		if tr.ToolName == "git_commit" || tr.ToolName == "git_commit_amend" {
			if strings.Contains(tr.Result, "SUCCESS") {
				committed = true
				break
			}
		}
	}

	if committed && s.commitMsg != "" {
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

// fillFallbackTokenEstimate 在 API 未返回 token 用量时，用本地估算作为兜底
func (s *CommitSession) fillFallbackTokenEstimate(outputContent string) {
	var inputText strings.Builder
	for _, msg := range s.messages {
		inputText.WriteString(msg.Content)
		for _, tc := range msg.ToolCalls {
			inputText.WriteString(tc.Function.Name)
			inputText.WriteString(tc.Function.Arguments)
		}
	}
	s.promptTokens = estimateTokenCount(inputText.String())
	s.completionTokens = estimateTokenCount(outputContent)
	s.totalTokens = s.promptTokens + s.completionTokens
}

func executeToolCall(name, argsJSON string) string {
	logger.Debug("executeToolCall name=%s args=%s", name, truncate(argsJSON, 200))
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

	case "read_diff":
		var args struct {
			Path         string `json:"path"`
			ContextLines int    `json:"context_lines"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		return git.GetFileDiff(args.Path, args.ContextLines)

	case "git_commit":
		var args struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			logger.Error("git_commit 参数解析失败: %v", err)
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		result := git.Commit(args.Message)
		if result.Success {
			logger.Info("git_commit 成功 hash=%s", result.Hash)
			return fmt.Sprintf("SUCCESS: 提交成功 %s", result.Hash)
		}
		logger.Error("git_commit 失败 stderr=%s", result.Stderr)
		classified := git.ClassifyCommitError(result.Stderr)
		return fmt.Sprintf("FAILED: %s\n分类：%s\n建议：%s\n原始错误：%s",
			classified.Message, categoryLabel(classified.Category), classified.Suggestion, classified.RawStderr)

	case "git_commit_amend":
		var args struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			logger.Error("git_commit_amend 参数解析失败: %v", err)
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		result := git.CommitAmend(args.Message)
		if result.Success {
			logger.Info("git_commit_amend 成功 hash=%s", result.Hash)
			return fmt.Sprintf("SUCCESS: amend 成功 %s", result.Hash)
		}
		logger.Error("git_commit_amend 失败 stderr=%s", result.Stderr)
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
		if info.HookExists || info.PreCommitHookExists || info.PrepareCommitHookExists {
			return info.AllConventionTools
		}
		return "NOT_FOUND: 仓库没有检测到 git hook 约束"

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

	case "diff_overview":
		return git.GetDiffOverview()

	case "search_references":
		var args struct {
			Symbol     string `json:"symbol"`
			PathFilter string `json:"path_filter"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		return git.SearchReferences(args.Symbol, args.PathFilter, 30)

	case "summarize_changes":
		var args struct {
			Understanding string `json:"understanding"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		return fmt.Sprintf("UNDERSTANDING_RECORDED: %s", args.Understanding)

	case "report_review":
		var args struct {
			Summary         string                   `json:"summary"`
			HasRisk         bool                     `json:"has_risk"`
			IsSimple        bool                     `json:"is_simple"`
			Recommendation  string                   `json:"recommendation"`
			Highlights      []string                 `json:"highlights"`
			BreakingChanges bool                     `json:"breaking_changes"`
			Risks           []map[string]interface{} `json:"risks"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		result := fmt.Sprintf("REVIEW_RESULT:\n摘要: %s\n建议: %s\n风险: ", args.Summary, args.Recommendation)
		if args.IsSimple {
			result = "REVIEW_RESULT:\n摘要: " + args.Summary + "\n建议: approve (简单变更)\n风险: 无 (简单变更跳过详细审查)"
		} else if args.HasRisk && len(args.Risks) > 0 {
			for i, r := range args.Risks {
				sev, _ := r["severity"].(string)
				cat, _ := r["category"].(string)
				desc, _ := r["description"].(string)
				file, _ := r["file"].(string)
				sug, _ := r["suggestion"].(string)
				result += fmt.Sprintf("\n  [%d] [%s/%s]", i+1, sev, cat)
				if file != "" {
					result += fmt.Sprintf(" %s", file)
				}
				result += fmt.Sprintf(": %s", desc)
				if sug != "" {
					result += fmt.Sprintf(" (建议: %s)", sug)
				}
			}
		} else {
			result += "无风险"
		}
		return result

	case "update_memory":
		var args struct {
			Content string `json:"content"`
			Action  string `json:"action"`
		}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		if args.Content == "" {
			return "ERROR: content 不能为空"
		}
		if args.Action != "append" && args.Action != "replace" {
			return fmt.Sprintf("ERROR: action 必须是 append 或 replace，当前值: %s", args.Action)
		}

		var finalContent string
		if args.Action == "append" {
			existing, err := memory.Read()
			if err != nil {
				logger.Warn("读取现有记忆失败: %v", err)
				finalContent = args.Content
			} else if existing != "" {
				finalContent = existing + "\n\n---\n\n" + args.Content
			} else {
				finalContent = args.Content
			}
		} else {
			finalContent = args.Content
		}

		if err := memory.Write(finalContent); err != nil {
			logger.Error("写入记忆失败: %v", err)
			return fmt.Sprintf("ERROR: 写入记忆失败：%v", err)
		}
		logger.Info("记忆已更新 action=%s length=%d", args.Action, len(finalContent))
		return fmt.Sprintf("MEMORY_UPDATED: 项目记忆已%s，当前长度 %d 字符", args.Action, len(finalContent))

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
	case "update_memory":
		s.mu.Lock()
		if s.updateMemoryCalls >= s.maxUpdateMemoryCalls {
			s.mu.Unlock()
			return fmt.Sprintf("SKIPPED: update_memory 调用已达上限（%d），每次会话最多更新 1 次", s.maxUpdateMemoryCalls)
		}
		s.updateMemoryCalls++
		s.mu.Unlock()
	case "ask_user":
		s.mu.Lock()
		answer := s.askUserAnswer
		s.askUserAnswer = ""
		s.mu.Unlock()
		if answer != "" {
			return fmt.Sprintf("USER_ANSWER: %s", answer)
		}
		return "ERROR: 用户未提供回答"
	}

	// Check if it's a skill tool
	if s.skillManager != nil && s.skillManager.HasTool(name) {
		var args map[string]interface{}
		if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
			return fmt.Sprintf("ERROR: 解析参数失败：%v", err)
		}
		result, err := s.skillManager.CallTool(context.Background(), name, args)
		if err != nil {
			logger.Error("skill tool %s 调用失败: %v", name, err)
			return fmt.Sprintf("ERROR: %v", err)
		}
		s.mu.Lock()
		s.toolCache[cacheKey] = result
		s.mu.Unlock()
		return result
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

// dynReadFileLimit 根据变更文件数量动态计算 read_file 调用上限。
// 小变更集保持 4 次，大变更集逐步放宽到 16 次。
// 用户可通过 GIT_AI_MAX_READ_FILE_CALLS 环境变量覆盖。
func dynReadFileLimit(fileCount int) int {
	envVal := os.Getenv("GIT_AI_MAX_READ_FILE_CALLS")
	if envVal != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(envVal)); err == nil && n > 0 {
			return n
		}
	}
	switch {
	case fileCount <= 3:
		return 4
	case fileCount <= 10:
		return 8
	case fileCount <= 25:
		return 12
	default:
		return 16
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

func parseReviewResult(argsJSON string) *ReviewResult {
	var args struct {
		Summary         string                   `json:"summary"`
		HasRisk         bool                     `json:"has_risk"`
		IsSimple        bool                     `json:"is_simple"`
		Recommendation  string                   `json:"recommendation"`
		Highlights      []string                 `json:"highlights"`
		BreakingChanges bool                     `json:"breaking_changes"`
		Risks           []map[string]interface{} `json:"risks"`
	}
	if err := json.Unmarshal(json.RawMessage(argsJSON), &args); err != nil {
		return nil
	}
	rr := &ReviewResult{
		Summary:         args.Summary,
		HasRisk:         args.HasRisk,
		IsSimple:        args.IsSimple,
		Recommendation:  args.Recommendation,
		Highlights:      args.Highlights,
		BreakingChanges: args.BreakingChanges,
	}
	for _, r := range args.Risks {
		risk := ReviewRisk{}
		if s, ok := r["severity"].(string); ok {
			risk.Severity = s
		}
		if s, ok := r["category"].(string); ok {
			risk.Category = s
		}
		if s, ok := r["file"].(string); ok {
			risk.File = s
		}
		if n, ok := r["line"].(float64); ok {
			risk.Line = int(n)
		}
		if s, ok := r["description"].(string); ok {
			risk.Description = s
		}
		if s, ok := r["suggestion"].(string); ok {
			risk.Suggestion = s
		}
		rr.Risks = append(rr.Risks, risk)
	}
	return rr
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

func convertSkillTools(skillTools []skill.ToolDefinition) []openai.Tool {
	var tools []openai.Tool
	for _, td := range skillTools {
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.InputSchema,
			},
		})
	}
	return tools
}

func buildGenerateSystemPrompt(conventionInfo git.ConventionInfo) string {
	var b strings.Builder
	b.WriteString("你是 Git commit 生成助手。根据代码变更生成 Conventional Commits 消息，直接返回文本，不调用工具。\n\n")
	b.WriteString("格式: <type>(<scope>): <subject>\n")
	b.WriteString("type: feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert\n")
	b.WriteString("scope: 可选\n")
	b.WriteString("subject: 中文，具体，≤72字符\n")
	b.WriteString("破坏性变更: type!: 或 BREAKING CHANGE:\n")
	b.WriteString("只基于本次变更生成，不编造未发生的改动\n\n")

	if conventionInfo.AllConventionTools != "" {
		b.WriteString("项目提交规范:\n")
		b.WriteString(truncate(conventionInfo.AllConventionTools, 800))
		b.WriteString("\n\n")
	}

	if conventionInfo.TemplateExists {
		b.WriteString("Commit 模板:\n")
		b.WriteString(conventionInfo.TemplateContent)
		b.WriteString("\n\n")
	}

	if len(conventionInfo.RecentMessages) > 0 {
		b.WriteString("参考风格:\n")
		for _, entry := range conventionInfo.RecentMessages {
			b.WriteString(fmt.Sprintf("- %s\n", entry.Message))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func buildGeneratePrompt(diffContent, description string) string {
	var b strings.Builder

	b.WriteString("根据以下变更生成 commit message。\n\n")

	if description != "" {
		b.WriteString("项目描述：\n")
		b.WriteString(description)
		b.WriteString("\n\n")
	}

	b.WriteString("代码变更：\n")
	b.WriteString(diffContent)

	return b.String()
}

// buildAuthSystemPrompt 构建 Phase 2（审查提交阶段）系统提示词
func buildAuthSystemPrompt(conventionInfo git.ConventionInfo, scopeHints []string, memoryContent string, skillMgr *skill.Manager) string {
	var b strings.Builder
	b.WriteString("你是资深代码审查助手。工作流程：审查风险 → report_review → 提交。\n\n")
	b.WriteString("你已在理解阶段阅读了代码，当前对话中已包含对变更的结构化理解。\n")
	b.WriteString("如需补充阅读特定代码，仍可使用 read_file。\n\n")

	if memoryContent != "" {
		b.WriteString("【项目记忆】（历史积累的项目知识，帮助你更好地审查代码）\n")
		b.WriteString(truncate(memoryContent, 800))
		b.WriteString("\n\n")
	}

	b.WriteString("【核心原则】\n")
	b.WriteString("1. 读代码，再做判断：严禁仅凭文件名或 diff 摘要推断风险。必须 read_file 读取具体代码后再分析。\n")
	b.WriteString("2. 先理解再判断：读代码后先总结「变更结构摘要」（改了哪些文件、函数、类型），再基于这个理解做审查。\n")
	b.WriteString("3. 审查与提交分离：审查意见是给用户的辅助信息，commit message 必须基于代码变更本身生成。\n")
	b.WriteString("4. 节约 token：每次工具调用结果都会累积在对话中。只读必要的行，不读整个文件。\n\n")

	b.WriteString("【审查维度】（8 个维度，根据变更类型选择性检查）\n")
	b.WriteString("1. 正确性 (correctness): 逻辑缺陷、边界条件、竞态条件、状态转换\n")
	b.WriteString("2. 安全性 (security): 注入、XSS、权限泄露、敏感信息暴露\n")
	b.WriteString("3. 性能 (performance): 不必要的循环、重复查询、内存泄漏\n")
	b.WriteString("4. 错误处理 (error_handling): 异常未捕获、降级缺失、错误信息不明确\n")
	b.WriteString("5. 设计 (design): 架构合理性、职责划分、接口设计\n")
	b.WriteString("6. 测试 (testing): 测试覆盖、边界用例、回归风险\n")
	b.WriteString("7. 可维护性 (maintainability): 魔法数字、过度耦合、命名清晰度\n")
	b.WriteString("8. 一致性 (consistency): 代码风格、命名规范、项目约定\n")
	b.WriteString("审查结果须引用具体代码行说明判断依据。\n\n")

	b.WriteString("【简单变更快速通道】\n")
	b.WriteString("如果变更极为简单（如纯注释、单行修复、配置微调），可设置 is_simple=true 跳过详细审查。\n")
	b.WriteString("判断标准：变更不涉及逻辑、不影响运行时行为、不改变接口。\n\n")

	b.WriteString("【执行顺序】\n")
	b.WriteString("1. 基于已有理解分析风险\n")
	b.WriteString("2. 如有需要 read_file 补充阅读关键代码\n")
	b.WriteString("3. 调用 report_review 输出审查意见（必须调用，在 git_commit 之前）\n")
	b.WriteString("4. 调用 git_commit 提交（commit message 基于变更结构摘要，而非审查结论）\n")
	b.WriteString("5. 提交成功后输出 【最终提交信息】\n\n")

	b.WriteString("【Commit 格式】\n")
	b.WriteString("<type>(<scope>): <subject>\n")
	b.WriteString("type: feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert\n")
	b.WriteString("scope: 可选\n")
	b.WriteString("subject: 中文，具体，≤50字符\n")
	b.WriteString("破坏性变更: type!: 或 BREAKING CHANGE:\n")

	if len(scopeHints) > 0 {
		b.WriteString("推荐 scope: ")
		for i, s := range scopeHints {
			if i < 3 {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(s)
			}
		}
		b.WriteString("\n")
	}

	if conventionInfo.AllConventionTools != "" {
		b.WriteString("项目提交规范:\n")
		b.WriteString(truncate(conventionInfo.AllConventionTools, 500))
		b.WriteString("\n")
	}

	if len(conventionInfo.RecentMessages) > 0 {
		b.WriteString("参考风格: ")
		b.WriteString(conventionInfo.RecentMessages[0].Message)
		b.WriteString("\n")
	}

	if skillMgr != nil && skillMgr.HasTool("codegraph_search") {
		b.WriteString("\n【代码图分析工具】\n")
		b.WriteString("可使用 codegraph_* 工具深入分析代码结构：\n")
		b.WriteString("- codegraph_search: 按名称搜索符号（函数、类、变量）\n")
		b.WriteString("- codegraph_explore: 深度探索代码结构和调用关系（推荐首先使用）\n")
		b.WriteString("- codegraph_callers: 查找调用者（谁调用了这个函数）\n")
		b.WriteString("- codegraph_callees: 查找被调用者（这个函数调用了谁）\n")
		b.WriteString("- codegraph_impact: 分析变更影响范围\n")
		b.WriteString("- codegraph_node: 获取符号详细信息和上下文\n")
		b.WriteString("适用场景：理解复杂调用链、评估重构影响、搜索代码结构\n")
	}

	b.WriteString("\n【用户交互工具】\n")
	b.WriteString("当遇到不确定的决策时，使用 ask_user 工具向用户提问：\n")
	b.WriteString("- 多种实现方案需要用户选择\n")
	b.WriteString("- 发现潜在问题但不确定用户意图\n")
	b.WriteString("- 需要用户确认某个设计决策\n")
	b.WriteString("- 遇到模糊需求需要澄清\n")
	b.WriteString("工具会弹出交互界面让用户选择或输入，等待用户回答后继续执行。\n")

	b.WriteString("\n【规则】\n")
	b.WriteString("- diff 内容可能被截断：如果 seen 的 patch 不全，用 read_file 补全关键代码\n")
	b.WriteString("- 先读代码后判断，不可凭文件名猜测风险\n")
	b.WriteString("- 审查意见与 commit message 独立，互不影响\n")
	b.WriteString("- commit message 描述「代码做了什么」（新增/修改/删除功能），而非「代码是否安全」\n")
	b.WriteString("- 提交前自检：commit message 是否准确反映了代码变更？如果发现是审查结论的改写，重新生成\n")
	b.WriteString("- 避免在对话中混入无关的长文本，控制每轮输出长度\n")
	b.WriteString("- tool 结果中已有内容的文件，直接引用，不要重复 read_file\n")
	b.WriteString("- list_tree 默认 depth=1\n")
	b.WriteString("- report_review 必须在 git_commit 之前调用\n")
	b.WriteString("- git_commit 是最终目标，失败用 amend 修正，最多 3 次\n")
	b.WriteString("- 不可恢复错误时不重试\n")
	b.WriteString("- 如果发现项目的重要架构模式、团队约定或易错点，可调用 update_memory 记录（每次会话最多 1 次）\n")
	b.WriteString("- 提交后输出：\n")
	b.WriteString("【最终提交信息】\n")
	b.WriteString("```commit\n")
	b.WriteString("<type>(<scope>): <subject>\n")
	b.WriteString("```\n")

	return b.String()
}

func buildAuthSystemPromptCompact(conventionInfo git.ConventionInfo, scopeHints []string, memoryContent string, skillMgr *skill.Manager) string {
	var b strings.Builder
	b.WriteString("你是代码审查助手。工作流程：审查风险 → report_review → 提交。\n")
	b.WriteString("已在理解阶段阅读了代码，直接进入审查和提交。\n")

	if memoryContent != "" {
		b.WriteString("项目记忆: " + truncate(memoryContent, 400) + "\n")
	}

	b.WriteString("审查维度：正确性、安全性、性能、错误处理、设计、测试、可维护性、一致性\n")
	b.WriteString("简单变更（注释/单行修复/配置微调）可设 is_simple=true 跳过详细审查。\n")
	b.WriteString("先读代码再判断，commit message 描述变更本身不是审查结论。\n")
	b.WriteString("diff 可能被截断，用 read_file 补全。控制输出长度节约 token。\n\n")
	b.WriteString("步骤: 分析风险 → read_file(需要时) → report_review(必须) → git_commit\n")
	b.WriteString("Commit 格式: <type>(<scope>): <subject>\n")
	b.WriteString("type: feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert\n")

	if len(scopeHints) > 0 {
		b.WriteString("推荐 scope: " + strings.Join(scopeHints[:minInt(3, len(scopeHints))], ", ") + "\n")
	}

	if conventionInfo.AllConventionTools != "" {
		b.WriteString("规范:\n" + truncate(conventionInfo.AllConventionTools, 300) + "\n")
	}

	if skillMgr != nil && skillMgr.HasTool("codegraph_search") {
		b.WriteString("代码图工具: codegraph_search/explore/callers/callees/impact/node 可用于深入分析代码结构和调用关系\n")
	}

	b.WriteString("用户交互: 遇到不确定决策时用 ask_user 向用户提问\n")
	b.WriteString("规则: 读代码再判断; report_review 在 git_commit 前; commit 描述变更本身; git_commit 是目标; 失败用 amend; 发现重要模式可 update_memory(最多1次)\n")

	return b.String()
}

// ---- Phase 1 提示词 ----

// buildUnderstandSystemPrompt 构建 Phase 1（理解阶段）系统提示词
func buildUnderstandSystemPrompt(conventionInfo git.ConventionInfo, memoryContent string) string {
	var b strings.Builder
	b.WriteString("你是资深代码审查助手。当前阶段：【理解变更】。\n\n")
	b.WriteString("你的任务是阅读并理解代码变更，然后调用 summarize_changes 提交结构化理解。\n\n")

	if memoryContent != "" {
		b.WriteString("【项目记忆】（历史积累的项目知识，帮助你更好地理解上下文）\n")
		b.WriteString(truncate(memoryContent, 800))
		b.WriteString("\n\n")
	}

	b.WriteString("【大变更集处理策略】\n")
	b.WriteString("- diff 开头可能包含「变更文件分组（按目录）」索引，按目录列出所有变更文件及数量\n")
	b.WriteString("- 先看分组索引了解全貌，再决定读哪些文件\n")
	b.WriteString("- 优先读取 core 类型文件（核心业务逻辑），跳过 test/config/generated 文件\n")
	b.WriteString("- 每个文件用 start_line/end_line 限定行范围，不要读整个文件\n")
	b.WriteString("- 同一目录下的文件通常相关，可以一起理解\n\n")

	b.WriteString("【执行步骤】\n")
	b.WriteString("1. diff_overview → 了解变更概览（无需授权，自动执行）\n")
	b.WriteString("2. read_file → 读取关键变更代码（指定行范围，勿读整个文件）\n")
	b.WriteString("3. 输出你对变更的完整理解：改了哪些文件/模块、修改目的、涉及的核心函数或类型\n")
	b.WriteString("4. 调用 summarize_changes 工具提交理解，进入审查阶段\n\n")

	b.WriteString("【输出要求】\n")
	b.WriteString("- 读代码后再总结，不要凭 diff 摘要猜测\n")
	b.WriteString("- 控制输出长度，用 2-4 行概括变更结构\n")
	b.WriteString("- diff 可能被截断，用 read_file 补全关键代码\n\n")

	b.WriteString("【限制】\n")
	b.WriteString("- 不要调用 git_commit、git_commit_amend 或 report_review\n")
	b.WriteString("- 不要进行审查或风险分析，理解阶段只关注「代码做了什么」\n")
	b.WriteString("- 理解完成后，务必调用 summarize_changes 进入下一阶段\n")

	if conventionInfo.AllConventionTools != "" {
		b.WriteString("\n项目提交规范（仅供参考，理解阶段不需要关注格式）:\n")
		b.WriteString(truncate(conventionInfo.AllConventionTools, 300))
		b.WriteString("\n")
	}

	return b.String()
}

// buildUnderstandUserPrompt 构建 Phase 1 用户提示词
func buildUnderstandUserPrompt(diffContent, description string) string {
	var b strings.Builder

	b.WriteString("请阅读并理解以下代码变更，输出结构化理解后调用 summarize_changes。\n")
	b.WriteString("注意：diff 内容可能被截断，请先用 diff_overview 了解全貌，再用 read_file 补全关键代码。\n\n")

	if description != "" {
		b.WriteString("项目描述：\n")
		b.WriteString(description)
		b.WriteString("\n\n")
	}

	b.WriteString("代码变更：\n")
	truncatedDiff := truncate(diffContent, 4000)
	b.WriteString(truncatedDiff)
	b.WriteString("\n\n")

	b.WriteString("步骤：\n")
	b.WriteString("1. diff_overview → 了解变更概览\n")
	b.WriteString("2. read_file → 读取关键变更代码（如有需要）\n")
	b.WriteString("3. 输出变更理解（改了哪些文件/函数/类型）\n")
	b.WriteString("4. 调用 summarize_changes 提交理解\n")

	return b.String()
}

// buildUnderstandSystemPromptCompact Phase 1 紧凑版系统提示词
func buildUnderstandSystemPromptCompact(conventionInfo git.ConventionInfo, memoryContent string) string {
	var b strings.Builder
	b.WriteString("当前阶段：【理解变更】。阅读代码理解变更后调用 summarize_changes。\n\n")

	if memoryContent != "" {
		b.WriteString("项目记忆: " + truncate(memoryContent, 400) + "\n\n")
	}

	b.WriteString("大变更集: 先看开头的文件分组索引了解全貌, 优先读 core 文件, 跳过 test/config。\n")
	b.WriteString("步骤: diff_overview → read_file → 输出理解 → summarize_changes\n")
	b.WriteString("不要审查风险，不要提交。理解够了就调用 summarize_changes。\n")
	b.WriteString("控制输出长度，diff 可能被截断用 read_file 补全。\n")

	if conventionInfo.AllConventionTools != "" {
		b.WriteString("规范: " + truncate(conventionInfo.AllConventionTools, 200) + "\n")
	}

	return b.String()
}

// buildUnderstandUserPromptCompact Phase 1 紧凑版用户提示词
func buildUnderstandUserPromptCompact(diffContent string) string {
	var b strings.Builder

	b.WriteString("代码变更：\n")
	truncatedDiff := truncateCompact(diffContent, 2000)
	b.WriteString(truncatedDiff)
	b.WriteString("\n\n")

	b.WriteString("步骤: diff_overview → read_file → 输出理解 → summarize_changes\n")

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
		logger.Error("GenerateDescription API 调用失败: %v", err)
		return "", fmt.Errorf("调用 AI API 失败：%w", err)
	}

	if len(resp.Choices) == 0 {
		logger.Error("GenerateDescription API 返回空响应")
		return "", fmt.Errorf("API 返回空响应")
	}

	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func (c *Client) GenerateMemory(commitMsg, reviewSummary, existingMemory, diffContent string) (string, error) {
	var prompt strings.Builder
	prompt.WriteString("请根据以下信息，生成或更新项目记忆（200-500 字）。\n\n")

	if existingMemory != "" {
		prompt.WriteString("现有记忆：\n")
		prompt.WriteString(existingMemory)
		prompt.WriteString("\n\n")
	}

	prompt.WriteString("本次提交：\n")
	prompt.WriteString(commitMsg)
	prompt.WriteString("\n\n")

	if reviewSummary != "" {
		prompt.WriteString("审查摘要：\n")
		prompt.WriteString(reviewSummary)
		prompt.WriteString("\n\n")
	}

	if diffContent != "" {
		prompt.WriteString("代码变更（参考）：\n")
		prompt.WriteString(truncate(diffContent, 2000))
		prompt.WriteString("\n\n")
	}

	prompt.WriteString("请记录以下维度的项目知识（选择性记录，只记有价值的内容）：\n")
	prompt.WriteString("1. 架构模式：项目的模块划分、核心组件关系\n")
	prompt.WriteString("2. 代码约定：命名规范、错误处理模式、常用工具函数\n")
	prompt.WriteString("3. 易错点：容易出 bug 的地方、需要注意的边界条件\n")
	prompt.WriteString("4. 审查规则：项目特有的审查要点\n\n")
	prompt.WriteString("要求：\n")
	prompt.WriteString("- 只记录有长期价值的知识，不要记录临时性变更\n")
	prompt.WriteString("- 如果现有记忆已有相关内容，合并更新而非重复追加\n")
	prompt.WriteString("- 保持简洁，每条知识 1-2 行\n")
	prompt.WriteString("- 只返回记忆内容，不要其他说明\n")

	req := openai.ChatCompletionRequest{
		Model: c.config.Model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "你是一个项目知识管理助手。你的任务是从代码变更中提取有长期价值的项目知识。",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt.String(),
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
