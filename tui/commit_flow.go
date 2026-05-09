package tui

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
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

type aiRoundMsg struct {
	pending []ai.PendingToolCall
	err     error
}

type streamChunkMsg struct {
	chunk ai.StreamChunk
}

type stageDoneMsg struct {
	success     bool
	err         error
	diffContent string
	diffMode    string
}

var (
	phaseHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("63")).
				Background(lipgloss.Color("235")).
				Padding(0, 2)

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("63"))

	toolCallLineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241")).
				PaddingLeft(1)

	successLineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")).
				Bold(true).
				PaddingLeft(1)

	errorLineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Bold(true).
			PaddingLeft(1)

	thinkingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")).
			PaddingLeft(1)

	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("237")).
			PaddingLeft(1)
)

type CommitFlowOptions struct {
	AutoConfirm     bool
	DryRun          bool
	DescFunc        func() string // lazy description, blocks until ready
	DiffCfg         diff.DiffPromptConfig
	GitRoot         string
	Client          *ai.Client
	ContinueSession *ai.PersistableSession // 不为 nil 时跳过文件选择，使用 continue 会话
}

type CommitFlowModel struct {
	phase           phase
	opts            CommitFlowOptions
	files           []diff.FileChange
	fileSelector    *FileSelector
	spinner         spinner.Model
	termWidth       int
	termHeight      int
	ready           bool
	stageInProgress bool

	session        *ai.CommitSession
	continueSess   *ai.PersistableSession // --continue 时加载的持久化会话
	pendingCalls   []ai.PendingToolCall
	commitMessage  string
	commitHash     string
	selectedFiles  []string
	stagedFiles    []string
	originalStaged []string
	streamChan     chan tea.Msg
	streamDoneCh   chan struct{} // signals goroutines to exit
	streamDoneOnce sync.Once     // prevents double-close on streamDoneCh

	stagedDiffContent string // diff of staged files, set after startStageCmd
	stagedDiffMode    string

	streamThinking strings.Builder
	streamContent  strings.Builder
	streamDone     bool

	toolRunNames    []string // current executing tool names (shown with spinner)

	// post-commit verification state
	verifiedHash    string
	remainingFiles  []string
	isPartialCommit bool
	commitVerified  bool

	outputLog                strings.Builder
	reviewOutput             strings.Builder // 持久化的 AI review 内容（markdown 渲染）
	contentViewport          viewport.Model
	vpReady                  bool
	awaitingConfirm          bool
	awaitingSummarizeConfirm bool
	userAuthorizedCommit     bool
}

func NewCommitFlowModel(files []diff.FileChange, opts CommitFlowOptions) *CommitFlowModel {
	fs := NewFileSelector(files)
	s := spinner.New()
	s.Style = spinnerStyle
	s.Spinner = spinner.Dot

	model := &CommitFlowModel{
		phase:        phaseSelectFiles,
		opts:         opts,
		files:        files,
		fileSelector: fs,
		spinner:      s,
		continueSess: opts.ContinueSession,
		streamDoneCh: make(chan struct{}),
	}

	// -y 模式：跳过文件选择，直接全选进入 streaming
	if opts.AutoConfirm && len(files) > 0 {
		var paths []string
		for _, f := range files {
			paths = append(paths, f.Path)
		}
		model.selectedFiles = paths
		model.userAuthorizedCommit = true
		model.phase = phaseStreaming
	}

	return model
}

func (m *CommitFlowModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.spinner.Tick,
	}

	if m.opts.AutoConfirm && len(m.selectedFiles) > 0 {
		cmds = append(cmds, m.startStageCmd(m.selectedFiles))
	} else {
		cmds = append(cmds, m.fileSelector.Init())
	}

	return tea.Batch(cmds...)
}

