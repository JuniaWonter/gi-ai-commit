package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oliver/git-ai-commit/internal/ai"
)

// DonePanel displays the final commit result.
type DonePanel struct {
	commonPanel
	result       CommitFlowResult
	commitMsg    string
	commitHash   string
	isPartial    bool
	remaining    []string
	verified     bool
	tokenInfo    string
	reviewResult *ai.ReviewResult
	termWidth    int
	termHeight   int
	viewport     viewport.Model
	vpReady      bool
}

func NewDonePanel(result CommitFlowResult, commitMsg, commitHash string, isPartial bool, remaining []string, verified bool, tokenInfo string, reviewResult *ai.ReviewResult) *DonePanel {
	return &DonePanel{
		result:       result,
		commitMsg:    commitMsg,
		commitHash:   commitHash,
		isPartial:    isPartial,
		remaining:    remaining,
		verified:     verified,
		tokenInfo:    tokenInfo,
		reviewResult: reviewResult,
	}
}

func (p *DonePanel) Init() tea.Cmd { return nil }

func (p *DonePanel) SetViewportSize(width, height int) {
	if width <= 0 {
		width = 80
	}
	contentH := height - 4
	if contentH < 1 {
		contentH = 1
	}
	if !p.vpReady {
		p.viewport = viewport.New(width, contentH)
		p.viewport.Style = lipgloss.NewStyle().Padding(0, 1)
		p.viewport.MouseWheelEnabled = true
		p.vpReady = true
	} else {
		p.viewport.Width = width
		p.viewport.Height = contentH
	}
	p.termWidth = width
	p.termHeight = height
}

func (p *DonePanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.termWidth = msg.Width
		p.termHeight = msg.Height
		p.SetViewportSize(msg.Width, msg.Height)
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "enter", "esc":
			return p, tea.Quit
		case "ctrl+c":
			return p, tea.Quit
		default:
			// Forward other keys to viewport for scrolling
			if p.vpReady {
				var cmd tea.Cmd
				p.viewport, cmd = p.viewport.Update(msg)
				return p, cmd
			}
		}
	case tea.MouseMsg:
		// Forward mouse events to viewport for scrolling
		if p.vpReady {
			var cmd tea.Cmd
			p.viewport, cmd = p.viewport.Update(msg)
			return p, cmd
		}
	}
	return p, nil
}

func (p *DonePanel) View(width, height int) string {
	if width <= 0 {
		width = 80
	}
	contentW := width - 4

	// Initialize viewport if needed
	p.SetViewportSize(width, height)

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

		// Review result card
		if p.reviewResult != nil {
			b.WriteString("\n\n")
			b.WriteString(p.renderReviewCard(contentW))
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

	// Set content and return viewport view
	p.viewport.SetContent(b.String())
	return p.viewport.View()
}

func (p *DonePanel) renderReviewCard(contentW int) string {
	rr := p.reviewResult

	// Determine card color based on recommendation
	var borderColor lipgloss.Color
	var titleIcon string
	switch rr.Recommendation {
	case "approve":
		borderColor = Th.Success
		titleIcon = "✓"
	case "approve_with_warnings":
		borderColor = Th.Warning
		titleIcon = "⚠"
	case "request_changes":
		borderColor = Th.Error
		titleIcon = "✗"
	default:
		borderColor = Th.DimText
		titleIcon = "•"
	}

	var content strings.Builder

	// Summary
	content.WriteString(DimStyle().Render("变更摘要: ") + rr.Summary + "\n\n")

	// Simple change indicator
	if rr.IsSimple {
		content.WriteString(DimStyle().Render("审查模式: ") + "简单变更（跳过详细审查）\n\n")
	}

	// Recommendation
	recText := map[string]string{
		"approve":               "通过 - 无风险可提交",
		"approve_with_warnings": "通过（有警告）- 可提交但建议关注",
		"request_changes":       "需修改 - 有严重问题",
	}
	recLabel := recText[rr.Recommendation]
	if recLabel == "" {
		recLabel = rr.Recommendation
	}
	content.WriteString(DimStyle().Render("审查建议: ") + titleIcon + " " + recLabel + "\n")

	// Breaking changes
	if rr.BreakingChanges {
		content.WriteString(lipgloss.NewStyle().Foreground(Th.Error).Bold(true).Render("⚠ 包含破坏性变更") + "\n")
	}

	// Highlights
	if len(rr.Highlights) > 0 {
		content.WriteString("\n" + DimStyle().Render("亮点:") + "\n")
		for _, h := range rr.Highlights {
			content.WriteString("  • " + h + "\n")
		}
	}

	// Risks
	if len(rr.Risks) > 0 {
		content.WriteString("\n" + DimStyle().Render("风险项:") + "\n")
		for i, r := range rr.Risks {
			sevColor := Th.DimText
			switch r.Severity {
			case "critical":
				sevColor = Th.Error
			case "high":
				sevColor = Th.Error
			case "medium":
				sevColor = Th.Warning
			case "low":
				sevColor = Th.Success
			}

			sevLabel := lipgloss.NewStyle().Foreground(sevColor).Bold(true).Render(fmt.Sprintf("[%s]", r.Severity))
			catLabel := DimStyle().Render(fmt.Sprintf("[%s]", r.Category))

			content.WriteString(fmt.Sprintf("  %d. %s %s ", i+1, sevLabel, catLabel))
			if r.File != "" {
				content.WriteString(DimStyle().Render(r.File))
				if r.Line > 0 {
					content.WriteString(DimStyle().Render(fmt.Sprintf(":%d", r.Line)))
				}
				content.WriteString(" ")
			}
			content.WriteString(r.Description + "\n")
			if r.Suggestion != "" {
				content.WriteString("     " + DimStyle().Render("→ "+r.Suggestion) + "\n")
			}
		}
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(contentW - 4).
		Render(content.String())
}

func (p *DonePanel) Help() []HelpEntry {
	return []HelpEntry{
		{"Enter/Esc/q", "退出"},
	}
}
