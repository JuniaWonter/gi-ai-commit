package tui

import (
	"encoding/json"
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
	phaseGenerating
	phaseAuthorize
	phaseExecuting
	phaseResult
	phaseError
)

type aiResponseMsg struct {
	pending []ai.PendingToolCall
	err     error
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

	commitMsgBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(1, 2)

	toastStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("220")).
			Padding(1, 2).
			Background(lipgloss.Color("236"))

	toastTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("220"))

	toastToolNameStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("6"))

	toastArgsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1"))

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("63"))
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
	viewport     viewport.Model
	textarea     textarea.Model
	spinner      spinner.Model
	termWidth    int
	termHeight   int
	ready        bool

	session        *ai.CommitSession
	pendingCalls   []ai.PendingToolCall
	commitMessage  string
	commitHash     string
	errorMsg       string
	selectedFiles  []string
	stagedFiles    []string
	originalStaged []string
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
			m.viewport = viewport.New(msg.Width, msg.Height-4)
			m.viewport.Style = lipgloss.NewStyle()
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - 4
		}
		if m.phase == phaseSelectFiles || m.phase == phaseDiffView {
			fsModel, cmd := m.fileSelector.Update(msg)
			m.fileSelector = fsModel.(*FileSelector)
			return m, cmd
		}
		return m, nil

	case signalMsg:
		m.phase = phaseError
		m.errorMsg = "用户取消操作"
		return m, tea.Quit

	case aiResponseMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.errorMsg = msg.err.Error()
			return m, nil
		}
		if msg.pending == nil || len(msg.pending) == 0 {
			result := m.session.GetResult()
			if result.Success {
				m.phase = phaseResult
				m.commitMessage = result.CommitMsg
				m.commitHash = extractHash(result.ToolResults)
			} else {
				m.phase = phaseError
				m.errorMsg = result.Error.Error()
			}
			return m, nil
		}
		m.pendingCalls = msg.pending
		m.phase = phaseAuthorize
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.errorMsg = msg.err.Error()
			return m, nil
		}
		if msg.pending == nil || len(msg.pending) == 0 {
			result := m.session.GetResult()
			if result.Success {
				m.phase = phaseResult
				m.commitMessage = result.CommitMsg
				m.commitHash = extractHash(result.ToolResults)
			} else {
				m.phase = phaseError
				m.errorMsg = result.Error.Error()
			}
			return m, nil
		}
		m.pendingCalls = msg.pending
		m.phase = phaseAuthorize
		return m, nil

	case stageDoneMsg:
		if msg.success {
			m.phase = phaseGenerating
			return m, tea.Batch(m.spinner.Tick, m.startGenerateCmd())
		}
		m.phase = phaseError
		m.errorMsg = fmt.Sprintf("暂存文件失败：%v", msg.err)
		return m, nil

	case resetDoneMsg:
		m.phase = phaseError
		m.errorMsg = "用户取消提交，已恢复暂存状态"
		return m, nil
	}

	switch m.phase {
	case phaseSelectFiles:
		fsModel, cmd := m.fileSelector.Update(msg)
		m.fileSelector = fsModel.(*FileSelector)
		if m.fileSelector.done {
			selected := m.fileSelector.GetSelectedFiles()
			if m.fileSelector.quitting || len(selected) == 0 {
				m.phase = phaseError
				if m.fileSelector.quitting {
					m.errorMsg = "用户取消操作"
				} else {
					m.errorMsg = "未选择任何文件"
				}
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
				m.phase = phaseError
				m.errorMsg = "用户取消操作"
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
				m.phase = phaseError
				m.errorMsg = "用户取消操作"
				return m, tea.Quit
			}
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, tea.Batch(cmd, m.spinner.Tick)

	case phaseGenerating:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.Type == tea.KeyCtrlC {
				m.phase = phaseError
				m.errorMsg = "用户取消操作"
				return m, tea.Quit
			}
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, tea.Batch(cmd, m.spinner.Tick)

	case phaseAuthorize:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			s := msg.String()
			switch s {
			case "y", "Y", "enter":
				m.phase = phaseExecuting
				return m, m.executeAuthorizedCmd()
			case "n", "N":
				m.phase = phaseError
				m.errorMsg = "用户拒绝工具调用，已取消"
				return m, nil
			case "ctrl+c":
				m.phase = phaseError
				m.errorMsg = "用户取消操作"
				return m, nil
			}
		}

	case phaseExecuting:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.Type == tea.KeyCtrlC {
				m.phase = phaseError
				m.errorMsg = "用户取消操作"
				return m, tea.Quit
			}
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, tea.Batch(cmd, m.spinner.Tick)

	case phaseResult, phaseError:
		switch msg.(type) {
		case tea.KeyMsg:
			return m, tea.Quit
		}
	}

	return m, nil
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
	return func() tea.Msg {
		conventionInfo := git.DetectConventions()
		sess, pending, err := m.opts.Client.StartCommitSession(
			m.opts.DiffContent, m.opts.Desc, conventionInfo, 3,
		)
		if err != nil {
			return aiResponseMsg{err: err}
		}
		m.session = sess
		return aiResponseMsg{pending: pending}
	}
}