func (m *CommitFlowModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		if !m.ready {
			m.ready = true
		}
		if m.phase == phaseSelectFiles {
			fsModel, cmd := m.fileSelector.Update(msg)
			m.fileSelector = fsModel.(*FileSelector)
			return m, cmd
		}
		contentH := msg.Height - 4
		if contentH < 1 {
			contentH = 1
		}
		if !m.vpReady {
			m.contentViewport = viewport.New(msg.Width, contentH)
			m.contentViewport.Style = lipgloss.NewStyle().Padding(0, 1)
			m.contentViewport.MouseWheelEnabled = true
			m.vpReady = true
		} else {
			m.contentViewport.Width = msg.Width
			m.contentViewport.Height = contentH
		}
		if m.phase == phaseStreaming || m.phase == phaseDone {
			m.refreshViewport()
		}
		return m, nil

	case tea.InterruptMsg:
		m.closeStreamDone()
		return m, tea.Quit

	case streamChunkMsg:
		if msg.chunk.Done {
			m.streamDone = true
			m.refreshViewport()
			return m, nil
		}
		if msg.chunk.Thinking != "" {
			m.streamThinking.WriteString(msg.chunk.Thinking)
		}
		if msg.chunk.Content != "" {
			m.streamContent.WriteString(msg.chunk.Content)
		}
		if m.phase == phaseStreaming && m.vpReady {
			m.refreshViewport()
			m.contentViewport.GotoBottom()
		}
		return m, tea.Batch(
			func() tea.Msg {
				select {
				case msg := <-m.streamChan:
					return msg
				case <-m.streamDoneCh:
					return nil
				}
			},
			m.spinner.Tick,
		)

	case aiRoundMsg:
		m.spinner, _ = m.spinner.Update(msg)
		// 防止重复处理
		if m.phase == phaseDone {
			return m, nil
		}
		if msg.err != nil {
			m.appendErrorLine(fmt.Sprintf("失败: %v", msg.err))
			m.phase = phaseDone
			m.refreshViewport()
			return m, nil
		}
		if msg.pending == nil || len(msg.pending) == 0 {
			result := m.session.GetResult()
			if result.Success {
				m.commitMessage = result.CommitMsg
				m.commitHash = extractHash(result.ToolResults)
				// 独立验证：用 git 命令确认提交真实成功
				vResult := git.VerifyCommit()
				m.verifiedHash = vResult.Hash
				m.remainingFiles = append(vResult.RemainingStaged, vResult.RemainingDirty...)
				m.isPartialCommit = vResult.IsPartial
				m.commitVerified = vResult.Verified && vResult.Error == ""
				if !m.commitVerified && vResult.Error != "" {
					m.appendErrorLine(fmt.Sprintf("验证失败: %s", vResult.Error))
				}
				// 保存会话用于未来的 --continue
				if err := m.session.SaveSession(); err != nil {
					debug.Logf("保存会话失败: %v", err)
				}
			}
			m.flushStreamToOutput()
			m.phase = phaseDone
			m.refreshViewport()
			return m, nil
		}
		m.pendingCalls = msg.pending
		m.flushStreamToOutput()
		if m.hasCommitCall() && !m.opts.AutoConfirm {
			m.awaitingConfirm = true
			m.refreshViewport()
			return m, nil
		}
		if m.hasSummarizeCall() && !m.opts.AutoConfirm {
			m.awaitingConfirm = true
			m.awaitingSummarizeConfirm = true
			m.refreshViewport()
			return m, nil
		}
		m.appendToolCallLines(msg.pending)
		m.refreshViewport()
		return m, m.execPendingCmd()

	case stageDoneMsg:
		m.stageInProgress = false
		if m.phase == phaseDone {
			return m, nil
		}
		if msg.success {
			debug.Logf("stageDoneMsg success mode=%s diffBytes=%d", msg.diffMode, len(msg.diffContent))
			m.stagedDiffContent = msg.diffContent
			m.stagedDiffMode = msg.diffMode
			m.phase = phaseStreaming
			m.refreshViewport()
			return m, tea.Batch(m.spinner.Tick, m.startGenerateCmd())
		}
		debug.Logf("stageDoneMsg failed err=%v", msg.err)
		m.phase = phaseDone
		m.appendErrorLine(fmt.Sprintf("暂存文件失败: %v", msg.err))
		m.refreshViewport()
		return m, nil
	}

	switch m.phase {
	case phaseSelectFiles:
		fsModel, cmd := m.fileSelector.Update(msg)
		m.fileSelector = fsModel.(*FileSelector)
		if m.fileSelector.done {
			if m.stageInProgress {
				return m, cmd
			}
			selected := m.fileSelector.GetSelectedFiles()
			debug.Logf("selector done quitting=%v selected=%d", m.fileSelector.quitting, len(selected))
			if m.fileSelector.quitting || len(selected) == 0 {
				debug.Logf("selector exits without staging")
				return m, tea.Quit
			}
			m.stageInProgress = true
			m.selectedFiles = selected
			debug.Logf("start stage selected=%v", selected)
			return m, tea.Batch(cmd, m.spinner.Tick, m.startStageCmd(selected))
		}
		return m, cmd

	case phaseStreaming:
		var cmd tea.Cmd
		m.contentViewport, cmd = m.contentViewport.Update(msg)
		var tickCmd tea.Cmd
		m.spinner, tickCmd = m.spinner.Update(msg)
		cmd = tea.Batch(cmd, tickCmd)
		switch msg := msg.(type) {
		case tea.KeyMsg:
			s := msg.String()
			switch s {
			case "enter":
				if m.awaitingConfirm {
					isSummarize := m.awaitingSummarizeConfirm
					m.awaitingConfirm = false
					m.awaitingSummarizeConfirm = false
					m.userAuthorizedCommit = true
					if isSummarize {
						m.appendLine("→ 已确认，进入审查和提交阶段...")
					} else {
						m.appendLine("→ 已确认提交，执行中...")
					}
					m.refreshViewport()
					return m, m.execPendingCmd()
				}
			case "y", "Y":
				if m.awaitingConfirm {
					isSummarize := m.awaitingSummarizeConfirm
					m.awaitingConfirm = false
					m.awaitingSummarizeConfirm = false
					m.userAuthorizedCommit = true
					if isSummarize {
						m.appendLine("→ 确认，进入审查和提交阶段...")
					} else {
						m.appendLine("→ 确认提交，执行中...")
					}
					m.refreshViewport()
					return m, m.execPendingCmd()
				}
			case "n", "N":
				if m.awaitingConfirm {
					m.awaitingConfirm = false
					m.awaitingSummarizeConfirm = false
					m.appendLine("→ 用户取消提交")
					m.phase = phaseDone
					m.refreshViewport()
					return m, nil
				}
			case "esc":
				if m.awaitingConfirm {
					m.awaitingConfirm = false
					m.awaitingSummarizeConfirm = false
					m.appendLine("→ 用户取消提交")
					m.phase = phaseDone
					m.refreshViewport()
					return m, nil
				}
			case "ctrl+c":
				m.closeStreamDone()
				return m, tea.Quit
			}
		}
		m.refreshViewport()
		if !m.streamDone {
			m.contentViewport.GotoBottom()
		}
		if m.streamDone {
			return m, cmd
		}
		return m, tea.Batch(cmd, m.spinner.Tick, func() tea.Msg {
			select {
			case msg := <-m.streamChan:
				return msg
			case <-m.streamDoneCh:
				return nil
			}
		})

	case phaseDone:
		var doneCmd tea.Cmd
		m.contentViewport, doneCmd = m.contentViewport.Update(msg)
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.Type == tea.KeyEnter || msg.Type == tea.KeyEscape || msg.String() == "q" || msg.String() == "ctrl+c" {
				m.closeStreamDone()
				return m, tea.Quit
			}
		}
		return m, doneCmd
	}

	return m, nil
}

