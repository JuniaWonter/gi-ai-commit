package tui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oliver/git-ai-commit/internal/ai"
	"github.com/oliver/git-ai-commit/internal/diff"
	"github.com/oliver/git-ai-commit/internal/git"
)

type phase int

const (
	phaseSelectFiles phase = iota
	phaseDiffView
	phaseStaging
	phaseStreaming
	phaseDone
)

type aiResponseMsg struct {
	pending []ai.PendingToolCall
	err     error
}

type streamChunkMsg struct {
	chunk ai.StreamChunk
}

type execDoneMsg struct {
	pending []ai.PendingToolCall
	err     error
}

type stageDoneMsg struct {
	success bool
	err     error
}

type resetDoneMsg struct {
	success bool
	err     error
}

var (
	phaseHeaderStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("63")).
		Background(lipgloss.Color("235")).
		Padding(0, 2)

	phaseHelpStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
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

	tokenLineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("63")).
			PaddingLeft(1)
)

type CommitFlowOptions struct {
	AutoConfirm bool
	DryRun      bool
	Desc        string
	DiffContent string
	DiffMode    string
	Client      *ai.Client
}

type CommitFlowModel struct {
	phase        phase
	opts         CommitFlowOptions
	files        []diff.FileChange
	fileSelector *FileSelector
	textarea     textarea.Model
	spinner      spinner.Model
	termWidth    int
	termHeight   int
	ready        bool

	session        *ai.CommitSession
	pendingCalls   []ai.PendingToolCall
	commitMessage  string
	commitHash     string
	selectedFiles  []string
	stagedFiles    []string
	originalStaged []string
	streamChan     chan tea.Msg

	streamThinking strings.Builder
	streamContent  strings.Builder
	streamDone     bool

	outputLog       strings.Builder
	contentViewport viewport.Model
	vpReady         bool
	awaitingConfirm bool
}

func NewCommitFlowModel(files []diff.FileChange, opts CommitFlowOptions) *CommitFlowModel {
	fs := NewFileSelector(files)
	s := spinner.New()
	s.Style = spinnerStyle
	s.Spinner = spinner.Dot

	ta := textarea.New()
	ta.Placeholder = "输入 commit message..."
	ta.CharLimit = 500
	ta.SetWidth(80)
	ta.SetHeight(10)

	return &CommitFlowModel{
		phase:        phaseSelectFiles,
		opts:         opts,
		files:        files,
		fileSelector: fs,
		spinner:      s,
		textarea:     ta,
	}
}

func (m *CommitFlowModel) Init() tea.Cmd {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	return tea.Batch(m.fileSelector.Init(), m.spinner.Tick, func() tea.Msg {
		sig := <-sigChan
		return signalMsg{sig: sig}
	})
}

type signalMsg struct {
	sig os.Signal
}

