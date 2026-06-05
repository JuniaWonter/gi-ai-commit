package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type AskUserOption struct {
	Label       string
	Description string
}

type AskUserResult struct {
	Selected string
	Custom   string
}

type AskUserPanel struct {
	question     string
	options      []AskUserOption
	allowCustom  bool
	cursor       int
	customInput  textinput.Model
	useCustom    bool
	width        int
	height       int
	done         bool
	result       AskUserResult
}

func NewAskUserPanel(question string, options []AskUserOption, allowCustom bool) *AskUserPanel {
	ti := textinput.New()
	ti.Placeholder = "输入自定义答案..."
	ti.CharLimit = 200
	ti.Width = 50

	return &AskUserPanel{
		question:    question,
		options:     options,
		allowCustom: allowCustom,
		cursor:      0,
		customInput: ti,
		useCustom:   false,
	}
}

func (p *AskUserPanel) Init() tea.Cmd {
	return nil
}

func (p *AskUserPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	if p.done {
		return p, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if p.useCustom {
			switch msg.String() {
			case "enter":
				p.result = AskUserResult{Custom: p.customInput.Value()}
				p.done = true
				return p, func() tea.Msg { return askUserDoneMsg{result: p.result} }
			case "esc":
				p.useCustom = false
				p.customInput.Blur()
				return p, nil
			case "ctrl+c":
				return p, tea.Quit
			default:
				var cmd tea.Cmd
				p.customInput, cmd = p.customInput.Update(msg)
				return p, cmd
			}
		}

		switch msg.String() {
		case "up", "k":
			if p.cursor > 0 {
				p.cursor--
			}
		case "down", "j":
			maxCursor := len(p.options) - 1
			if p.allowCustom {
				maxCursor++
			}
			if p.cursor < maxCursor {
				p.cursor++
			}
		case "enter":
			if p.cursor == len(p.options) && p.allowCustom {
				p.useCustom = true
				p.customInput.Focus()
				return p, p.customInput.Focus()
			}
			if p.cursor < len(p.options) {
				p.result = AskUserResult{Selected: p.options[p.cursor].Label}
				p.done = true
				return p, func() tea.Msg { return askUserDoneMsg{result: p.result} }
			}
		case "ctrl+c":
			return p, tea.Quit
		}
	}

	return p, nil
}

func (p *AskUserPanel) View(width, height int) string {
	p.width = width
	p.height = height

	if width <= 0 {
		width = 80
	}

	var b strings.Builder

	questionStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(Th.Primary).
		MarginBottom(1)

	b.WriteString(questionStyle.Render("? " + p.question))
	b.WriteString("\n\n")

	for i, opt := range p.options {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(Th.Text)
		if i == p.cursor && !p.useCustom {
			cursor = "▸ "
			style = style.Foreground(Th.Primary).Bold(true)
		}

		line := cursor + opt.Label
		if opt.Description != "" {
			line += " - " + DimStyle().Render(opt.Description)
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	if p.allowCustom {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(Th.Text)
		if p.cursor == len(p.options) || p.useCustom {
			cursor = "▸ "
			style = style.Foreground(Th.Primary).Bold(true)
		}

		if p.useCustom {
			b.WriteString(style.Render(cursor + "自定义: "))
			b.WriteString(p.customInput.View())
		} else {
			b.WriteString(style.Render(cursor + "输入自定义答案..."))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	helpText := "↑/↓ 选择  Enter 确认"
	if p.useCustom {
		helpText = "Enter 提交  Esc 返回"
	}
	b.WriteString(DimStyle().Render(helpText))

	dialogW := 70
	if width < dialogW+4 {
		dialogW = width - 4
	}
	if dialogW < 40 {
		dialogW = 40
	}

	dialog := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Th.Primary).
		Padding(1, 2).
		Width(dialogW - 4).
		Render(b.String())

	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		dialog,
	)
}

func (p *AskUserPanel) SetViewportSize(w, h int) {
	p.width = w
	p.height = h
}

func (p *AskUserPanel) Help() []HelpEntry {
	if p.useCustom {
		return []HelpEntry{
			{Key: "enter", Desc: "提交"},
			{Key: "esc", Desc: "返回"},
		}
	}
	entries := []HelpEntry{
		{Key: "↑/↓", Desc: "选择"},
		{Key: "enter", Desc: "确认"},
	}
	return entries
}

type askUserDoneMsg struct {
	result AskUserResult
}