func (m *CommitFlowModel) refreshViewport() {
	w := m.termWidth
	if w == 0 {
		w = 80
	}
	h := m.termHeight
	if h == 0 {
		h = 24
	}
	contentH := h - 4
	if contentH < 1 {
		contentH = 1
	}
	contentW := w - 4

	if !m.vpReady {
		m.contentViewport = viewport.New(w, contentH)
		m.contentViewport.Style = lipgloss.NewStyle().Padding(0, 1)
		m.contentViewport.MouseWheelEnabled = true
		m.vpReady = true
	}

	var vis strings.Builder

	// outputLog: tool calls, status messages (already styled)
	if m.outputLog.Len() > 0 {
		vis.WriteString(m.outputLog.String())
	}

	// reviewOutput: flushed review content (normal display)
	if m.reviewOutput.Len() > 0 {
		if vis.Len() > 0 {
			vis.WriteString("\n")
		}
		vis.WriteString(renderMarkdown(m.reviewOutput.String(), contentW))
	}

	// toolRunNames: running tools (shown with spinner, inline in the flow)
	if len(m.toolRunNames) > 0 {
		for _, name := range m.toolRunNames {
			vis.WriteString("\n" + toolCallLineStyle.Render(fmt.Sprintf("⡿ │ %s", name)))
		}
	}

	// streamThinking: current thinking as dim/italic text
	if m.streamThinking.Len() > 0 {
		if vis.Len() > 0 {
			vis.WriteString("\n")
		}
		thinkingText := renderMarkdown(m.streamThinking.String(), contentW)
		vis.WriteString(thinkingStyle.Render(thinkingText))
	}

	// streamContent: current content as flowing text
	if m.streamContent.Len() > 0 {
		if vis.Len() > 0 {
			vis.WriteString("\n")
		}
		vis.WriteString(renderMarkdown(m.streamContent.String(), contentW))
	}

	// Initial spinner when nothing yet
	if m.phase == phaseStreaming && vis.Len() == 0 {
		vis.WriteString("\n" + lipgloss.NewStyle().PaddingLeft(1).Render(m.spinner.View()+" AI 正在分析代码变更..."))
	}

	// Confirmation prompts
	if m.phase == phaseStreaming && m.awaitingConfirm {
		if m.awaitingSummarizeConfirm {
			vis.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(1).Bold(true).Foreground(lipgloss.Color("220")).Render("➜ 等待确认: 代码已理解，按 Y/Enter 进入审查和提交阶段 / N 取消 / Esc 退出"))
		} else {
			vis.WriteString("\n\n" + lipgloss.NewStyle().PaddingLeft(1).Bold(true).Foreground(lipgloss.Color("220")).Render("➜ 等待确认: 按 Y/Enter 确认提交 / N 取消 / Esc 退出"))
		}
	}

	// Done state
	if m.phase == phaseDone {
		if m.commitMessage != "" {
			vis.WriteString("\n\n" + successLineStyle.Render("✓ 提交成功"))
			if m.verifiedHash != "" {
				vis.WriteString("\n" + toolCallLineStyle.Render(fmt.Sprintf("提交哈希: %s", m.verifiedHash)))
			} else if m.commitHash != "" {
				vis.WriteString("\n" + toolCallLineStyle.Render(fmt.Sprintf("AI 报告哈希: %s", m.commitHash)))
			}
			if m.commitVerified {
				if m.isPartialCommit {
					vis.WriteString("\n" + lipgloss.NewStyle().PaddingLeft(1).Foreground(lipgloss.Color("220")).Render("⚠ 部分提交: 仍有文件未提交"))
					for _, f := range m.remainingFiles {
						vis.WriteString("\n" + toolCallLineStyle.Render(fmt.Sprintf("  \u2022 %s", f)))
					}
				} else {
					vis.WriteString("\n" + toolCallLineStyle.Render("状态: 工作区干净"))
				}
			}
			// Commit message as flowing markdown, no border
			vis.WriteString("\n\n")
			vis.WriteString(renderMarkdown(m.commitMessage, contentW))
		} else {
			vis.WriteString("\n" + errorLineStyle.Render("✗ 提交未完成"))
		}
	}

	m.contentViewport.SetContent(vis.String())
}
func (m *CommitFlowModel) closeStreamDone() {
	m.streamDoneOnce.Do(func() { close(m.streamDoneCh) })
}