func (m *CommitFlowModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		if !m.ready {
			m.ready = true
		}
		if m.phase == phaseSelectFiles || m.phase == phaseDiffView {
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
			m.vpReady = true
		} else {
			m.contentViewport.Width = msg.Width
			m.contentViewport.Height = contentH
		}
		if m.phase == phaseStreaming || m.phase == phaseDone {
			m.refreshViewport()
		}
		return m, nil

	case signalMsg:
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
			func() tea.Msg { return <-m.streamChan },
			m.spinner.Tick,
		)

	case aiResponseMsg:
		m.spinner, _ = m.spinner.Update(msg)
		if msg.err != nil {
			m.appendErrorLine(fmt.Sprintf("AI 调用失败: %v", msg.err))
			m.phase = phaseDone
			m.refreshViewport()
			return m, nil
		}
		if msg.pending == nil || len(msg.pending) == 0 {
			result := m.session.GetResult()
			if result.Success {
				m.commitMessage = result.CommitMsg
				m.commitHash = extractHash(result.ToolResults)
			}
			m.phase = phaseDone
			m.refreshViewport()
			return m, nil
		}
		m.pendingCalls = msg.pending
		if m.hasCommitCall() {
			m.awaitingConfirm = true
			m.refreshViewport()
			return m, nil
		}
		m.appendToolCallLines(msg.pending)
		m.refreshViewport()
		return m, m.autoExecCmd()

	case execDoneMsg:
		m.spinner, _ = m.spinner.Update(msg)
		if msg.err != nil {
			m.appendErrorLine(fmt.Sprintf("执行失败: %v", msg.err))
			m.phase = phaseDone
			m.refreshViewport()
			return m, nil
		}
		if msg.pending == nil || len(msg.pending) == 0 {
			result := m.session.GetResult()
			if result.Success {
				m.commitMessage = result.CommitMsg
				m.commitHash = extractHash(result.ToolResults)
			}
			m.phase = phaseDone
			m.refreshViewport()
			return m, nil
		}
		m.pendingCalls = msg.pending
		if m.hasCommitCall() {
			m.awaitingConfirm = true
			m.refreshViewport()
			return m, nil
		}
		m.appendToolCallLines(msg.pending)
		m.refreshViewport()
		return m, m.autoExecCmd()

	case stageDoneMsg:
		if msg.success {
			m.phase = phaseStreaming
			m.streamThinking.Reset()
			m.streamContent.Reset()
			m.streamDone = false
			m.refreshViewport()
			return m, tea.Batch(m.spinner.Tick, m.startGenerateCmd())
		}
		return m, tea.Quit

	case resetDoneMsg:
		return m, tea.Quit
	}

	switch m.phase {
	case phaseSelectFiles:
		fsModel, cmd := m.fileSelector.Update(msg)
		m.fileSelector = fsModel.(*FileSelector)
		if m.fileSelector.done {
			selected := m.fileSelector.GetSelectedFiles()
			if m.fileSelector.quitting || len(selected) == 0 {
				return m, tea.Quit
			}
			m.selectedFiles = selected
			m.phase = phaseStaging
			return m, tea.Batch(cmd, m.spinner.Tick, m.startStageCmd(selected))
		}
		return m, cmd

	case phaseDiffView:
		fsModel, cmd := m.fileSelector.Update(msg)
		m.fileSelector = fsModel.(*FileSelector)
		if !m.fileSelector.showDiff && !m.fileSelector.diffLoading {
			m.phase = phaseSelectFiles
		}
		if m.fileSelector.done {
			selected := m.fileSelector.GetSelectedFiles()
			if m.fileSelector.quitting || len(selected) == 0 {
				return m, tea.Quit
			}
			m.selectedFiles = selected
			m.phase = phaseStaging
			return m, tea.Batch(cmd, m.spinner.Tick, m.startStageCmd(selected))
		}
		return m, cmd

	case phaseStaging:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, tea.Batch(cmd, m.spinner.Tick)

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
			case "y", "Y":
				if m.awaitingConfirm {
					m.awaitingConfirm = false
					m.appendLine("→ 确认提交，执行中...")
					m.refreshViewport()
					return m, m.executeAuthorizedCmd()
				}
			case "n", "N":
				if m.awaitingConfirm {
					m.appendLine("→ 用户取消提交")
					m.phase = phaseDone
					m.refreshViewport()
					return m, nil
				}
			case "ctrl+c":
				return m, tea.Quit
			}
		}
		if m.streamContent.Len() > 0 || m.streamThinking.Len() > 0 || m.streamDone {
			m.refreshViewport()
			if !m.streamDone {
				m.contentViewport.GotoBottom()
			}
		}
		if m.streamDone {
			return m, cmd
		}
		return m, tea.Batch(cmd, m.spinner.Tick, func() tea.Msg { return <-m.streamChan })

	case phaseDone:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.Type == tea.KeyEnter || msg.Type == tea.KeyEscape || msg.String() == "q" || msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
		}
		return m, nil
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
		m.vpReady = true
	}

	vis := m.outputLog.String()

	if m.streamThinking.Len() > 0 {
		thinkingBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("241")).
			Padding(1, 2).
			Width(contentW).
			Render(renderMarkdown(m.streamThinking.String(), contentW-4))
		vis += "\n" + thinkingBox
	}

	if m.streamContent.Len() > 0 {
		contentBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(1, 2).
			Width(contentW).
			Render(renderMarkdown(m.streamContent.String(), contentW-4))
		vis += "\n" + contentBox
	}

	if m.phase == phaseStreaming && m.streamThinking.Len() == 0 && m.streamContent.Len() == 0 && m.outputLog.Len() == 0 {
		vis += "\n" + lipgloss.NewStyle().PaddingLeft(1).Render(m.spinner.View()+" AI 正在分析代码变更...")
	}

	if m.phase == phaseStreaming && m.awaitingConfirm {
		vis += "\n" + lipgloss.NewStyle().PaddingLeft(1).Bold(true).Foreground(lipgloss.Color("220")).Render("是否确认提交？(Y/N)")
	}

	if m.phase == phaseDone {
		if m.commitMessage != "" {
			vis += "\n" + successLineStyle.Render("✓ 提交成功")
			if m.commitHash != "" {
				vis += "\n" + toolCallLineStyle.Render(fmt.Sprintf("提交哈希: %s", m.commitHash))
			}
			msgBox := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("2")).
				Padding(1, 2).
				Width(contentW).
				Render(m.commitMessage)
			vis += "\n" + msgBox
		} else {
			vis += "\n" + errorLineStyle.Render("✗ 提交未完成")
		}
	}

	m.contentViewport.SetContent(vis)
}

