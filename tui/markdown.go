package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	mdHeadingRE  = regexp.MustCompile(`^(#{1,3})\s+(.+)$`)
	mdBoldRE     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdItalicRE   = regexp.MustCompile(`\*(.+?)\*`)
	mdCodeRE     = regexp.MustCompile("`([^`]+)`")
	mdNumberedRE = regexp.MustCompile(`^(\d+)\.\s+(.+)$`)
)

func renderMarkdown(text string, maxWidth int) string {
	if maxWidth <= 0 {
		maxWidth = 80
	}

	lines := strings.Split(text, "\n")
	var out strings.Builder
	inCodeBlock := false
	var codeBlock strings.Builder

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, " \t")

		// Handle code blocks
		if strings.HasPrefix(line, "```") {
			if inCodeBlock {
				// End code block
				codeStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("241")).
					Background(lipgloss.Color("236")).
					Padding(1, 2).
					Width(maxWidth).
					Render(codeBlock.String())
				out.WriteString("\n" + codeStyle + "\n")
				codeBlock.Reset()
				inCodeBlock = false
			} else {
				// Start code block
				inCodeBlock = true
			}
			continue
		}

		if inCodeBlock {
			codeBlock.WriteString(line + "\n")
			continue
		}

		if line == "" {
			out.WriteString("\n")
			continue
		}

		// Headings
		if m := mdHeadingRE.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			content := processInline(m[2])
			var style lipgloss.Style
			switch level {
			case 1:
				style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")).PaddingTop(1)
			case 2:
				style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("104"))
			case 3:
				style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
			}
			out.WriteString(style.Width(maxWidth).Render(content) + "\n")
			continue
		}

		// Unordered list items
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			content := processInline(line[2:])
			out.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render("• "+content) + "\n")
			continue
		}

		// Numbered list items
		if m := mdNumberedRE.FindStringSubmatch(line); m != nil {
			content := processInline(m[2])
			out.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(m[1]+". "+content) + "\n")
			continue
		}

		// Regular line with inline formatting
		out.WriteString(processInline(line) + "\n")
	}

	return out.String()
}

func processInline(text string) string {
	// Replace bold
	text = mdBoldRE.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[2 : len(match)-2]
		return lipgloss.NewStyle().Bold(true).Render(inner)
	})

	// Replace italic
	text = mdItalicRE.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[1 : len(match)-1]
		return lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("141")).Render(inner)
	})

	// Replace inline code
	text = mdCodeRE.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[1 : len(match)-1]
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("220")).
			Background(lipgloss.Color("236")).
			Padding(0, 1).
			Render(inner)
	})

	return text
}
