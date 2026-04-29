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
)

type CommitFlowOptions struct {
	AutoConfirm bool
	DryRun      bool
	DescFunc    func() string // lazy description, blocks until ready
	DiffCfg     diff.DiffPromptConfig
	GitRoot     string
	Client      *ai.Client
}

type CommitFlowModel struct {
	phase        phase
	opts         CommitFlowOptions
	files        []diff.FileChange
	fileSelector *FileSelector
	spinner      spinner.Model
	termWidth    int
	termHeight   int
	ready        bool
	stageInProgress bool

	session        *ai.CommitSession
	pendingCalls   []ai.PendingToolCall
	commitMessage  string
	commitHash     string
	selectedFiles  []string
	stagedFiles    []string
	originalStaged []string
	streamChan     chan tea.Msg
	streamDoneCh   chan struct{} // signals goroutines to exit
	streamDoneOnce sync.Once    // prevents double-close on streamDoneCh

	stagedDiffContent string // diff of staged files, set after startStageCmd
	stagedDiffMode    string

	streamThinking strings.Builder
	streamContent  strings.Builder
	streamDone     bool

	outputLog       strings.Builder
	contentViewport viewport.Model
	vpReady         bool
	awaitingConfirm  bool
	userAuthorizedCommit bool
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
			}
			m.phase = phaseDone
			m.refreshViewport()
			return m, nil
		}
		m.pendingCalls = msg.pending
		if m.hasCommitCall() && !m.opts.AutoConfirm {
			m.awaitingConfirm = true
			m.refreshViewport()
			return m, nil
		}
		m.appendToolCallLines(msg.pending)
		m.refreshViewport()
		return m, m.execPendingCmd()

	case stageDoneMsg:
		m.stageInProgress = false
		// 防止重复处理
		if m.phase == phaseDone || m.phase == phaseStreaming {
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
					m.awaitingConfirm = false
					m.userAuthorizedCommit = true
					m.appendLine("→ 已确认提交，执行中...")
					m.refreshViewport()
					return m, m.execPendingCmd()
				}
			case "y", "Y":
				if m.awaitingConfirm {
					m.awaitingConfirm = false
					m.userAuthorizedCommit = true
					m.appendLine("→ 确认提交，执行中...")
					m.refreshViewport()
					return m, m.execPendingCmd()
				}
			case "n", "N":
				if m.awaitingConfirm {
					m.appendLine("→ 用户取消提交")
					m.phase = phaseDone
					m.refreshViewport()
					return m, nil
				}
			case "esc":
				if m.awaitingConfirm {
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
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.Type == tea.KeyEnter || msg.Type == tea.KeyEscape || msg.String() == "q" || msg.String() == "ctrl+c" {
				m.closeStreamDone()
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
		vis += "\n" + lipgloss.NewStyle().PaddingLeft(1).Bold(true).Foreground(lipgloss.Color("220")).Render("➜ 等待确认: 按 Y/Enter 确认提交 / N 取消 / Esc 退出")
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
	// 使用 stage 后的实际 diff，确保 AI 分析的内容与提交完全一致
	sess, err := m.opts.Client.StartCommitSession(
		m.stagedDiffContent, m.opts.DescFunc(), conventionInfo, 3, m.selectedFiles,
	)
	if err != nil {
		debug.Logf("startGenerateCmd StartCommitSession failed err=%v", err)
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

	label := "正在执行工具..."
	if m.hasCommitCall() {
		label = "正在执行提交..."
	}
	m.appendLine(m.spinner.View() + " " + label)
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
