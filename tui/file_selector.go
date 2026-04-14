package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oliver/git-ai-commit/internal/diff"
)

var (
	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Padding(0, 1)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Padding(0, 1)

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205"))
)

type FileSelector struct {
	files    []diff.FileChange
	cursor   int
	selected map[int]bool
	quitting bool
	done     bool
}

func NewFileSelector(files []diff.FileChange) *FileSelector {
	selected := make(map[int]bool)
	return &FileSelector{
		files:    files,
		cursor:   0,
		selected: selected,
	}
}

func (f *FileSelector) Init() tea.Cmd {
	return nil
}

func (f *FileSelector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			f.quitting = true
			f.done = true
			return f, tea.Quit

		case "up", "k":
			if f.cursor > 0 {
				f.cursor--
			}

		case "down", "j":
			if f.cursor < len(f.files)-1 {
				f.cursor++
			}

		case " ", "enter":
			if len(f.files) > 0 {
				f.selected[f.cursor] = !f.selected[f.cursor]
				f.files[f.cursor].Selected = f.selected[f.cursor]
			}

		case "a":
			for i := range f.files {
				f.selected[i] = true
				f.files[i].Selected = true
			}

		case "d":
			for i := range f.files {
				f.selected[i] = false
				f.files[i].Selected = false
			}

		case "s":
			hasSelected := false
			for _, v := range f.selected {
				if v {
					hasSelected = true
					break
				}
			}
			if hasSelected {
				f.done = true
				return f, tea.Quit
			}
		}
	}

	return f, nil
}

func (f *FileSelector) View() string {
	if f.quitting {
		return "已取消\n"
	}

	if f.done {
		var selectedFiles []string
		for i, file := range f.files {
			if f.selected[i] {
				selectedFiles = append(selectedFiles, file.Path)
			}
		}
		if len(selectedFiles) == 0 {
			return "未选择任何文件\n"
		}
		return fmt.Sprintf("已选择 %d 个文件:\n%s\n", len(selectedFiles), strings.Join(selectedFiles, "\n"))
	}

	var b strings.Builder

	b.WriteString("选择要提交的文件 (↑↓ 移动，Space 选择，A 全选，D 取消，S 确认，Q 退出)\n\n")

	for i, file := range f.files {
		cursor := " "
		if i == f.cursor {
			cursor = cursorStyle.Render(">")
		}

		checkbox := "[ ]"
		style := normalStyle
		if f.selected[i] {
			checkbox = "[✓]"
			style = selectedStyle
		}

		status := "M"
		if file.Staged {
			status = "S"
		}

		line := fmt.Sprintf("%s %s %s %s", cursor, checkbox, status, file.Path)
		b.WriteString(style.Render(line) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(normalStyle.Render(fmt.Sprintf("已选择：%d/%d", f.countSelected(), len(f.files))))

	return b.String()
}

func (f *FileSelector) countSelected() int {
	count := 0
	for _, v := range f.selected {
		if v {
			count++
		}
	}
	return count
}

func (f *FileSelector) GetSelectedFiles() []string {
	var selected []string
	for i, file := range f.files {
		if f.selected[i] {
			selected = append(selected, file.Path)
		}
	}
	return selected
}

func (f *FileSelector) IsCancelled() bool {
	return f.quitting
}

func SelectFiles(files []diff.FileChange) ([]string, error) {
	selector := NewFileSelector(files)
	p := tea.NewProgram(selector, tea.WithAltScreen())

	model, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("运行文件选择器失败：%w", err)
	}

	selector, ok := model.(*FileSelector)
	if !ok {
		return nil, fmt.Errorf("类型转换失败")
	}

	if selector.IsCancelled() {
		return nil, fmt.Errorf("用户取消操作")
	}

	return selector.GetSelectedFiles(), nil
}
