package tui

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oliver/git-ai-commit/internal/ai"
	"github.com/oliver/git-ai-commit/internal/debug"
	"github.com/oliver/git-ai-commit/internal/diff"
	"github.com/oliver/git-ai-commit/internal/git"
)

type phase int

const (
	phaseSelectFiles phase = iota
	phaseStreaming
	phaseDone
)

// Message types for cross-phase communication.
type (
	streamChunkMsg struct {
		chunk ai.StreamChunk
	}
	aiRoundMsg struct {
		pending []ai.PendingToolCall
		err     error
	}
	stageDoneMsg struct {
		success     bool
		err         error
		diffContent string
		diffMode    string
	}
)

// CommitFlowOptions configures the commit flow.
type CommitFlowOptions struct {
	AutoConfirm     bool
	DryRun          bool
	DescFunc        func() string
	DiffCfg         diff.DiffPromptConfig
	GitRoot         string
	Client          *ai.Client
	ContinueSession *ai.PersistableSession
}

// CommitFlowResult is returned after the flow completes.
type CommitFlowResult struct {
	Success          bool
	CommitMessage    string
	CommitHash       string
	SelectedFiles    []string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	IsPartial        bool
	RemainingFiles   []string
}

// CommitFlowModel is the root model. It acts as a thin router,
// delegating display to the current Panel and coordinating phases.
type CommitFlowModel struct {
	phase phase
	opts  CommitFlowOptions
	files []diff.FileChange

	// Chrome
	header  HeaderBar
	footer  FooterBar
	overlay *Overlay
	panel   Panel

	// Window
	termWidth  int
	termHeight int

	// Stage state
	selectedFiles   []string
	stagedFiles     []string
	originalStaged  []string
	stagedDiff      string
	stagedDiffMode  string
	stageInProgress bool

	// AI session
	session     *ai.CommitSession
	continueSess *ai.PersistableSession

	// Stream actor (per-round)
	actor     *StreamActor
	actorDone bool

	// Commit result
	commitMessage    string
	commitHash       string
	verifiedHash     string
	remainingFiles   []string
	isPartialCommit  bool
	commitVerified   bool
	userAuthorized   bool
	lastPendingCalls []ai.PendingToolCall // saved for overlay confirmation
}

func NewCommitFlowModel(files []diff.FileChange, opts CommitFlowOptions) *CommitFlowModel {
	m := &CommitFlowModel{
		phase:        phaseSelectFiles,
		opts:         opts,
		files:        files,
		continueSess: opts.ContinueSession,
		header:       HeaderBar{PhaseLabel: "文件选择"},
	}

	// Auto-confirm mode: select all, skip file panel
	if opts.AutoConfirm && len(files) > 0 {
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		m.selectedFiles = paths
		m.userAuthorized = true
		m.phase = phaseStreaming
	}

	return m
}

func (m *CommitFlowModel) Init() tea.Cmd {
	if m.phase == phaseSelectFiles {
		m.panel = NewFilePanel(m.files)
		return m.panel.Init()
	}
	if m.phase == phaseStreaming {
		m.header.PhaseLabel = "AI 分析"
		return m.startStageCmd(m.selectedFiles)
	}
	return nil
}

func (m *CommitFlowModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// --- Global messages ---
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		// Forward to panel so FileSelector/viewport get dimensions
		if m.panel != nil {
			newPanel, cmd := m.panel.Update(msg)
			m.panel = newPanel
			return m, cmd
		}
		return m, nil

	case tea.InterruptMsg:
		m.stopActor()
		return m, tea.Quit

	case filePanelMsg:
		return m.handleFilePanelMsg(msg)

	case stageDoneMsg:
		return m.handleStageDone(msg)

	case streamChunkMsg:
		return m.handleStreamChunk(msg)

	case aiRoundMsg:
		return m.handleAiRound(msg)

	case OverlayResult:
		return m.handleOverlayResult(msg)
	}

	// --- Overlay takes priority ---
	if m.overlay != nil {
		newOv, ovCmd := m.overlay.Update(msg)
		m.overlay = newOv
		return m, ovCmd
	}

	// --- Global keys ---
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "ctrl+c":
			if !m.actorDone && m.actor != nil {
				m.stopActor()
			}
			return m, tea.Quit
		}
	}

	// --- Delegate to current panel ---
	if m.panel != nil {
		newPanel, panelCmd := m.panel.Update(msg)
		m.panel = newPanel
		// During streaming, always chain the stream actor read
		if m.phase == phaseStreaming && !m.actorDone && m.actor != nil {
			return m, tea.Batch(panelCmd, m.actor.NextMsgCmd(), m.spinnerCmd())
		}
		return m, panelCmd
	}

	return m, nil
}

