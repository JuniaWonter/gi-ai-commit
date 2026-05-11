package tui

import "github.com/charmbracelet/lipgloss"

// Theme defines a consistent color palette for the entire TUI.
// All colors are centralized here instead of scattered across files.
type Theme struct {
	// Core palette
	Primary   lipgloss.Color
	Secondary lipgloss.Color
	Accent    lipgloss.Color

	// Semantic
	Success lipgloss.Color
	Error   lipgloss.Color
	Warning lipgloss.Color
	Info    lipgloss.Color

	// Text
	Text     lipgloss.Color
	DimText  lipgloss.Color
	Inverted lipgloss.Color

	// Surfaces
	Surface     lipgloss.Color
	SurfaceAlt  lipgloss.Color
	Border      lipgloss.Color
	BorderFocus lipgloss.Color
}

var DefaultTheme = Theme{
	// Purple-based modern palette
	Primary:   lipgloss.Color("99"),  // bright purple
	Secondary: lipgloss.Color("63"),  // medium purple
	Accent:    lipgloss.Color("141"), // soft purple

	Success: lipgloss.Color("2"),
	Error:   lipgloss.Color("1"),
	Warning: lipgloss.Color("220"),
	Info:    lipgloss.Color("6"),

	Text:     lipgloss.Color("252"),
	DimText:  lipgloss.Color("241"),
	Inverted: lipgloss.Color("0"),

	Surface:     lipgloss.Color("235"),
	SurfaceAlt:  lipgloss.Color("236"),
	Border:      lipgloss.Color("237"),
	BorderFocus: lipgloss.Color("99"),
}

var Th = DefaultTheme

// Convenience style constructors
func PrimaryStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(Th.Primary).Bold(true)
}

func SecondaryStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(Th.Secondary)
}

func SuccessStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(Th.Success).Bold(true)
}

func ErrorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(Th.Error).Bold(true)
}

func DimStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(Th.DimText)
}

func PanelStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Th.Border).
		Padding(0, 1).
		Width(width - 4)
}

func PanelFocusStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Th.BorderFocus).
		Padding(0, 1).
		Width(width - 4)
}

func BarStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Background(Th.Surface).
		Foreground(Th.Text).
		Width(width).
		Padding(0, 1)
}

func TagStyle(bg lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Background(bg).
		Foreground(Th.Inverted).
		Bold(true).
		Padding(0, 1)
}
