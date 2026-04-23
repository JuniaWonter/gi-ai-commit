package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
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

	addedLineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	removedLineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1"))

	hunkHeaderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).Bold(true)

	diffHeaderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).Bold(true)

	diffTitleBarStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("63")).
			Background(lipgloss.Color("235")).
			Padding(0, 2)

	diffHelpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Background(lipgloss.Color("235")).
			Padding(0, 2)

	splitLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("1")).
			Background(lipgloss.Color("235")).
			Padding(0, 1)

	splitLabelAddedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("2")).
			Background(lipgloss.Color("235")).
			Padding(0, 1)

	splitSeparatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("63"))
)

type diffLoadedMsg struct {
	content string
	path    string
	err     error
}

type FileSelector struct {
	files       []diff.FileChange
	cursor      int
	selected    map[int]bool
	quitting    bool
	done        bool
	showDiff    bool
	splitView   bool
	viewport    viewport.Model
	leftVP      viewport.Model
	rightVP     viewport.Model
	ready       bool
	diffLoading bool
	termWidth   int
	termHeight  int
	diffPath    string
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
	case tea.WindowSizeMsg:
		f.termWidth = msg.Width
		f.termHeight = msg.Height
		if !f.ready {
			f.viewport = viewport.New(msg.Width, msg.Height-3)
			f.viewport.Style = lipgloss.NewStyle()
			halfW := msg.Width / 2 - 2
			f.leftVP = viewport.New(halfW, msg.Height-5)
			f.leftVP.Style = lipgloss.NewStyle()
			f.rightVP = viewport.New(halfW, msg.Height-5)
			f.rightVP.Style = lipgloss.NewStyle()
			f.ready = true
		} else {
			f.viewport.Width = msg.Width
			f.viewport.Height = msg.Height - 3
			halfW := msg.Width / 2 - 2
			f.leftVP.Width = halfW
			f.leftVP.Height = msg.Height - 5
			f.rightVP.Width = halfW
			f.rightVP.Height = msg.Height - 5
		}
		return f, nil

	case diffLoadedMsg:
		f.diffLoading = false
		if msg.err != nil {
			errContent := lipgloss.NewStyle().
				Foreground(lipgloss.Color("1")).
				Render(fmt.Sprintf("Error: %v", msg.err))
			f.viewport.SetContent(errContent)
			f.leftVP.SetContent(errContent)
			f.rightVP.SetContent("")
		} else {
			f.viewport.SetContent(highlightDiff(msg.content))
			leftContent, rightContent := highlightDiffSideBySide(msg.content)
			f.leftVP.SetContent(leftContent)
			f.rightVP.SetContent(rightContent)
		}
		f.viewport.GotoTop()
		f.leftVP.GotoTop()
		f.rightVP.GotoTop()
		f.showDiff = true
		return f, nil

	case tea.KeyMsg:
		if f.showDiff {
			return f.handleDiffKeys(msg)
		}
		return f.handleSelectorKeys(msg)
	}

	if f.showDiff {
		if f.splitView {
			var cmdL, cmdR tea.Cmd
			f.leftVP, cmdL = f.leftVP.Update(msg)
			f.rightVP, cmdR = f.rightVP.Update(msg)
			return f, tea.Batch(cmdL, cmdR)
		}
		var cmd tea.Cmd
		f.viewport, cmd = f.viewport.Update(msg)
		return f, cmd
	}

	return f, nil
}

func (f *FileSelector) handleDiffKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		f.showDiff = false
		return f, nil
	case "tab":
		f.splitView = !f.splitView
		if f.splitView {
			halfW := f.termWidth / 2 - 2
			f.leftVP.Width = halfW
			f.leftVP.Height = f.termHeight - 5
			f.rightVP.Width = halfW
			f.rightVP.Height = f.termHeight - 5
			f.leftVP.GotoTop()
			f.rightVP.GotoTop()
		} else {
			f.viewport.Width = f.termWidth
			f.viewport.Height = f.termHeight - 3
			f.viewport.GotoTop()
		}
		return f, nil
	default:
		if f.splitView {
			var cmdL, cmdR tea.Cmd
			f.leftVP, cmdL = f.leftVP.Update(msg)
			f.rightVP, cmdR = f.rightVP.Update(msg)
			return f, tea.Batch(cmdL, cmdR)
		}
		var cmd tea.Cmd
		f.viewport, cmd = f.viewport.Update(msg)
		return f, cmd
	}
}

func (f *FileSelector) handleSelectorKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	case "v":
		if len(f.files) > 0 && !f.diffLoading {
			filePath := f.files[f.cursor].Path
			f.diffPath = filePath
			f.diffLoading = true
			return f, loadDiffCmd(filePath)
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
	return f, nil
}

func loadDiffCmd(filePath string) tea.Cmd {
	return func() tea.Msg {
		content, err := diff.GetFileDiff(filePath)
		return diffLoadedMsg{content: content, path: filePath, err: err}
	}
}

