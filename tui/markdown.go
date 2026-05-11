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
	mdLinkRE     = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// simpleMarkdownCache caches rendered markdown output to avoid re-rendering
// on every frame when content hasn't changed.
var simpleMarkdownCache struct {
	input  string
	maxW   int
	output string
}

func renderMarkdown(text string, maxWidth int) string {
	if maxWidth <= 0 {
		maxWidth = 80
	}
	if simpleMarkdownCache.input == text && simpleMarkdownCache.maxW == maxWidth {
		return simpleMarkdownCache.output
	}

	lines := strings.Split(text, "\n")
	var out strings.Builder
	inCodeBlock := false
	var codeBlock strings.Builder

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, " \t")

		if strings.HasPrefix(line, "```") {
			if inCodeBlock {
				codeStyle := lipgloss.NewStyle().
					Foreground(Th.DimText).
					Background(Th.SurfaceAlt).
					Padding(1, 2).
					Width(maxWidth).
					Render(codeBlock.String())
				out.WriteString("\n" + codeStyle + "\n")
				codeBlock.Reset()
				inCodeBlock = false
			} else {
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
			content := processInlineMarkdown(m[2])
			var style lipgloss.Style
			switch level {
			case 1:
				style = lipgloss.NewStyle().Bold(true).Foreground(Th.Primary).PaddingTop(1)
			case 2:
				style = lipgloss.NewStyle().Bold(true).Foreground(Th.Secondary)
			case 3:
				style = lipgloss.NewStyle().Bold(true).Foreground(Th.Accent)
			}
			out.WriteString(style.Width(maxWidth).Render(content) + "\n")
			continue
		}

		// Unordered list
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			content := processInlineMarkdown(line[2:])
			out.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render("• "+content) + "\n")
			continue
		}

		// Numbered list
		if m := mdNumberedRE.FindStringSubmatch(line); m != nil {
			content := processInlineMarkdown(m[2])
			out.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(m[1]+". "+content) + "\n")
			continue
		}

		// Regular line
		out.WriteString(processInlineMarkdown(line) + "\n")
	}

	result := out.String()
	simpleMarkdownCache.input = text
	simpleMarkdownCache.maxW = maxWidth
	simpleMarkdownCache.output = result
	return result
}

func processInlineMarkdown(text string) string {
	// Links: [text](url) → text (underline)
	text = mdLinkRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := mdLinkRE.FindStringSubmatch(match)
		if len(parts) >= 3 {
			return lipgloss.NewStyle().
				Foreground(Th.Info).
				Underline(true).
				Render(parts[1])
		}
		return match
	})

	// Bold
	text = mdBoldRE.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[2 : len(match)-2]
		return lipgloss.NewStyle().Bold(true).Render(inner)
	})

	// Italic
	text = mdItalicRE.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[1 : len(match)-1]
		return lipgloss.NewStyle().Italic(true).Foreground(Th.Accent).Render(inner)
	})

	// Inline code
	text = mdCodeRE.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[1 : len(match)-1]
		return lipgloss.NewStyle().
			Foreground(Th.Warning).
			Background(Th.SurfaceAlt).
			Padding(0, 1).
			Render(inner)
	})

	return text
}
