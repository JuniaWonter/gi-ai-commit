package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type editorModel struct {
	textarea  textarea.Model
	err       error
	done      bool
	cancelled bool
	result    string
}

func newEditorModel(initialValue string) editorModel {
	ta := textarea.New()
	ta.SetValue(initialValue)
	ta.Placeholder = "输入 commit message..."
	ta.Focus()
	ta.CharLimit = 500
	ta.SetWidth(80)
	ta.SetHeight(10)

	ta.FocusedStyle.CursorLine = lipgloss.NewStyle().
		Background(Th.SurfaceAlt)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().
		Foreground(Th.DimText)
	ta.FocusedStyle.Text = lipgloss.NewStyle().
		Foreground(Th.Text)

	return editorModel{
		textarea: ta,
	}
}

func (m editorModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m editorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.cancelled = true
			m.done = true
			return m, tea.Quit
		case tea.KeyEsc:
			m.cancelled = true
			m.done = true
			return m, tea.Quit
		case tea.KeyCtrlS:
			m.result = strings.TrimSpace(m.textarea.Value())
			m.done = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m editorModel) View() string {
	if m.done {
		return ""
	}

	title := PrimaryStyle().Render("编辑 commit message")
	help := lipgloss.NewStyle().
		Foreground(Th.DimText).
		Render("Ctrl+S 保存 │ Esc/Ctrl+C 取消 │ 支持多行输入")

	return lipgloss.JoinVertical(lipgloss.Left,
		"\n"+title+"\n",
		m.textarea.View(),
		"\n"+help,
	)
}

func EditCommitMessage(initialValue string) (string, error) {
	if initialValue == "" {
		initialValue = "feat: "
	}

	m := newEditorModel(initialValue)
	p := tea.NewProgram(m)

	model, err := p.Run()
	if err != nil {
		return "", err
	}

	m, ok := model.(editorModel)
	if !ok {
		return "", nil
	}

	if m.cancelled {
		return "", nil
	}

	return m.result, nil
}