// --- Phase transitions ---

func (m *CommitFlowModel) switchToStreaming() {
	m.phase = phaseStreaming
	m.header.PhaseLabel = "AI 分析"
	m.header.DiffMode = m.stagedDiffMode
	m.header.FileCount = len(m.files)
	m.header.SelectedCnt = len(m.selectedFiles)
}

func (m *CommitFlowModel) switchToDone() {
	m.phase = phaseDone
	m.header.PhaseLabel = "完成"
	m.header.DiffMode = ""
	tokenStr := ""
	if m.session != nil {
		if r := m.session.GetResult(); r.TotalTokens > 0 {
			m.header.TokenCount = r.TotalTokens
			tokenStr = fmt.Sprintf("prompt=%d completion=%d total=%d", r.PromptTokens, r.CompletionTokens, r.TotalTokens)
		}
	}
	// Create DonePanel with result
	r := m.GetResult()
	m.panel = NewDonePanel(r, m.commitMessage, m.verifiedHash, m.isPartialCommit, m.remainingFiles, m.commitVerified, tokenStr)
}

// --- Message handlers ---

func (m *CommitFlowModel) handleFilePanelMsg(msg filePanelMsg) (tea.Model, tea.Cmd) {
	if msg.cancelled {
		return m, tea.Quit
	}
	m.selectedFiles = msg.selectedFiles
	m.stageInProgress = true
	m.switchToStreaming()
	return m, tea.Batch(
		m.startStageCmd(msg.selectedFiles),
	)
}

func (m *CommitFlowModel) handleStageDone(msg stageDoneMsg) (tea.Model, tea.Cmd) {
	m.stageInProgress = false
	if !msg.success {
		m.switchToDone()
		return m, nil
	}
	m.header.SelectedCnt = len(m.selectedFiles)
	m.stagedDiff = msg.diffContent
	m.stagedDiffMode = msg.diffMode
	debug.Logf("stage done mode=%s bytes=%d", msg.diffMode, len(msg.diffContent))

	// Create streaming panel with stored window dimensions
	sp := NewStreamingPanel(msg.diffMode)
	sp.SetViewportSize(m.termWidth, m.termHeight-2)
	m.panel = sp
	return m, m.startGenerateCmd()
}

func (m *CommitFlowModel) handleStreamChunk(msg streamChunkMsg) (tea.Model, tea.Cmd) {
	sp, ok := m.panel.(*StreamingPanel)
	if !ok {
		return m, nil
	}

	if msg.chunk.Done {
		m.actorDone = true
		// aiRoundMsg follows in the channel — keep reading
		return m, m.actor.NextMsgCmd()
	}
	if msg.chunk.Thinking != "" {
		sp.streamThinking.WriteString(msg.chunk.Thinking)
	}
	if msg.chunk.Content != "" {
		sp.streamContent.WriteString(msg.chunk.Content)
	}
	// Auto-scroll
	if sp.vpReady {
		sp.viewport.GotoBottom()
	}
	return m, tea.Batch(
		m.actor.NextMsgCmd(),
		sp.SpinnerCmd(),
	)
}

func (m *CommitFlowModel) handleAiRound(msg aiRoundMsg) (tea.Model, tea.Cmd) {
	sp, ok := m.panel.(*StreamingPanel)
	if !ok {
		// Might have transitioned already
		return m, nil
	}
	if m.phase == phaseDone {
		return m, nil
	}

	if msg.err != nil {
		sp.AppendError(fmt.Sprintf("失败: %v", msg.err))
		m.switchToDone()
		return m, nil
	}

	// Check for completion
	if len(msg.pending) == 0 {
		result := m.session.GetResult()
		if result.Success {
			m.commitMessage = result.CommitMsg
			m.commitHash = extractHash(result.ToolResults)
			// Independent verification
			vResult := git.VerifyCommit()
			m.verifiedHash = vResult.Hash
			m.remainingFiles = append(vResult.RemainingStaged, vResult.RemainingDirty...)
			m.isPartialCommit = vResult.IsPartial
			m.commitVerified = vResult.Verified && vResult.Error == ""
			if !m.commitVerified && vResult.Error != "" {
				sp.AppendError(fmt.Sprintf("验证失败: %s", vResult.Error))
			}
			// Save session for --continue
			if err := m.session.SaveSession(); err != nil {
				debug.Logf("保存会话失败: %v", err)
			}
		}
		sp.FlushStream()
		sp.done = true
		m.switchToDone()
		return m, nil
	}

	// Pending tool calls
	m.panel = sp
	m.actorDone = false
	m.lastPendingCalls = msg.pending
	sp.FlushStream()
	sp.AppendToolCall(msg.pending)

	// Check if we need confirmation
	if m.requiresConfirm(msg.pending) {
		sp.SetAwaitingConfirm(true, m.hasSummarizeCall(msg.pending))
		m.overlay = NewConfirmOverlay(m.overlayType(msg.pending))
		return m, nil
	}

	// Auto-execute (non-sensitive tools or auto-confirm)
	sp.SetToolsCompleted()
	sp.Reset()
	return m, m.execPendingCmd(msg.pending, nil)
}