func (m *CommitFlowModel) appendLine(line string) {
	m.outputLog.WriteString(line + "\n")
}

func (m *CommitFlowModel) appendErrorLine(line string) {
	m.outputLog.WriteString(errorLineStyle.Render(line) + "\n")
}

func (m *CommitFlowModel) appendToolCallLines(calls []ai.PendingToolCall) {
	for _, tc := range calls {
		switch tc.Name {
		case "read_file":
			path := tc.ArgString("path")
			m.appendLine(toolCallLineStyle.Render(fmt.Sprintf("📄 [read_file] %s → 读取中...", path)))
		case "list_tree":
			m.appendLine(toolCallLineStyle.Render("📁 [list_tree] → 获取项目树..."))
		case "git_log_recent":
			m.appendLine(toolCallLineStyle.Render("📋 [git_log_recent] → 获取历史记录..."))
		case "git_hook_check":
			m.appendLine(toolCallLineStyle.Render("🔧 [git_hook_check] → 检查 hook..."))
		case "git_config_get":
			key := tc.ArgString("key")
			m.appendLine(toolCallLineStyle.Render(fmt.Sprintf("⚙️  [git_config_get] %s → 查询中...", key)))
		default:
			m.appendLine(toolCallLineStyle.Render(fmt.Sprintf("🔧 [%s] → 执行中...", tc.Name)))
		}
	}
}