func highlightDiff(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "diff --cc") {
			b.WriteString(diffHeaderStyle.Render(line) + "\n")
		} else if strings.HasPrefix(line, "@@") {
			b.WriteString(hunkHeaderStyle.Render(line) + "\n")
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			b.WriteString(addedLineStyle.Render(line) + "\n")
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			b.WriteString(removedLineStyle.Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

type diffLine struct {
	content string
	typ     string
}

func parseDiffLines(content string) []diffLine {
	lines := strings.Split(content, "\n")
	var result []diffLine
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "diff --cc") {
			result = append(result, diffLine{content: line, typ: "header"})
		} else if strings.HasPrefix(line, "@@") {
			result = append(result, diffLine{content: line, typ: "hunk"})
		} else if strings.HasPrefix(line, "---") {
			result = append(result, diffLine{content: line, typ: "oldFile"})
		} else if strings.HasPrefix(line, "+++") {
			result = append(result, diffLine{content: line, typ: "newFile"})
		} else if strings.HasPrefix(line, "-") {
			result = append(result, diffLine{content: line, typ: "removed"})
		} else if strings.HasPrefix(line, "+") {
			result = append(result, diffLine{content: line, typ: "added"})
		} else {
			result = append(result, diffLine{content: line, typ: "context"})
		}
	}
	return result
}

func highlightDiffSideBySide(content string) (string, string) {
	dlines := parseDiffLines(content)
	var leftLines, rightLines []string

	i := 0
	for i < len(dlines) {
		dl := dlines[i]
		switch dl.typ {
		case "header", "hunk", "oldFile", "newFile":
			rendered := renderDiffLine(dl)
			leftLines = append(leftLines, rendered)
			rightLines = append(rightLines, rendered)
			i++
		case "removed":
			removedBatch := []diffLine{dl}
			j := i + 1
			for j < len(dlines) && dlines[j].typ == "removed" {
				removedBatch = append(removedBatch, dlines[j])
				j++
			}
			addedBatch := []diffLine{}
			for j < len(dlines) && dlines[j].typ == "added" {
				addedBatch = append(addedBatch, dlines[j])
				j++
			}
			maxLen := max(len(removedBatch), len(addedBatch))
			for k := 0; k < maxLen; k++ {
				if k < len(removedBatch) {
					leftLines = append(leftLines, removedLineStyle.Render(removedBatch[k].content))
				} else {
					leftLines = append(leftLines, "")
				}
				if k < len(addedBatch) {
					rightLines = append(rightLines, addedLineStyle.Render(addedBatch[k].content))
				} else {
					rightLines = append(rightLines, "")
				}
			}
			i = j
		case "added":
			rightLines = append(rightLines, addedLineStyle.Render(dl.content))
			leftLines = append(leftLines, "")
			i++
		case "context":
			leftLines = append(leftLines, dl.content)
			rightLines = append(rightLines, dl.content)
			i++
		default:
			leftLines = append(leftLines, dl.content)
			rightLines = append(rightLines, dl.content)
			i++
		}
	}

	return strings.Join(leftLines, "\n"), strings.Join(rightLines, "\n")
}

func renderDiffLine(dl diffLine) string {
	switch dl.typ {
	case "header":
		return diffHeaderStyle.Render(dl.content)
	case "hunk":
		return hunkHeaderStyle.Render(dl.content)
	case "oldFile":
		return removedLineStyle.Render(dl.content)
	case "newFile":
		return addedLineStyle.Render(dl.content)
	default:
		return dl.content
	}
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

	if f.showDiff || f.diffLoading {
		return f.renderDiffView()
	}

	return f.renderFileList()
}

func (f *FileSelector) renderDiffView() string {
	mode := "单列"
	if f.splitView {
		mode = "左右"
	}
	title := fmt.Sprintf("  %s", f.diffPath)
	help := fmt.Sprintf("  ↑↓ 滚动 │ Tab 切换视图(%s) │ q/Esc 返回列表", mode)

	titleBar := diffTitleBarStyle.Width(f.termWidth).Render(title)
	helpBar := diffHelpStyle.Width(f.termWidth).Render(help)

	var content string
	if f.diffLoading {
		content = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Render("加载 diff 中...")
	} else if f.splitView {
		content = f.renderSplitView()
	} else {
		content = f.viewport.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left, titleBar, content, helpBar)
}

func (f *FileSelector) renderSplitView() string {
	sep := splitSeparatorStyle.Render("│")
	halfW := f.termWidth / 2 - 1

	leftLabel := splitLabelStyle.Width(halfW).Render(" 删除(-)")
	rightLabel := splitLabelAddedStyle.Width(halfW).Render(" 新增(+)")
	labelRow := lipgloss.JoinHorizontal(lipgloss.Top, leftLabel, sep, rightLabel)

	leftContent := f.leftVP.View()
	rightContent := f.rightVP.View()

	panels := lipgloss.JoinHorizontal(lipgloss.Top, leftContent, sep, rightContent)

	return lipgloss.JoinVertical(lipgloss.Left, labelRow, panels)
}

func (f *FileSelector) renderFileList() string {
	var b strings.Builder

	b.WriteString("选择要提交的文件 (↑↓/j/k 移动，Space 选择，A 全选，D 取消，v 查看 diff，S 确认，Q 退出)\n\n")

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