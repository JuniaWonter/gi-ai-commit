package tui

import (
	"fmt"
	"os/exec"
	"strings"

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
	phaseConfirm
	phaseEdit
	phaseCommitting
	phaseResult
	phaseError
)

type generateResultMsg struct {
	commitMessage string
	toolResults   []ai.ToolCallResult
	err           error
}

type commitDoneMsg struct {
	success bool
	hash    string
	err     error
}

type amendDoneMsg struct {
	success bool
	hash    string
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
	phase          phase
	opts           CommitFlowOptions
	files          []diff.FileChange
	fileSelector   *FileSelector
	viewport       viewport.Model
	textarea       textarea.Model
	spinner        spinner.Model
	termWidth      int
	termHeight     int
	ready          bool

	commitMessage  string
	toolResults    []ai.ToolCallResult
	errorMsg       string
	commitHash     string
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
	return tea.Batch(m.fileSelector.Init(), m.spinner.Tick)
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
		return m, nil

	case generateResultMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.errorMsg = msg.err.Error()
			m.toolResults = msg.toolResults
			return m, nil
		}
		m.phase = phaseConfirm
		m.commitMessage = msg.commitMessage
		m.toolResults = msg.toolResults
		if m.opts.AutoConfirm {
			m.phase = phaseCommitting
			return m, m.startCommitCmd()
		}
		return m, nil

	case commitDoneMsg:
		if msg.success {
			m.phase = phaseResult
			m.commitHash = msg.hash
			return m, nil
		}
		m.phase = phaseError
		m.errorMsg = msg.err.Error()
		return m, nil

	case amendDoneMsg:
		if msg.success {
			m.commitHash = msg.hash
			m.phase = phaseResult
			return m, nil
		}
		m.phase = phaseError
		m.errorMsg = msg.err.Error()
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
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, tea.Batch(cmd, m.spinner.Tick)

	case phaseGenerating:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, tea.Batch(cmd, m.spinner.Tick)

	case phaseConfirm:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "y", "enter":
				m.phase = phaseResult
				return m, nil
			case "n":
				m.phase = phaseCommitting
				return m, m.startResetCmd()
			case "e":
				m.phase = phaseEdit
				m.textarea.SetValue(m.commitMessage)
				m.textarea.Focus()
				return m, textarea.Blink
			case "ctrl+c", "q":
				m.phase = phaseError
				m.errorMsg = "用户取消操作"
				return m, tea.Quit
			}
		}

	case phaseEdit:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.Type {
			case tea.KeyCtrlC, tea.KeyEsc:
				m.phase = phaseConfirm
				return m, nil
			case tea.KeyCtrlS:
				newMsg := strings.TrimSpace(m.textarea.Value())
				if newMsg != "" {
					m.commitMessage = newMsg
				}
				m.phase = phaseConfirm
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd

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
		commitMessage, toolResults, err := m.opts.Client.CommitWithRetry(
			m.opts.DiffContent, m.opts.Desc, conventionInfo, 3,
		)
		return generateResultMsg{
			commitMessage: commitMessage,
			toolResults:   toolResults,
			err:           err,
		}
	}
}

func (m *CommitFlowModel) startCommitCmd() tea.Cmd {
	return func() tea.Msg {
		if m.opts.DryRun {
			return commitDoneMsg{success: true}
		}
		result := git.Commit(m.commitMessage)
		if result.Success {
			return commitDoneMsg{success: true, hash: result.Hash}
		}
		return commitDoneMsg{success: false, err: fmt.Errorf("commit failed: %s", result.Stderr)}
	}
}

func (m *CommitFlowModel) startResetCmd() tea.Cmd {
	return func() tea.Msg {
		if m.commitHash != "" {
			result := git.ResetLastCommit()
			if !result.Success {
				return resetDoneMsg{success: false, err: fmt.Errorf("reset failed: %s", result.Error)}
			}
		}
		if len(m.originalStaged) > 0 {
			diff.StageFiles(m.originalStaged)
		}
		return resetDoneMsg{success: true}
	}
}

func resetFiles(files []string) {
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
		content := lipgloss.NewStyle().Padding(2).Render(m.spinner.View() + " 正在调用 AI 分析变更并生成提交...")
		return lipgloss.JoinVertical(lipgloss.Left, header, content, help)

	case phaseConfirm:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  确认 commit message")
		help := phaseHelpStyle.Width(m.termWidth).Render("  Y/Enter 确认 │ e 编辑 │ n 取消(回滚) │ Ctrl+C 退出")

		msgBox := commitMsgBoxStyle.Width(min(m.termWidth-4, 80)).Render(m.commitMessage)

		var toolLog string
		if len(m.toolResults) > 0 {
			var b strings.Builder
			b.WriteString("\n  工具调用记录：\n")
			for _, tr := range m.toolResults {
				icon := "→"
				if strings.Contains(tr.Result, "SUCCESS") {
					icon = "✓"
				} else if strings.Contains(tr.Result, "FAILED") {
					icon = "✗"
				}
				b.WriteString(fmt.Sprintf("  %s %s\n", icon, truncateResult(tr.ToolName+" → "+tr.Result, 100)))
			}
			toolLog = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(b.String())
		}

		content := lipgloss.NewStyle().Padding(2).Render(
			"AI 生成的 commit message：\n\n" + msgBox + toolLog)
		return lipgloss.JoinVertical(lipgloss.Left, header, content, help)

	case phaseEdit:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  编辑 commit message")
		help := phaseHelpStyle.Width(m.termWidth).Render("  Ctrl+S 保存 │ Esc 取消编辑 │ Ctrl+C 退出")
		content := lipgloss.NewStyle().Padding(2).Render(m.textarea.View())
		return lipgloss.JoinVertical(lipgloss.Left, header, content, help)

	case phaseCommitting:
		header := phaseHeaderStyle.Width(m.termWidth).Render("  提交中...")
		help := phaseHelpStyle.Width(m.termWidth).Render("  Ctrl+C 取消")
		content := lipgloss.NewStyle().Padding(2).Render(m.spinner.View() + " 正在执行 git commit...")
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

		var toolLog string
		if len(m.toolResults) > 0 {
			var b strings.Builder
			b.WriteString("\n  工具调用记录：\n")
			for _, tr := range m.toolResults {
				icon := "→"
				if strings.Contains(tr.Result, "SUCCESS") {
					icon = "✓"
				} else if strings.Contains(tr.Result, "FAILED") {
					icon = "✗"
				}
				b.WriteString(fmt.Sprintf("  %s %s\n", icon, truncateResult(tr.ToolName+" → "+tr.Result, 150)))
			}
			toolLog = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(b.String())
		}

		content := lipgloss.NewStyle().Padding(2).Render(
			errorStyle.Render(m.errorMsg) + toolLog)
		return lipgloss.JoinVertical(lipgloss.Left, header, content, help)
	}

	return ""
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

func truncateResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