func (m *CommitFlowModel) startStageCmd(files []string) tea.Cmd {
	return func() tea.Msg {
		originalStaged := getStagedFiles()
		if err := diff.StageFiles(files); err != nil {
			return stageDoneMsg{success: false, err: err}
		}
		m.stagedFiles = files
		m.originalStaged = originalStaged
		return stageDoneMsg{success: true}
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
	sess, err := m.opts.Client.StartCommitSession(
		m.opts.DiffContent, m.opts.Desc, conventionInfo, 3,
	)
	if err != nil {
		return func() tea.Msg { return aiResponseMsg{err: err} }
	}
	m.session = sess
	m.streamThinking.Reset()
	m.streamContent.Reset()
	m.streamDone = false
	m.refreshViewport()

	m.streamChan = make(chan tea.Msg, 64)
	go func() {
		pending, streamErr := sess.StreamAI(func(chunk ai.StreamChunk) {
			m.streamChan <- streamChunkMsg{chunk: chunk}
		})
		if streamErr != nil {
			m.streamChan <- aiResponseMsg{err: streamErr}
			return
		}
		m.streamChan <- aiResponseMsg{pending: pending}
	}()

	return func() tea.Msg {
		return <-m.streamChan
	}
}

func (m *CommitFlowModel) executeAuthorizedCmd() tea.Cmd {
	m.streamThinking.Reset()
	m.streamContent.Reset()
	m.streamDone = false
	m.appendLine(m.spinner.View() + " 正在执行提交...")
	m.refreshViewport()

	m.streamChan = make(chan tea.Msg, 64)
	go func() {
		authorized := make([]bool, len(m.pendingCalls))
		for i := range authorized {
			authorized[i] = true
		}
		pending, err := m.session.ExecuteAndResumeWithStream(m.pendingCalls, authorized, func(chunk ai.StreamChunk) {
			m.streamChan <- streamChunkMsg{chunk: chunk}
		})
		if err != nil {
			m.streamChan <- execDoneMsg{err: err}
			return
		}
		m.streamChan <- execDoneMsg{pending: pending}
	}()

	return func() tea.Msg {
		return <-m.streamChan
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

func (m *CommitFlowModel) autoExecCmd() tea.Cmd {
	m.streamThinking.Reset()
	m.streamContent.Reset()
	m.streamDone = false
	m.appendLine(m.spinner.View() + " 正在执行工具...")
	m.refreshViewport()

	m.streamChan = make(chan tea.Msg, 64)
	go func() {
		authorized := make([]bool, len(m.pendingCalls))
		for i := range authorized {
			authorized[i] = true
		}
		pending, err := m.session.ExecuteAndResumeWithStream(m.pendingCalls, authorized, func(chunk ai.StreamChunk) {
			m.streamChan <- streamChunkMsg{chunk: chunk}
		})
		if err != nil {
			m.streamChan <- execDoneMsg{err: err}
			return
		}
		m.streamChan <- execDoneMsg{pending: pending}
	}()

	return func() tea.Msg {
		return <-m.streamChan
	}
}

func (m *CommitFlowModel) View() string {
	switch m.phase {
	case phaseSelectFiles, phaseDiffView:
		return m.fileSelector.View()

	case phaseStaging:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  暂存文件...")
		help := phaseHelpStyle.Width(m.termWidth).Render("  Ctrl+C 取消")
		content := lipgloss.NewStyle().Padding(2).Render(m.spinner.View() + " 正在暂存选中的文件...")
		return lipgloss.JoinVertical(lipgloss.Left, header, content, help)

	case phaseStreaming:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  AI 代码审查 & 提交")
		if m.opts.DiffMode != "" && m.opts.DiffMode != "完整 diff" {
			header = phaseHeaderStyle.Width(m.termWidth).Render(fmt.Sprintf("  AI 代码审查 & 提交（%s 模式）", m.opts.DiffMode))
		}

		return lipgloss.JoinVertical(lipgloss.Left, header, m.contentViewport.View())

	case phaseDone:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  完成")
		help := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Background(lipgloss.Color("235")).
			Padding(0, 2).
			Width(m.termWidth).
			Render("  按 Enter / Esc / q 退出")

		var tokenInfo string
		if m.session != nil {
			result := m.session.GetResult()
			if result.TotalTokens > 0 {
				tokenInfo = lipgloss.NewStyle().
					Foreground(lipgloss.Color("241")).
					Background(lipgloss.Color("235")).
					Padding(0, 2).
					Width(m.termWidth).
					Render(fmt.Sprintf("  Token 消耗: prompt=%d  completion=%d  total=%d", result.PromptTokens, result.CompletionTokens, result.TotalTokens))
			}
		}

		if tokenInfo != "" {
			return lipgloss.JoinVertical(lipgloss.Left, header, m.contentViewport.View(), tokenInfo, help)
		}
		return lipgloss.JoinVertical(lipgloss.Left, header, m.contentViewport.View(), help)
	}

	return ""
}

func (m *CommitFlowModel) GetResult() CommitFlowResult {
	result := m.session.GetResult()
	return CommitFlowResult{
		Success:          m.commitMessage != "",
		CommitMessage:    m.commitMessage,
		CommitHash:       m.commitHash,
		SelectedFiles:    m.selectedFiles,
		PromptTokens:     result.PromptTokens,
		CompletionTokens: result.CompletionTokens,
		TotalTokens:      result.TotalTokens,
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
}

func extractHash(results []ai.ToolCallResult) string {
	for _, tr := range results {
		if (tr.ToolName == "git_commit" || tr.ToolName == "git_commit_amend") && strings.Contains(tr.Result, "SUCCESS") {
			parts := strings.Fields(tr.Result)
			if len(parts) >= 4 {
				return parts[3]
			}
		}
	}
	return ""
}

func RunCommitFlow(files []diff.FileChange, opts CommitFlowOptions) (CommitFlowResult, error) {
	m := NewCommitFlowModel(files, opts)
	p := tea.NewProgram(m, tea.WithAltScreen())

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