func (m *CommitFlowModel) appendLine(line string) {
	m.outputLog.WriteString(line + "\n")
}

func (m *CommitFlowModel) appendErrorLine(line string) {
	m.outputLog.WriteString(errorLineStyle.Render(line) + "\n")
}

func (m *CommitFlowModel) appendToolCallLines(calls []ai.PendingToolCall) {
	m.toolRunNames = nil
	for _, tc := range calls {
		label := tc.Name
		switch tc.Name {
		case "read_file":
			label = tc.Name + " " + tc.ArgString("path")
		case "git_config_get":
			label = tc.Name + " " + tc.ArgString("key")
		case "search_references":
			label = tc.Name + " '" + tc.ArgString("symbol") + "'"
		}
		m.toolRunNames = append(m.toolRunNames, label)
	}
}

func (m *CommitFlowModel) markToolsCompleted() {
	if len(m.toolRunNames) == 0 {
		return
	}
	for _, name := range m.toolRunNames {
		m.reviewOutput.WriteString("✓ │ " + name + "\n")
	}
	m.toolRunNames = nil
}

func (m *CommitFlowModel) flushStreamToOutput() {
	if m.streamThinking.Len() > 0 {
		m.reviewOutput.WriteString(m.streamThinking.String() + "\n")
		m.streamThinking.Reset()
	}
	if m.streamContent.Len() > 0 {
		m.reviewOutput.WriteString(m.streamContent.String() + "\n")
		m.streamContent.Reset()
	}
}

