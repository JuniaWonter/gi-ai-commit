package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// FooterBar renders a fixed bottom bar with keybinding help.
type FooterBar struct {
	Entries  []HelpEntry
	RightMsg string
	FgColor  lipgloss.Color
	BgColor  lipgloss.Color
}

func NewFooterBar(entries []HelpEntry) FooterBar {
	return FooterBar{
		Entries: entries,
		FgColor: Th.DimText,
		BgColor: Th.Surface,
	}
}

func (f FooterBar) View(width int) string {
	if width <= 0 {
		width = 80
	}
	if f.BgColor == "" {
		f.BgColor = Th.Surface
	}
	if f.FgColor == "" {
		f.FgColor = Th.DimText
	}

	style := lipgloss.NewStyle().
		Background(f.BgColor).
		Foreground(f.FgColor).
		Width(width).
		Padding(0, 1)

	var leftParts []string
	for _, e := range f.Entries {
		key := lipgloss.NewStyle().
			Foreground(Th.Primary).
			Background(f.BgColor).
			Bold(true).
			Render(e.Key)
		leftParts = append(leftParts, key+" "+e.Desc)
	}

	leftStr := strings.Join(leftParts, "  │  ")
	filler := width - len(leftStr) - len(f.RightMsg)
	if filler < 1 {
		filler = 1
	}

	return style.Render(leftStr + strings.Repeat(" ", filler) + f.RightMsg)
}
