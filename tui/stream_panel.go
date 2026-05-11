package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oliver/git-ai-commit/internal/ai"
	"github.com/oliver/git-ai-commit/internal/debug"
)

// StreamingPanel displays AI streaming output, tool calls, and review content.
type StreamingPanel struct {
	commonPanel
	outputLog          strings.Builder
	reviewOutput       strings.Builder
	streamThinking     strings.Builder
	streamContent      strings.Builder
	toolNames          []string
	viewport           viewport.Model
	vpReady            bool
	streamDone         bool
	awaitingConfirm    bool
	isSummarizeConfirm bool
	authorizedCommit   bool
	done               bool
	result             CommitFlowResult
	diffMode           string
	termWidth          int
	termHeight         int
}

func NewStreamingPanel(diffMode string) *StreamingPanel {
	p := &StreamingPanel{diffMode: diffMode}
	p.commonPanel = newCommonPanel()
	return p
}

// SetViewportSize initializes or resizes the viewport.
// Called by the parent when creating the panel or on window resize.
func (p *StreamingPanel) SetViewportSize(width, height int) {
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

func (p *StreamingPanel) Init() tea.Cmd {
	return p.SpinnerCmd()
}

func (p *StreamingPanel) Help() []HelpEntry {
	if p.awaitingConfirm {
		return []HelpEntry{
			{"Y/Enter", "确认"},
			{"N/Esc", "取消"},
		}
	}
	return []HelpEntry{
		{"↑↓", "滚动"},
		{"Ctrl+C", "退出"},
	}
}

// Reset clears all streaming state for a new round of tools.
func (p *StreamingPanel) Reset() {
	p.streamThinking.Reset()
	p.streamContent.Reset()
	p.streamDone = false
	p.toolNames = nil
}

// AppendOutput adds a line to the output log.
func (p *StreamingPanel) AppendOutput(line string) {
	p.outputLog.WriteString(line + "\n")
	p.trimOutputLog()
}

func (p *StreamingPanel) AppendError(line string) {
	p.outputLog.WriteString(ErrorStyle().Render(line) + "\n")
	p.trimOutputLog()
}

func (p *StreamingPanel) AppendToolCall(calls []ai.PendingToolCall) {
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
		p.toolNames = append(p.toolNames, label)
	}
}

func (p *StreamingPanel) SetToolsCompleted() {
	if len(p.toolNames) == 0 {
		return
	}
	for _, name := range p.toolNames {
		p.reviewOutput.WriteString(SuccessStyle().Render("✓") + " │ " + name + "\n")
	}
	p.toolNames = nil
	p.trimReviewOutput()
}

func (p *StreamingPanel) SetAwaitingConfirm(yes bool, isSummarize bool) {
	p.awaitingConfirm = yes
	p.isSummarizeConfirm = isSummarize
}

func (p *StreamingPanel) AuthorizedCommit() {
	p.authorizedCommit = true
}

func (p *StreamingPanel) FlushStream() {
	if p.streamThinking.Len() > 0 {
		p.reviewOutput.WriteString(p.streamThinking.String() + "\n")
		p.streamThinking.Reset()
	}
	if p.streamContent.Len() > 0 {
		p.reviewOutput.WriteString(p.streamContent.String() + "\n")
		p.streamContent.Reset()
	}
	p.trimReviewOutput()
}

func (p *StreamingPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.SetViewportSize(msg.Width, msg.Height-2)
		return p, nil

	case tea.KeyMsg:
		if !p.awaitingConfirm {
			var cmd tea.Cmd
			p.viewport, cmd = p.viewport.Update(msg)
			return p, cmd
		}
		return p, nil
	}

	// spinner.Tick and other messages
	var cmd tea.Cmd
	p.spinner, cmd = p.spinner.Update(msg)
	return p, cmd
}

func (p *StreamingPanel) View(width, height int) string {
	if width <= 0 {
		width = 80
	}
	contentH := height - 4
	if contentH < 1 {
		contentH = 1
	}
	contentW := width - 4

	if !p.vpReady {
		return lipgloss.NewStyle().PaddingLeft(1).Render(p.spinner.View() + " AI 正在分析代码变更...")
	}

	var vis strings.Builder

	// Output log (tool calls, status)
	if p.outputLog.Len() > 0 {
		vis.WriteString(lipgloss.NewStyle().Width(contentW).Render(p.outputLog.String()))
	}

	// Review output (flushed AI content, markdown rendered)
	if p.reviewOutput.Len() > 0 {
		if vis.Len() > 0 {
			vis.WriteString("\n")
		}
		vis.WriteString(renderMarkdown(p.reviewOutput.String(), contentW))
	}

	// Current tool names with spinner
	for _, name := range p.toolNames {
		if vis.Len() > 0 || p.reviewOutput.Len() > 0 {
			vis.WriteString("\n")
		}
		spinnerView := p.spinner.View()
		vis.WriteString(DimStyle().PaddingLeft(1).Render(fmt.Sprintf("%s │ %s", spinnerView, name)))
	}

	// Thinking
	if p.streamThinking.Len() > 0 {
		if vis.Len() > 0 {
			vis.WriteString("\n")
		}
		thinkingText := renderMarkdown(p.streamThinking.String(), contentW)
		vis.WriteString(lipgloss.NewStyle().
			Foreground(Th.DimText).
			PaddingLeft(1).
			Render(thinkingText))
	}

	// Content
	if p.streamContent.Len() > 0 {
		if vis.Len() > 0 {
			vis.WriteString("\n")
		}
		vis.WriteString(renderMarkdown(p.streamContent.String(), contentW))
	}

	// Initial spinner
	if vis.Len() == 0 {
		vis.WriteString("\n" + lipgloss.NewStyle().PaddingLeft(1).Render(p.spinner.View()+" AI 正在分析代码变更..."))
	}

	p.viewport.Width = width
	p.viewport.Height = contentH
	p.viewport.SetContent(vis.String())

	return p.viewport.View()
}

func (p *StreamingPanel) trimOutputLog() {
	if p.outputLog.Len() > 10000 {
		s := p.outputLog.String()
		p.outputLog.Reset()
		p.outputLog.WriteString(s[len(s)-7500:])
	}
}

func (p *StreamingPanel) trimReviewOutput() {
	const maxBytes = 80000
	if p.reviewOutput.Len() > maxBytes {
		s := p.reviewOutput.String()
		trimAt := len(s) - maxBytes*3/4
		if trimAt < 0 {
			trimAt = 0
		}
		if idx := strings.Index(s[trimAt:], "\n"); idx >= 0 {
			trimAt += idx + 1
		}
		p.reviewOutput.Reset()
		p.reviewOutput.WriteString("... (早期内容已截断) \n")
		p.reviewOutput.WriteString(s[trimAt:])
		debug.Logf("reviewOutput trimmed to %d bytes", p.reviewOutput.Len())
	}
}