func (m *CommitFlowModel) executeAuthorizedCmd() tea.Cmd {
	return func() tea.Msg {
		authorized := make([]bool, len(m.pendingCalls))
		for i := range authorized {
			authorized[i] = true
		}
		pending, err := m.session.ExecuteAndResume(authorized)
		return execDoneMsg{pending: pending, err: err}
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

	case phaseGenerating:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  生成 commit message...")
		if m.opts.DiffMode != "" && m.opts.DiffMode != "完整 diff" {
			header = phaseHeaderStyle.Width(m.termWidth).Render(fmt.Sprintf("  生成 commit message...（%s 模式）", m.opts.DiffMode))
		}
		help := phaseHelpStyle.Width(m.termWidth).Render("  Ctrl+C 取消")
		content := lipgloss.NewStyle().Padding(2).Render(m.spinner.View() + " 正在调用 AI 分析变更...")
		return lipgloss.JoinVertical(lipgloss.Left, header, content, help)

	case phaseAuthorize:
		return m.renderAuthorizeView()

	case phaseExecuting:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  执行工具调用...")
		help := phaseHelpStyle.Width(m.termWidth).Render("  Ctrl+C 取消")
		content := lipgloss.NewStyle().Padding(2).Render(m.spinner.View() + " 正在执行...")
		return lipgloss.JoinVertical(lipgloss.Left, header, content, help)

	case phaseResult:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  提交成功!")
		msgBox := commitMsgBoxStyle.Width(min(m.termWidth-4, 80)).Render(m.commitMessage)
		hashInfo := ""
		if m.commitHash != "" {
			hashInfo = successStyle.Render(fmt.Sprintf("\n  提交哈希：%s", m.commitHash))
		}
		help := phaseHelpStyle.Width(m.termWidth).Render("  任意键退出")
		content := lipgloss.NewStyle().Padding(2).Render(msgBox + hashInfo)
		return lipgloss.JoinVertical(lipgloss.Left, header, content, help)

	case phaseError:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  错误")
		help := phaseHelpStyle.Width(m.termWidth).Render("  任意键退出")
		content := lipgloss.NewStyle().Padding(2).Render(errorStyle.Render(m.errorMsg))
		return lipgloss.JoinVertical(lipgloss.Left, header, content, help)
	}

	return ""
}

func (m *CommitFlowModel) renderAuthorizeView() string {
	w := m.termWidth
	if w == 0 {
		w = 80
	}
	maxW := min(w-4, 90)

	var items []string
	for i, tc := range m.pendingCalls {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("  %d. ", i+1))
		b.WriteString(toastToolNameStyle.Render(tc.Name))
		b.WriteString("\n")

		if tc.Name == "git_commit" || tc.Name == "git_commit_amend" {
			msg := tc.ArgString("message")
			b.WriteString(toastArgsStyle.Render("     message: ") + msg + "\n")
		} else if len(tc.Args) > 0 {
			argsJSON, _ := json.MarshalIndent(tc.Args, "     ", "  ")
			b.WriteString(toastArgsStyle.Render(string(argsJSON)) + "\n")
		}
		items = append(items, b.String())
	}

	if len(items) == 0 {
		items = append(items, "  (无工具调用)")
	}

	toastContent := strings.Join(items, "\n")

	toastTitle := toastTitleStyle.Render("  AI 请求执行工具调用")
	toastBody := toastStyle.Width(maxW).Render(toastContent)

	help := phaseHelpStyle.Width(w).Render("  Y/Enter 授权执行 │ N 拒绝并取消 │ Ctrl+C 退出")

	return lipgloss.JoinVertical(lipgloss.Left, toastTitle, "\n"+toastBody+"\n", help)
}

func (m *CommitFlowModel) GetResult() CommitFlowResult {
	return CommitFlowResult{
		Success:       m.phase == phaseResult,
		CommitMessage: m.commitMessage,
		CommitHash:    m.commitHash,
		Error:         m.errorMsg,
		SelectedFiles: m.selectedFiles,
	}
}

type CommitFlowResult struct {
	Success       bool
	CommitMessage string
	CommitHash    string
	Error         string
	SelectedFiles []string
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