func (m *CommitFlowModel) handleOverlayResult(msg OverlayResult) (tea.Model, tea.Cmd) {
	m.overlay = nil

	if !msg.Confirmed {
		// User cancelled
		if sp, ok := m.panel.(*StreamingPanel); ok {
			sp.AppendOutput("→ 用户取消")
			sp.done = true
		}
		m.switchToDone()
		return m, nil
	}

	// Confirmed
	m.userAuthorized = true
	if sp, ok := m.panel.(*StreamingPanel); ok {
		sp.SetAwaitingConfirm(false, false)
		sp.authorizedCommit = true
		sp.AppendOutput("→ 已确认，执行中...")
		sp.SetToolsCompleted()
		sp.Reset()
	}

	pending := m.lastPendingCalls
	m.lastPendingCalls = nil
	return m, m.execPendingCmd(pending, nil)
}

func (m *CommitFlowModel) requiresConfirm(pending []ai.PendingToolCall) bool {
	if m.opts.AutoConfirm {
		return false
	}
	for _, tc := range pending {
		if tc.Name == "git_commit" || tc.Name == "git_commit_amend" || tc.Name == "summarize_changes" {
			return true
		}
	}
	return false
}

func (m *CommitFlowModel) hasSummarizeCall(pending []ai.PendingToolCall) bool {
	for _, tc := range pending {
		if tc.Name == "summarize_changes" {
			return true
		}
	}
	return false
}

func (m *CommitFlowModel) overlayType(pending []ai.PendingToolCall) OverlayType {
	for _, tc := range pending {
		if tc.Name == "summarize_changes" {
			return OverlayConfirmSummarize
		}
	}
	return OverlayConfirmCommit
}

// --- Commands ---

func (m *CommitFlowModel) startStageCmd(files []string) tea.Cmd {
	return func() tea.Msg {
		debug.Logf("staging files=%v", files)
		originalStaged := getStagedFiles()
		if err := diff.StageFiles(files); err != nil {
			debug.Logf("stage failed err=%v", err)
			return stageDoneMsg{success: false, err: err}
		}
		m.stagedFiles = files
		m.originalStaged = originalStaged

		processor := diff.NewDiffProcessor(m.opts.DiffCfg, m.opts.GitRoot)
		payloads, err := processor.BuildPayloadsForFiles(files)
		if err != nil || len(payloads) == 0 {
			debug.Logf("payload build failed err=%v count=%d", err, len(payloads))
			return stageDoneMsg{success: false, err: fmt.Errorf("获取 staged diff 失败: %w", err)}
		}
		return stageDoneMsg{
			success:     true,
			diffContent: payloads[0].Content,
			diffMode:    payloads[0].Mode,
		}
	}
}

func (m *CommitFlowModel) startGenerateCmd() tea.Cmd {
	conventionInfo := git.DetectConventions()
	debug.Logf("starting AI session diffMode=%s bytes=%d", m.stagedDiffMode, len(m.stagedDiff))

	var sess *ai.CommitSession
	var err error
	if m.continueSess != nil {
		sess, err = m.opts.Client.ContinueCommitSession(m.continueSess, m.stagedDiff, conventionInfo, m.selectedFiles)
	} else {
		sess, err = m.opts.Client.StartCommitSession(m.stagedDiff, m.opts.DescFunc(), conventionInfo, 3, m.selectedFiles)
	}
	if err != nil {
		return func() tea.Msg { return aiRoundMsg{err: err} }
	}
	m.session = sess

	m.actor = NewStreamActor()
	debug.Logf("AI session started")
	return m.actor.Run(func(send func(tea.Msg)) {
		pending, streamErr := sess.StreamAI(func(chunk ai.StreamChunk) {
			send(streamChunkMsg{chunk: chunk})
		})
		if streamErr != nil {
			send(aiRoundMsg{err: streamErr})
			return
		}
		send(aiRoundMsg{pending: pending})
	})
}

