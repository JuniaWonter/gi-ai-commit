package tui

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oliver/git-ai-commit/internal/diff"
	"github.com/oliver/git-ai-commit/internal/git"
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

	folderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4")).
			Bold(true).
			Padding(0, 1)

	addedLineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	removedLineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1"))

	contextLineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241"))

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

	lineNumStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	statAddedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	statRemovedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1"))

	statFileStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6"))

	statHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("63"))

	indicatorAddedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("2"))

	indicatorRemovedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("1"))

	indicatorContextStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("238"))

	indicatorHunkStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("6"))
)

type diffLoadedMsg struct {
	content  string
	rawDiff  string
	path     string
	err      error
	ignoreWS bool
}

type gitignoreAddedMsg struct {
	entry string
	err   error
}

type filesRefreshedMsg struct {
	files []diff.FileChange
	err   error
}

type FileSelector struct {
	files         []diff.FileChange
	cursor        int
	selected      map[int]bool
	quitting      bool
	done          bool
	showDiff      bool
	splitView     bool
	hideContext   bool
	showLineNum   bool
	ignoreWS      bool
	showStat      bool
	viewport      viewport.Model
	leftVP        viewport.Model
	rightVP       viewport.Model
	ready         bool
	diffLoading   bool
	termWidth     int
	termHeight    int
	diffPath      string
	rawDiff       string
	totalLines    int
	addedLines    int
	removedLines  int
	hunkPositions []int
	lineTypes     []string
	gitignoreMsg  string
	// Folder grouping
	collapsedFolders map[string]bool
	fileListVP       viewport.Model
	fileListReady    bool
}

func NewFileSelector(files []diff.FileChange) *FileSelector {
	selected := make(map[int]bool)
	collapsedFolders := make(map[string]bool)
	return &FileSelector{
		files:            files,
		cursor:           0,
		selected:         selected,
		collapsedFolders: collapsedFolders,
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
			halfW := msg.Width/2 - 2
			f.leftVP = viewport.New(halfW, msg.Height-5)
			f.leftVP.Style = lipgloss.NewStyle()
			f.rightVP = viewport.New(halfW, msg.Height-5)
			f.rightVP.Style = lipgloss.NewStyle()
			f.ready = true
		} else {
			f.viewport.Width = msg.Width
			f.viewport.Height = msg.Height - 3
			halfW := msg.Width/2 - 2
			f.leftVP.Width = halfW
			f.leftVP.Height = msg.Height - 5
			f.rightVP.Width = halfW
			f.rightVP.Height = msg.Height - 5
		}
		return f, nil

	case diffLoadedMsg:
		f.diffLoading = false
		if !f.ready {
			w := f.termWidth
			h := f.termHeight
			if w == 0 {
				w = 80
			}
			if h == 0 {
				h = 24
			}
			f.viewport = viewport.New(w, h-3)
			f.viewport.Style = lipgloss.NewStyle()
			halfW := w/2 - 2
			f.leftVP = viewport.New(halfW, h-5)
			f.leftVP.Style = lipgloss.NewStyle()
			f.rightVP = viewport.New(halfW, h-5)
			f.rightVP.Style = lipgloss.NewStyle()
			f.ready = true
		}
		if msg.err != nil {
			errContent := lipgloss.NewStyle().
				Foreground(lipgloss.Color("1")).
				Render(fmt.Sprintf("Error: %v", msg.err))
			f.viewport.SetContent(errContent)
			f.leftVP.SetContent(errContent)
			f.rightVP.SetContent("")
		} else {
			f.rawDiff = msg.rawDiff
			f.ignoreWS = msg.ignoreWS
			f.refreshDiffView()
		}
		f.viewport.GotoTop()
		f.leftVP.GotoTop()
		f.rightVP.GotoTop()
		f.showDiff = true
		return f, nil

	case gitignoreAddedMsg:
		if msg.err != nil {
			f.gitignoreMsg = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(fmt.Sprintf("✗ 添加 .gitignore 失败：%v", msg.err))
		} else {
			f.gitignoreMsg = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(fmt.Sprintf("✓ 已添加 %s 到 .gitignore，刷新文件列表...", msg.entry))
			return f, refreshFilesCmd()
		}
		return f, nil

	case filesRefreshedMsg:
		if msg.err != nil {
			f.gitignoreMsg = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(fmt.Sprintf("✗ 刷新文件列表失败：%v", msg.err))
		} else {
			oldCursorPath := ""
			if len(f.files) > 0 && f.cursor < len(f.files) {
				oldCursorPath = f.files[f.cursor].Path
			}
			f.files = msg.files
			f.selected = make(map[int]bool)
			f.cursor = 0
			for i, file := range f.files {
				f.selected[i] = file.Selected
			}
			if oldCursorPath != "" {
				for i, file := range f.files {
					if file.Path == oldCursorPath {
						f.cursor = i
						break
					}
				}
			}
			if f.cursor >= len(f.files) {
				f.cursor = max(0, len(f.files)-1)
			}
			f.gitignoreMsg = ""
		}
		return f, nil

	case tea.KeyMsg:
		if f.showDiff {
			return f.handleDiffKeys(msg)
		}
		return f.handleSelectorKeys(msg)
	}

	if f.showDiff {
		return f.scrollViewports(msg)
	}

	return f, nil
}

