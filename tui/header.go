package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// HeaderBar renders a fixed top bar showing phase, model, and stats.
type HeaderBar struct {
	PhaseLabel  string
	DiffMode    string
	ModelName   string
	FileCount   int
	SelectedCnt int
	TokenCount  int
}

func (h HeaderBar) View(width int) string {
	if width <= 0 {
		width = 80
	}

	segments := []string{}

	// Phase label with dot indicator
	phaseStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(Th.Primary).
		Background(Th.SurfaceAlt).
		Padding(0, 2)
	phaseText := phaseStyle.Render(fmt.Sprintf("● %s", h.PhaseLabel))
	segments = append(segments, phaseText)

	if h.DiffMode != "" && h.DiffMode != "完整 diff" {
		tag := TagStyle(Th.Secondary).Render(h.DiffMode)
		segments = append(segments, tag)
	}

	// Right-aligned stats
	var rightSegments []string
	if h.ModelName != "" {
		rightSegments = append(rightSegments, DimStyle().Render(h.ModelName))
	}
	if h.FileCount > 0 {
		rightSegments = append(rightSegments, DimStyle().Render(fmt.Sprintf("文件:%d/%d", h.SelectedCnt, h.FileCount)))
	}
	if h.TokenCount > 0 {
		rightSegments = append(rightSegments, DimStyle().Render(fmt.Sprintf("token:%d", h.TokenCount)))
	}

	bar := BarStyle(width).
		Render(h.joinBar(segments, rightSegments, width))

	return bar
}

func (h HeaderBar) joinBar(left, right []string, width int) string {
	leftStr := strings.Join(left, "  ")
	rightStr := strings.Join(right, "  ")
	filler := width - len(leftStr) - len(rightStr)
	if filler < 1 {
		return leftStr + " " + rightStr
	}
	return leftStr + strings.Repeat(" ", filler) + rightStr
}
