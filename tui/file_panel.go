package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/oliver/git-ai-commit/internal/diff"
)

// filePanelMsg signals that file selection is complete.
type filePanelMsg struct {
	selectedFiles []string
	cancelled     bool
}

// FilePanel wraps the existing FileSelector to implement the Panel interface.
type FilePanel struct {
	selector *FileSelector
	files    []diff.FileChange
	done     bool
	quitting bool
}

func NewFilePanel(files []diff.FileChange) *FilePanel {
	return &FilePanel{
		selector: NewFileSelector(files),
		files:    files,
	}
}

func (p *FilePanel) Init() tea.Cmd {
	return p.selector.Init()
}

func (p *FilePanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	if p.done || p.quitting {
		return p, nil
	}

	model, cmd := p.selector.Update(msg)
	p.selector = model.(*FileSelector)

	if p.selector.quitting {
		p.quitting = true
		return p, func() tea.Msg {
			return filePanelMsg{cancelled: true}
		}
	}

	if p.selector.done {
		selected := p.selector.GetSelectedFiles()
		if len(selected) == 0 {
			p.quitting = true
			return p, func() tea.Msg {
				return filePanelMsg{cancelled: true}
			}
		}
		p.done = true
		return p, func() tea.Msg {
			return filePanelMsg{selectedFiles: selected}
		}
	}

	return p, cmd
}

func (p *FilePanel) View(width, height int) string {
	return p.selector.View()
}

func (p *FilePanel) Help() []HelpEntry {
	return []HelpEntry{
		{"↑↓", "移动"},
		{"Space", "选择"},
		{"A", "全选"},
		{"D", "取消"},
		{"Enter", "确认"},
		{"v/V", "查看 diff"},
		{"e", "gitignore"},
		{"q", "退出"},
	}
}

func (p *FilePanel) GetSelectedFiles() []string {
	return p.selector.GetSelectedFiles()
}

func (p *FilePanel) IsCancelled() bool {
	return p.quitting
}