func (f *FileSelector) scrollViewports(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (f *FileSelector) refreshDiffView() {
	w := f.termWidth
	h := f.termHeight
	if w == 0 {
		w = 80
	}
	if h == 0 {
		h = 24
	}
	f.computeStats(f.rawDiff)
	content := f.renderDiffContent(f.rawDiff)
	if !f.ready {
		f.viewport = viewport.New(w, h-3)
		f.viewport.Style = lipgloss.NewStyle()
		halfW := w/2 - 2
		f.leftVP = viewport.New(halfW, h-5)
		f.leftVP.Style = lipgloss.NewStyle()
		f.rightVP = viewport.New(halfW, h-5)
		f.rightVP.Style = lipgloss.NewStyle()
		f.ready = true
	}
	f.viewport.Width = w
	f.viewport.Height = h - 3
	f.viewport.SetContent(content)
	f.viewport.YOffset = 0
	leftContent, rightContent := f.renderDiffSideBySideContent(f.rawDiff)
	halfW := w/2 - 2
	f.leftVP.Width = halfW
	f.leftVP.Height = h - 5
	f.leftVP.SetContent(leftContent)
	f.leftVP.YOffset = 0
	f.rightVP.Width = halfW
	f.rightVP.Height = h - 5
	f.rightVP.SetContent(rightContent)
	f.rightVP.YOffset = 0
}

func (f *FileSelector) handleDiffKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		f.showDiff = false
		f.showStat = false
		return f, nil
	case "tab":
		f.splitView = !f.splitView
		f.refreshDiffView()
		if f.splitView {
			f.leftVP.GotoTop()
			f.rightVP.GotoTop()
		} else {
			f.viewport.GotoTop()
		}
		return f, nil
	case "h":
		f.hideContext = !f.hideContext
		f.refreshDiffView()
		return f, nil
	case "l":
		f.showLineNum = !f.showLineNum
		f.refreshDiffView()
		return f, nil
	case "w":
		f.ignoreWS = !f.ignoreWS
		f.diffLoading = true
		return f, loadDiffCmd(f.diffPath, f.ignoreWS)
	case "i":
		f.showStat = !f.showStat
		if f.showStat {
			stat := f.computeStatSummary(f.rawDiff)
			f.viewport.SetContent(stat)
			f.viewport.GotoTop()
		} else {
			f.refreshDiffView()
		}
		return f, nil
	default:
		return f.scrollViewports(msg)
	}
}