func (m *CommitFlowModel) execPendingCmd(pending []ai.PendingToolCall, authorized []bool) tea.Cmd {
	if authorized == nil {
		authorized = make([]bool, len(pending))
		for i, tc := range pending {
			if tc.Name == "git_commit" || tc.Name == "git_commit_amend" {
				authorized[i] = m.userAuthorized
			} else {
				authorized[i] = true
			}
		}
	}

	m.actor = NewStreamActor()
	m.actorDone = false
	return m.actor.Run(func(send func(tea.Msg)) {
		newPending, err := m.session.ExecuteAndResumeWithStream(pending, authorized, func(chunk ai.StreamChunk) {
			send(streamChunkMsg{chunk: chunk})
		})
		if err != nil {
			send(aiRoundMsg{err: err})
			return
		}
		send(aiRoundMsg{pending: newPending})
	})
}

func (m *CommitFlowModel) spinnerCmd() tea.Cmd {
	if sp, ok := m.panel.(*StreamingPanel); ok {
		return sp.SpinnerCmd()
	}
	return nil
}

func (m *CommitFlowModel) stopActor() {
	if m.actor != nil {
		m.actor.Stop()
		m.actor = nil
	}
}

// --- View ---

func (m *CommitFlowModel) View() string {
	w := m.termWidth
	if w == 0 {
		w = 80
	}
	h := m.termHeight
	if h == 0 {
		h = 24
	}

	// Panel
	panelView := ""
	if m.panel != nil {
		panelView = m.panel.View(w, h-2)
	}

	// Footer — overlay replaces footer with a compact confirmation bar
	// instead of covering the entire screen, so user can see AI output.
	m.footer = NewFooterBar(m.footerEntries())
	m.footer.RightMsg = m.footerRightMsg()

	footerView := m.footer.View(w)
	if m.overlay != nil {
		footerView = m.overlay.View(w, 3)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		m.header.View(w),
		panelView,
		footerView,
	)
}

func (m *CommitFlowModel) footerEntries() []HelpEntry {
	if m.overlay != nil {
		return []HelpEntry{
			{"Y/Enter", "确认"},
			{"N/Esc", "取消"},
		}
	}
	if m.panel != nil {
		return m.panel.Help()
	}
	return nil
}

func (m *CommitFlowModel) footerRightMsg() string {
	return ""
}

// --- Result ---

func (m *CommitFlowModel) GetResult() CommitFlowResult {
	if m.session == nil {
		return CommitFlowResult{SelectedFiles: m.selectedFiles}
	}
	result := m.session.GetResult()
	hash := m.verifiedHash
	if hash == "" {
		hash = m.commitHash
	}
	return CommitFlowResult{
		Success:          m.commitMessage != "",
		CommitMessage:    m.commitMessage,
		CommitHash:       hash,
		SelectedFiles:    m.selectedFiles,
		PromptTokens:     result.PromptTokens,
		CompletionTokens: result.CompletionTokens,
		TotalTokens:      result.TotalTokens,
		IsPartial:        m.isPartialCommit,
		RemainingFiles:   m.remainingFiles,
	}
}

// --- Helpers ---

var hashRe = regexp.MustCompile(`SUCCESS[^\n]*?([0-9a-f]{7,40})`)

func extractHash(results []ai.ToolCallResult) string {
	for _, tr := range results {
		if tr.ToolName == "git_commit" || tr.ToolName == "git_commit_amend" {
			if matches := hashRe.FindStringSubmatch(tr.Result); len(matches) >= 2 {
				return matches[1]
			}
		}
	}
	return ""
}

func getStagedFiles() []string {
	gitRoot, err := git.GetGitRoot()
	if err != nil {
		return nil
	}
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = gitRoot
	output, _ := cmd.Output()
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var result []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			result = append(result, l)
		}
	}
	return result
}

// RunCommitFlow is the public entry point for the TUI.
func RunCommitFlow(files []diff.FileChange, opts CommitFlowOptions) (CommitFlowResult, error) {
	m := NewCommitFlowModel(files, opts)

	if os.Getenv("TERM") == "" {
		os.Setenv("TERM", "dumb")
	}

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	model, err := p.Run()
	if err != nil {
		return CommitFlowResult{}, fmt.Errorf("运行 TUI 失败: %w", err)
	}

	fm, ok := model.(*CommitFlowModel)
	if !ok {
		return CommitFlowResult{}, fmt.Errorf("类型转换失败")
	}

	return fm.GetResult(), nil
}
