package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// OverlayType describes what kind of overlay to show.
type OverlayType int

const (
	OverlayConfirmCommit OverlayType = iota
	OverlayConfirmSummarize
	OverlayCustom
)

// OverlayResult is sent when the user dismisses the overlay.
type OverlayResult struct {
	Confirmed bool
	Type      OverlayType
}

// Overlay is a modal confirmation dialog.
type Overlay struct {
	Type    OverlayType
	Message string
	Help    string
}

func NewConfirmOverlay(t OverlayType) *Overlay {
	msg := "确认提交到 Git？"
	help := "Y/Enter 确认  N/Esc 取消"
	if t == OverlayConfirmSummarize {
		msg = "代码已理解，进入审查和提交阶段？"
		help = "Y/Enter 确认  N/Esc 取消"
	}
	return &Overlay{Type: t, Message: msg, Help: help}
}

func NewCustomOverlay(msg, help string) *Overlay {
	return &Overlay{Type: OverlayCustom, Message: msg, Help: help}
}

func (o *Overlay) Update(msg tea.Msg) (*Overlay, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y", "enter":
			return nil, func() tea.Msg { return OverlayResult{Confirmed: true, Type: o.Type} }
		case "n", "N", "esc":
			return nil, func() tea.Msg { return OverlayResult{Confirmed: false, Type: o.Type} }
		case "ctrl+c":
			return nil, tea.Quit
		}
	}
	return o, nil
}

func (o *Overlay) View(width, height int) string {
	if width <= 0 {
		width = 80
	}

	// Compact bottom-bar mode — does NOT cover the UI content
	if height <= 5 {
		return o.compactBar(width)
	}

	dialogW := 60
	if width < dialogW+4 {
		dialogW = width - 4
	}
	if dialogW < 30 {
		dialogW = 30
	}

	content := lipgloss.NewStyle().
		Bold(true).
		Foreground(Th.Warning).
		Render("? " + o.Message)

	helpText := DimStyle().Render(o.Help)

	dialog := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Th.Warning).
		Padding(1, 2).
		Width(dialogW - 4).
		Render(content + "\n\n" + helpText)

	// Center the dialog
	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		dialog,
	)
}

func (o *Overlay) compactBar(width int) string {
	content := "? " + o.Message + "    " + o.Help
	return lipgloss.NewStyle().
		Background(Th.Warning).
		Foreground(Th.Inverted).
		Bold(true).
		Width(width).
		Padding(0, 1).
		Render(content)
}