func (f *FileSelector) handleSelectorKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		f.quitting = true
		f.done = true
		return f, tea.Quit

	case "up", "k":
		f.moveCursorUp()

	case "down", "j":
		f.moveCursorDown()

	case "tab":
		// Toggle folder collapse
		items := f.buildDisplayList()
		if f.cursor < len(items) && items[f.cursor].isFolder {
			f.toggleFolderCollapse(items[f.cursor].folder)
			// Adjust cursor if needed
			newItems := f.buildDisplayList()
			if f.cursor >= len(newItems) {
				f.cursor = len(newItems) - 1
			}
		}

	case " ":
		items := f.buildDisplayList()
		if f.cursor < len(items) && !items[f.cursor].isFolder {
			fileIdx := items[f.cursor].fileIndex
			f.selected[fileIdx] = !f.selected[fileIdx]
			f.files[fileIdx].Selected = f.selected[fileIdx]
		}

	case "enter":
		hasSelected := false
		for _, v := range f.selected {
			if v {
				hasSelected = true
				break
			}
		}
		if hasSelected {
			f.done = true
			return f, nil
		}
		items := f.buildDisplayList()
		if f.cursor < len(items) && !items[f.cursor].isFolder {
			fileIdx := items[f.cursor].fileIndex
			f.selected[fileIdx] = true
			f.files[fileIdx].Selected = true
			f.done = true
			return f, nil
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
		items := f.buildDisplayList()
		if f.cursor < len(items) && !items[f.cursor].isFolder && !f.diffLoading {
			fileIdx := items[f.cursor].fileIndex
			filePath := f.files[fileIdx].Path
			f.diffPath = filePath
			f.splitView = false
			f.diffLoading = true
			return f, loadDiffCmd(filePath, f.ignoreWS)
		}

	case "V":
		items := f.buildDisplayList()
		if f.cursor < len(items) && !items[f.cursor].isFolder && !f.diffLoading {
			fileIdx := items[f.cursor].fileIndex
			filePath := f.files[fileIdx].Path
			f.diffPath = filePath
			f.splitView = true
			f.diffLoading = true
			return f, loadDiffCmd(filePath, f.ignoreWS)
		}

	case "e":
		items := f.buildDisplayList()
		if f.cursor < len(items) && !items[f.cursor].isFolder {
			fileIdx := items[f.cursor].fileIndex
			entry := f.files[fileIdx].Path
			return f, addGitignoreCmd(entry)
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
			return f, nil
		}
	}
	return f, nil
}

func loadDiffCmd(filePath string, ignoreWS bool) tea.Cmd {
	return func() tea.Msg {
		content, raw, err := diff.GetFileDiffFull(filePath, ignoreWS)
		return diffLoadedMsg{content: content, rawDiff: raw, path: filePath, err: err, ignoreWS: ignoreWS}
	}
}

func addGitignoreCmd(entry string) tea.Cmd {
	return func() tea.Msg {
		err := git.AddToGitignore(entry)
		return gitignoreAddedMsg{entry: entry, err: err}
	}
}

func refreshFilesCmd() tea.Cmd {
	return func() tea.Msg {
		files, err := diff.GetChangedFiles()
		return filesRefreshedMsg{files: files, err: err}
	}
}

func (f *FileSelector) computeStats(raw string) {
	dlines := parseDiffLines(raw)
	f.totalLines = len(dlines)
	f.addedLines = 0
	f.removedLines = 0
	f.hunkPositions = nil
	f.lineTypes = nil

	lineIdx := 0
	oldLine := 0
	newLine := 0

	for _, dl := range dlines {
		if dl.typ == "hunk" {
			oldLine, newLine = parseHunkLineNumbers(dl.content)
			f.hunkPositions = append(f.hunkPositions, lineIdx)
		}

		f.lineTypes = append(f.lineTypes, dl.typ)

		switch dl.typ {
		case "added":
			f.addedLines++
			newLine++
		case "removed":
			f.removedLines++
			oldLine++
		case "context":
			oldLine++
			newLine++
		}
		lineIdx++
	}
}