func (m *CommitFlowModel) startStageCmd(files []string) tea.Cmd {
	return func() tea.Msg {
		debug.Logf("startStageCmd begin files=%v", files)
		originalStaged := getStagedFiles()
		if err := diff.StageFiles(files); err != nil {
			debug.Logf("startStageCmd stage failed err=%v", err)
			return stageDoneMsg{success: false, err: err}
		}
		m.stagedFiles = files
		m.originalStaged = originalStaged

		// stage 完成后，基于实际暂存文件重新获取 diff，确保 AI 分析与提交内容一致
		processor := diff.NewDiffProcessor(m.opts.DiffCfg, m.opts.GitRoot)
		payloads, err := processor.BuildPayloadsForFiles(files)
		if err != nil || len(payloads) == 0 {
			debug.Logf("startStageCmd payload build failed err=%v payloadCount=%d", err, len(payloads))
			return stageDoneMsg{success: false, err: fmt.Errorf("获取 staged diff 失败: %w", err)}
		}
		debug.Logf("startStageCmd payload ready mode=%s bytes=%d", payloads[0].Mode, len(payloads[0].Content))
		return stageDoneMsg{
			success:     true,
			diffContent: payloads[0].Content,
			diffMode:    payloads[0].Mode,
		}
	}
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

func (m *CommitFlowModel) startGenerateCmd() tea.Cmd {
	conventionInfo := git.DetectConventions()
	debug.Logf("startGenerateCmd begin stagedDiffMode=%s stagedDiffBytes=%d", m.stagedDiffMode, len(m.stagedDiffContent))

	var sess *ai.CommitSession
	var err error
	if m.continueSess != nil {
		debug.Logf("startGenerateCmd using ContinueCommitSession")
		sess, err = m.opts.Client.ContinueCommitSession(
			m.continueSess, m.stagedDiffContent, conventionInfo, m.selectedFiles,
		)
	} else {
		// 使用 stage 后的实际 diff，确保 AI 分析的内容与提交完全一致
		sess, err = m.opts.Client.StartCommitSession(
			m.stagedDiffContent, m.opts.DescFunc(), conventionInfo, 3, m.selectedFiles,
		)
	}
	if err != nil {
		debug.Logf("startGenerateCmd create session failed err=%v", err)
		return func() tea.Msg { return aiRoundMsg{err: err} }
	}
	debug.Logf("startGenerateCmd session started")
	m.session = sess
	m.streamThinking.Reset()
	m.streamContent.Reset()
	m.streamDone = false
	m.refreshViewport()

	m.streamChan = make(chan tea.Msg, 64)
	go func() {
		defer func() {
			// Drain channel to prevent goroutine leak
			close(m.streamChan)
		}()
		pending, streamErr := sess.StreamAI(func(chunk ai.StreamChunk) {
			select {
			case m.streamChan <- streamChunkMsg{chunk: chunk}:
			case <-m.streamDoneCh:
			}
		})
		if streamErr != nil {
			select {
			case m.streamChan <- aiRoundMsg{err: streamErr}:
			case <-m.streamDoneCh:
			}
			return
		}
		select {
		case m.streamChan <- aiRoundMsg{pending: pending}:
		case <-m.streamDoneCh:
		}
	}()

	return func() tea.Msg {
		select {
		case msg := <-m.streamChan:
			return msg
		case <-m.streamDoneCh:
			return nil
		}
	}
}

func (m *CommitFlowModel) execPendingCmd() tea.Cmd {
	m.streamThinking.Reset()
	m.streamContent.Reset()
	m.streamDone = false

	// Ensure toolRunNames is populated from pendingCalls (needed for commit/summarize confirm path)
	if len(m.toolRunNames) == 0 && len(m.pendingCalls) > 0 {
		for _, tc := range m.pendingCalls {
			m.toolRunNames = append(m.toolRunNames, tc.Name)
		}
	}
	m.refreshViewport()

	m.streamChan = make(chan tea.Msg, 64)
	go func() {
		defer func() {
			close(m.streamChan)
		}()
		authorized := make([]bool, len(m.pendingCalls))
		for i, tc := range m.pendingCalls {
			if tc.Name == "git_commit" || tc.Name == "git_commit_amend" {
				if m.userAuthorizedCommit {
					authorized[i] = true
				} else {
					authorized[i] = false
				}
			} else {
				authorized[i] = true
			}
		}
		pending, err := m.session.ExecuteAndResumeWithStream(m.pendingCalls, authorized, func(chunk ai.StreamChunk) {
			select {
			case m.streamChan <- streamChunkMsg{chunk: chunk}:
			case <-m.streamDoneCh:
			}
		})
		if err != nil {
			select {
			case m.streamChan <- aiRoundMsg{err: err}:
			case <-m.streamDoneCh:
			}
			return
		}
		select {
		case m.streamChan <- aiRoundMsg{pending: pending}:
		case <-m.streamDoneCh:
		}
	}()

	return func() tea.Msg {
		select {
		case msg := <-m.streamChan:
			return msg
		case <-m.streamDoneCh:
			return nil
		}
	}
}

func (m *CommitFlowModel) hasCommitCall() bool {
	for _, tc := range m.pendingCalls {
		if tc.Name == "git_commit" || tc.Name == "git_commit_amend" {
			return true
		}
	}
	return false
}

func (m *CommitFlowModel) hasSummarizeCall() bool {
	for _, tc := range m.pendingCalls {
		if tc.Name == "summarize_changes" {
			return true
		}
	}
	return false
}

func (m *CommitFlowModel) View() string {
	switch m.phase {
	case phaseSelectFiles:
		return m.fileSelector.View()

	case phaseStreaming:
		w := m.termWidth
		if w == 0 {
			w = 80
		}
		header := phaseHeaderStyle.Width(w).Render("  AI 代码审查 & 提交")
		if m.stagedDiffMode != "" && m.stagedDiffMode != "完整 diff" {
			header = phaseHeaderStyle.Width(w).Render(fmt.Sprintf("  AI 代码审查 & 提交（%s 模式）", m.stagedDiffMode))
		}
		if !m.vpReady {
			return lipgloss.JoinVertical(lipgloss.Left, header, m.spinner.View()+" AI 正在分析代码变更...")
		}
		return lipgloss.JoinVertical(lipgloss.Left, header, m.contentViewport.View())

	case phaseDone:
		w := m.termWidth
		if w == 0 {
			w = 80
		}
		header := phaseHeaderStyle.Width(w).Render("  完成")
		help := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Background(lipgloss.Color("235")).
			Padding(0, 2).
			Width(w).
			Render("  按 Enter / Esc / q 退出")

		var tokenInfo string
		if m.session != nil {
			result := m.session.GetResult()
			if result.TotalTokens > 0 {
				tokenInfo = lipgloss.NewStyle().
					Foreground(lipgloss.Color("241")).
					Background(lipgloss.Color("235")).
					Padding(0, 2).
					Width(w).
					Render(fmt.Sprintf("  Token 消耗: prompt=%d  completion=%d  total=%d", result.PromptTokens, result.CompletionTokens, result.TotalTokens))
			}
		}

		if !m.vpReady {
			content := lipgloss.JoinVertical(lipgloss.Left, header, help)
			if tokenInfo != "" {
				content = lipgloss.JoinVertical(lipgloss.Left, header, tokenInfo, help)
			}
			return content
		}

		if tokenInfo != "" {
			return lipgloss.JoinVertical(lipgloss.Left, header, m.contentViewport.View(), tokenInfo, help)
		}
		return lipgloss.JoinVertical(lipgloss.Left, header, m.contentViewport.View(), help)
	}

	return ""
}

func (m *CommitFlowModel) GetResult() CommitFlowResult {
	if m.session == nil {
		return CommitFlowResult{
			SelectedFiles: m.selectedFiles,
		}
	}
	result := m.session.GetResult()

	// 优先使用验证后的 hash（比 AI 报告的更可靠）
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
		return CommitFlowResult{}, fmt.Errorf("运行 TUI 失败：%w", err)
	}

	fm, ok := model.(*CommitFlowModel)
	if !ok {
		return CommitFlowResult{}, fmt.Errorf("类型转换失败")
	}

	return fm.GetResult(), nil
}
