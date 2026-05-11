package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DonePanel displays the final commit result.
type DonePanel struct {
	commonPanel
	result    CommitFlowResult
	commitMsg string
	commitHash string
	isPartial bool
	remaining []string
	verified  bool
	tokenInfo string
	termWidth  int
	termHeight int
}

func NewDonePanel(result CommitFlowResult, commitMsg, commitHash string, isPartial bool, remaining []string, verified bool, tokenInfo string) *DonePanel {
	return &DonePanel{
		result:     result,
		commitMsg:  commitMsg,
		commitHash: commitHash,
		isPartial:  isPartial,
		remaining:  remaining,
		verified:   verified,
		tokenInfo:  tokenInfo,
	}
}

func (p *DonePanel) Init() tea.Cmd { return nil }

func (p *DonePanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.termWidth = msg.Width
		p.termHeight = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "enter", "esc":
			return p, tea.Quit
		case "ctrl+c":
			return p, tea.Quit
		}
	}
	return p, nil
}

func (p *DonePanel) View(width, height int) string {
	if width <= 0 {
		width = 80
	}
	contentW := width - 4

	var b strings.Builder

	if p.commitMsg != "" {
		// Success card
		successCard := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Th.Success).
			Padding(1, 2).
			Width(contentW - 4).
			Render(
				SuccessStyle().Render("✓ 提交成功") + "\n\n" +
					DimStyle().Render(fmt.Sprintf("提交哈希: %s", p.commitHash)) + "\n" +
					renderMarkdown(p.commitMsg, contentW-8),
			)
		b.WriteString("\n")
		b.WriteString(successCard)

		if p.isPartial {
			b.WriteString("\n\n")
			warningCard := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(Th.Warning).
				Padding(1, 2).
				Width(contentW - 4).
				Render(
					lipgloss.NewStyle().Foreground(Th.Warning).Bold(true).Render("⚠ 部分提交") + "\n" +
						DimStyle().Render("仍有文件未提交:") + "\n" +
						strings.Join(p.remaining, "\n"),
				)
			b.WriteString(warningCard)
		}
	} else {
		errCard := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Th.Error).
			Padding(1, 2).
			Width(contentW - 4).
			Render(ErrorStyle().Render("✗ 提交未完成"))
		b.WriteString("\n")
		b.WriteString(errCard)
	}

	return b.String()
}

func (p *DonePanel) Help() []HelpEntry {
	return []HelpEntry{
		{"Enter/Esc/q", "退出"},
	}
}