func parseHunkLineNumbers(hunkLine string) (int, int) {
	parts := strings.SplitN(hunkLine, "@@", 3)
	if len(parts) < 2 {
		return 0, 0
	}
	rangeInfo := strings.TrimSpace(parts[1])
	oldRange := ""
	newRange := ""
	for _, token := range strings.Fields(rangeInfo) {
		if strings.HasPrefix(token, "-") {
			oldRange = token
		} else if strings.HasPrefix(token, "+") {
			newRange = token
		}
	}

	oldStart := 0
	newStart := 0

	if oldRange != "" {
		numStr := strings.TrimPrefix(oldRange, "-")
		if commaIdx := strings.Index(numStr, ","); commaIdx >= 0 {
			numStr = numStr[:commaIdx]
		}
		oldStart, _ = strconv.Atoi(numStr)
	}
	if newRange != "" {
		numStr := strings.TrimPrefix(newRange, "+")
		if commaIdx := strings.Index(numStr, ","); commaIdx >= 0 {
			numStr = numStr[:commaIdx]
		}
		newStart, _ = strconv.Atoi(numStr)
	}

	return oldStart, newStart
}

func formatLineNum(num int, width int) string {
	if num < 0 {
		return strings.Repeat(" ", width)
	}
	s := strconv.Itoa(num)
	if len(s) < width {
		s = strings.Repeat(" ", width-len(s)) + s
	}
	return lineNumStyle.Render(s)
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

func (f *FileSelector) renderDiffContent(raw string) string {
	dlines := parseDiffLines(raw)
	lineNumWidth := 5
	var b strings.Builder
	oldLine := 0
	newLine := 0

	for _, dl := range dlines {
		if dl.typ == "hunk" {
			oldLine, newLine = parseHunkLineNumbers(dl.content)
		}

		if f.hideContext && dl.typ == "context" {
			continue
		}

		line := dl.content
		switch dl.typ {
		case "header":
			line = diffHeaderStyle.Render(line)
		case "hunk":
			line = hunkHeaderStyle.Render(line)
		case "added":
			line = addedLineStyle.Render(line)
			newLine++
		case "removed":
			line = removedLineStyle.Render(line)
			oldLine++
		case "context":
			line = contextLineStyle.Render(line)
			oldLine++
			newLine++
		case "oldFile":
			line = removedLineStyle.Render(line)
		case "newFile":
			line = addedLineStyle.Render(line)
		}

		if f.showLineNum {
			var oldN, newN string
			switch dl.typ {
			case "added":
				oldN = formatLineNum(-1, lineNumWidth)
				newN = formatLineNum(newLine-1, lineNumWidth)
			case "removed":
				oldN = formatLineNum(oldLine-1, lineNumWidth)
				newN = formatLineNum(-1, lineNumWidth)
			case "context":
				oldN = formatLineNum(oldLine-1, lineNumWidth)
				newN = formatLineNum(newLine-1, lineNumWidth)
			default:
				b.WriteString(line + "\n")
				continue
			}
			b.WriteString(oldN + " " + newN + " " + line + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func (f *FileSelector) renderDiffSideBySideContent(raw string) (string, string) {
	dlines := parseDiffLines(raw)
	lineNumWidth := 4

	var leftLines, rightLines []string
	oldLine := 0
	newLine := 0

	i := 0
	for i < len(dlines) {
		dl := dlines[i]
		switch dl.typ {
		case "hunk":
			oldLine, newLine = parseHunkLineNumbers(dl.content)
			leftLines = append(leftLines, hunkHeaderStyle.Render(dl.content))
			rightLines = append(rightLines, hunkHeaderStyle.Render(dl.content))
			i++
		case "header", "oldFile", "newFile":
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
				var left, right string
				if k < len(removedBatch) {
					lNum := formatLineNum(oldLine, lineNumWidth)
					content := removedLineStyle.Render(removedBatch[k].content)
					left = lNum + " " + content
					oldLine++
				} else {
					left = strings.Repeat(" ", lineNumWidth+1)
				}
				if k < len(addedBatch) {
					rNum := formatLineNum(newLine, lineNumWidth)
					content := addedLineStyle.Render(addedBatch[k].content)
					right = rNum + " " + content
					newLine++
				} else {
					right = strings.Repeat(" ", lineNumWidth+1)
				}
				leftLines = append(leftLines, left)
				rightLines = append(rightLines, right)
			}
			i = j
		case "added":
			rNum := formatLineNum(newLine, lineNumWidth)
			content := addedLineStyle.Render(dl.content)
			rightLines = append(rightLines, rNum+" "+content)
			leftLines = append(leftLines, strings.Repeat(" ", lineNumWidth+1))
			newLine++
			i++
		case "context":
			if !f.hideContext {
				oNum := formatLineNum(oldLine, lineNumWidth)
				nNum := formatLineNum(newLine, lineNumWidth)
				leftLines = append(leftLines, oNum+" "+contextLineStyle.Render(dl.content))
				rightLines = append(rightLines, nNum+" "+contextLineStyle.Render(dl.content))
				oldLine++
				newLine++
			}
			i++
		default:
			leftLines = append(leftLines, dl.content)
			rightLines = append(rightLines, dl.content)
			i++
		}
	}

	return strings.Join(leftLines, "\n"), strings.Join(rightLines, "\n")
}

func (f *FileSelector) computeStatSummary(raw string) string {
	dlines := parseDiffLines(raw)
	var added, removed, context int
	fileNames := []string{}
	currentFile := ""

	for _, dl := range dlines {
		switch dl.typ {
		case "header":
			parts := strings.SplitN(dl.content, " ", 4)
			if len(parts) >= 4 {
				name := parts[3]
				name = strings.TrimPrefix(name, "b/")
				currentFile = name
				fileNames = append(fileNames, name)
			}
		case "added":
			added++
		case "removed":
			removed++
		case "context":
			context++
		}
	}

	var b strings.Builder
	b.WriteString(statHeaderStyle.Render("变更统计摘要") + "\n\n")
	b.WriteString(statFileStyle.Render(fmt.Sprintf("变更文件数：%d", len(fileNames))) + "\n")
	if currentFile != "" {
		b.WriteString(statFileStyle.Render(fmt.Sprintf("当前文件：%s", currentFile)) + "\n")
	}
	b.WriteString(statAddedStyle.Render(fmt.Sprintf("新增行数：%d", added)) + "\n")
	b.WriteString(statRemovedStyle.Render(fmt.Sprintf("删除行数：%d", removed)) + "\n")
	b.WriteString(fmt.Sprintf("上下文行：%d", context) + "\n")
	b.WriteString(fmt.Sprintf("总行数：%d", f.totalLines) + "\n\n")

	b.WriteString(statHeaderStyle.Render("文件列表") + "\n")
	for _, name := range fileNames {
		b.WriteString("  " + name + "\n")
	}

	if len(f.hunkPositions) > 0 {
		b.WriteString("\n" + statHeaderStyle.Render(fmt.Sprintf("Hunk 数：%d", len(f.hunkPositions))) + "\n")
	}

	return b.String()
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

func (f *FileSelector) renderMinimap() string {
	if len(f.lineTypes) == 0 {
		return ""
	}

	total := len(f.lineTypes)
	height := f.termHeight - 6
	if height < 5 {
		height = 5
	}
	if height > 40 {
		height = 40
	}

	blockSize := 1
	if total > height {
		blockSize = total / height
		if blockSize < 1 {
			blockSize = 1
		}
	}

	var blocks []string
	for i := 0; i < total; i += blockSize {
		end := i + blockSize
		if end > total {
			end = total
		}
		segment := f.lineTypes[i:end]
		addedCount := 0
		removedCount := 0
		hunkCount := 0
		contextCount := 0
		for _, t := range segment {
			switch t {
			case "added":
				addedCount++
			case "removed":
				removedCount++
			case "hunk":
				hunkCount++
			case "context":
				contextCount++
			}
		}
		if hunkCount > 0 {
			blocks = append(blocks, indicatorHunkStyle.Render(" "))
		} else if removedCount > addedCount {
			blocks = append(blocks, indicatorRemovedStyle.Render(" "))
		} else if addedCount > 0 {
			blocks = append(blocks, indicatorAddedStyle.Render(" "))
		} else if contextCount > 0 {
			blocks = append(blocks, indicatorContextStyle.Render(" "))
		} else {
			blocks = append(blocks, " ")
		}
	}

	return strings.Join(blocks, "\n")
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

	flags := ""
	if f.hideContext {
		flags += " [精简]"
	}
	if f.showLineNum {
		flags += " [行号]"
	}
	if f.ignoreWS {
		flags += " [忽略空白]"
	}

	title := fmt.Sprintf("  %s +%d -%d%s", f.diffPath, f.addedLines, f.removedLines, flags)
	help := fmt.Sprintf("  ↑↓ 滚动 │ Tab 视图(%s) │ h 精简 │ l 行号 │ w 忽略空白 │ i 统计 │ q/Esc 返回", mode)

	titleBar := diffTitleBarStyle.Width(f.termWidth).Render(title)
	helpBar := diffHelpStyle.Width(f.termWidth).Render(help)

	var content string
	if f.diffLoading {
		content = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Render("加载 diff 中...")
	} else if f.showStat {
		f.viewport.Width = f.termWidth
		f.viewport.Height = f.termHeight - 3
		content = f.viewport.View()
	} else if f.splitView {
		content = f.renderSplitView()
	} else {
		f.viewport.Width = f.termWidth
		f.viewport.Height = f.termHeight - 3
		mainContent := f.viewport.View()
		minimap := f.renderMinimap()
		if minimap != "" && !f.showStat {
			minimapPanel := lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, true, false, false).
				BorderForeground(lipgloss.Color("63")).
				Padding(0, 1).
				Render(minimap)
			content = lipgloss.JoinHorizontal(lipgloss.Top, mainContent, minimapPanel)
		} else {
			content = mainContent
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, titleBar, content, helpBar)
}

func (f *FileSelector) renderSplitView() string {
	sep := splitSeparatorStyle.Render("│")
	halfW := f.termWidth/2 - 1

	f.leftVP.Width = halfW
	f.leftVP.Height = f.termHeight - 5
	f.rightVP.Width = halfW
	f.rightVP.Height = f.termHeight - 5

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

	b.WriteString("选择要提交的文件 (↑↓/j/k 移动，Space 选择，Enter 确认，A 全选，D 取消，v 合并视图，V 分屏对比，e 添加 gitignore，Tab 折叠文件夹，S 确认，Q 退出)\n\n")

	items := f.buildDisplayList()
	
	// Initialize file list viewport if needed
	if !f.fileListReady && f.termWidth > 0 && f.termHeight > 0 {
		f.fileListVP = viewport.New(f.termWidth-4, f.termHeight-6)
		f.fileListVP.Style = lipgloss.NewStyle().Padding(0, 1)
		f.fileListVP.MouseWheelEnabled = true
		f.fileListReady = true
	}

	for i, item := range items {
		cursor := " "
		if i == f.cursor {
			cursor = cursorStyle.Render(">")
		}

		if item.isFolder {
			// Render folder header
			collapseIcon := "▼"
			if f.collapsedFolders[item.folder] {
				collapseIcon = "▶"
			}
			
			// Count files in this folder
			fileCount := 0
			selectedCount := 0
			for _, fileIdx := range f.getFilesInFolder(item.folder) {
				fileCount++
				if f.selected[fileIdx] {
					selectedCount++
				}
			}
			
			folderLabel := fmt.Sprintf("%s %s %s (%d/%d)", cursor, collapseIcon, item.folder, selectedCount, fileCount)
			b.WriteString(folderStyle.Render(folderLabel) + "\n")
		} else {
			// Render file with indentation
			file := f.files[item.fileIndex]
			checkbox := "[ ]"
			style := normalStyle
			if f.selected[item.fileIndex] {
				checkbox = "[✓]"
				style = selectedStyle
			}

			status := "M"
			if file.Staged {
				status = "S"
			}

			// Get just the filename (without directory)
			filename := file.Path
			if idx := strings.LastIndex(filename, "/"); idx != -1 {
				filename = filename[idx+1:]
			}

			line := fmt.Sprintf("  %s %s %s %s", cursor, checkbox, status, filename)
			b.WriteString(style.Render(line) + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(normalStyle.Render(fmt.Sprintf("已选择：%d/%d", f.countSelected(), len(f.files))))

	if f.gitignoreMsg != "" {
		b.WriteString("\n" + f.gitignoreMsg)
	}

	// Use viewport for file list if ready
	if f.fileListReady {
		f.fileListVP.SetContent(b.String())
		return f.fileListVP.View()
	}

	return b.String()
}

// getFilesInFolder returns the file indices for a given folder
func (f *FileSelector) getFilesInFolder(folder string) []int {
	var indices []int
	for i, file := range f.files {
		if getDirectory(file.Path) == folder {
			indices = append(indices, i)
		}
	}
	return indices
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

// getDirectory returns the directory part of a file path
func getDirectory(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx == -1 {
		return "."
	}
	return path[:idx]
}

// displayItem represents either a folder header or a file in the virtual display list
type displayItem struct {
	isFolder  bool
	folder    string
	fileIndex int // index into f.files, only valid if !isFolder
}

// buildDisplayList creates a virtual list of display items with folder grouping
func (f *FileSelector) buildDisplayList() []displayItem {
	// Group files by directory
	folderFiles := make(map[string][]int)
	var folders []string
	folderSet := make(map[string]bool)

	for i, file := range f.files {
		dir := getDirectory(file.Path)
		if !folderSet[dir] {
			folderSet[dir] = true
			folders = append(folders, dir)
		}
		folderFiles[dir] = append(folderFiles[dir], i)
	}

	// Sort folders for consistent ordering
	sort.Strings(folders)

	// Build display list
	var items []displayItem
	for _, folder := range folders {
		items = append(items, displayItem{isFolder: true, folder: folder})
		if !f.collapsedFolders[folder] {
			for _, fileIdx := range folderFiles[folder] {
				items = append(items, displayItem{isFolder: false, folder: folder, fileIndex: fileIdx})
			}
		}
	}

	return items
}

// getVisibleFileIndex maps cursor position to actual file index, skipping folder headers
func (f *FileSelector) getVisibleFileIndex(cursor int) int {
	items := f.buildDisplayList()
	if cursor >= len(items) {
		return -1
	}
	item := items[cursor]
	if item.isFolder {
		return -1
	}
	return item.fileIndex
}

// moveCursorUp moves cursor up, skipping collapsed folders
func (f *FileSelector) moveCursorUp() {
	items := f.buildDisplayList()
	if f.cursor > 0 {
		f.cursor--
	}
	// Ensure cursor is within bounds
	if f.cursor >= len(items) {
		f.cursor = len(items) - 1
	}
}

// moveCursorDown moves cursor down, skipping collapsed folders
func (f *FileSelector) moveCursorDown() {
	items := f.buildDisplayList()
	if f.cursor < len(items)-1 {
		f.cursor++
	}
}

// toggleFolderCollapse toggles the collapse state of a folder
func (f *FileSelector) toggleFolderCollapse(folder string) {
	f.collapsedFolders[folder] = !f.collapsedFolders[folder]
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

	if os.Getenv("TERM") == "" {
		os.Setenv("TERM", "dumb")
	}

	p := tea.NewProgram(selector)

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
