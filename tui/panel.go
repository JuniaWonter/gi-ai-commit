package tui

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// HelpEntry describes a single keybinding for the footer help bar.
type HelpEntry struct {
	Key  string
	Desc string
}

// Panel is the interface that all phase panels must implement.
// Each panel manages its own state and rendering.
type Panel interface {
	// Init returns the initial command when the panel becomes active.
	Init() tea.Cmd

	// Update handles a message and returns the updated panel.
	Update(msg tea.Msg) (Panel, tea.Cmd)

	// View renders the panel content within the given dimensions.
	View(width, height int) string

	// Help returns keybinding help entries for the footer.
	Help() []HelpEntry
}

// commonPanel provides shared fields and methods for all panels.
type commonPanel struct {
	spinner spinner.Model
}

func newCommonPanel() commonPanel {
	s := spinner.New()
	s.Style = lipgloss.NewStyle().Foreground(Th.Secondary)
	s.Spinner = spinner.Dot
	return commonPanel{spinner: s}
}

func (c *commonPanel) SpinnerCmd() tea.Cmd {
	return c.spinner.Tick
}

// PanelTitle renders a styled panel title.
func PanelTitle(title string) string {
	return PrimaryStyle().Render(title)
}
