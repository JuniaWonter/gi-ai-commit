package diff

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oliver/git-ai-commit/internal/debug"
)

const (
	truncatedDiffNotice = "\n[...内容已截断...]"
	truncatedFileNotice = "\n[...该文件 diff 已截断...]"
)

type DiffPromptConfig struct {
	MaxFullDiffBytes    int
	MaxCompactDiffBytes int
	MaxPerFileDiffBytes int
	MaxCompactDiffFiles int
}

type DiffPayload struct {
	Mode    string
	Content string
}

type diffSection struct {
	Path    string
	Content string
	Score   int
}

type DiffProcessor struct {
	cfg    DiffPromptConfig
	gitDir string
}

func NewDiffProcessor(cfg DiffPromptConfig, gitDir string) *DiffProcessor {
	return &DiffProcessor{
		cfg:    cfg,
		gitDir: gitDir,
	}
}

func (p *DiffProcessor) BuildPayloads() ([]DiffPayload, error) {
	fullDiff, err := p.getStagedDiff()
	if err != nil {
		return nil, err
	}
	return p.buildPayloadsFromDiff(fullDiff, nil)
}

func (p *DiffProcessor) BuildPayloadsForFiles(files []string) ([]DiffPayload, error) {
	var fullDiff string
	var err error
	debug.Logf("processor.BuildPayloadsForFiles begin gitDir=%s fileCount=%d", p.gitDir, len(files))

	if len(files) == 0 {
		fullDiff, err = p.getAllDiff()
	} else {
		args := append([]string{"diff", "--cached", "--no-ext-diff", "--unified=1", "--"}, files...)
		fullDiff, err = p.getCmdOutput("git", args...)
	}
	if err != nil {
		debug.Logf("processor.BuildPayloadsForFiles failed err=%v", err)
		return nil, err
	}
	debug.Logf("processor.BuildPayloadsForFiles diffBytes=%d", len(fullDiff))
	return p.buildPayloadsFromDiff(fullDiff, files)
}

func (p *DiffProcessor) getAllDiff() (string, error) {
	cached, _ := p.getCmdOutput("git", "diff", "--cached", "--no-ext-diff", "--unified=1")
	unstaged, _ := p.getCmdOutput("git", "diff", "--no-ext-diff", "--unified=1")

	diff := strings.TrimSpace(cached) + "\n" + strings.TrimSpace(unstaged)
	diff = strings.TrimSpace(diff)

	if diff == "" {
		changedFiles, _ := GetChangedFiles()
		var b strings.Builder
		for _, f := range changedFiles {
			d, _, _ := GetFileDiffFull(f.Path, false)
			if d != "" {
				b.WriteString(d + "\n")
			}
		}
		diff = strings.TrimSpace(b.String())
	}

	if diff == "" {
		return "", fmt.Errorf("没有检测到任何代码变更")
	}
	return diff, nil
}

func (p *DiffProcessor) buildPayloadsFromDiff(fullDiff string, files []string) ([]DiffPayload, error) {
	if strings.TrimSpace(fullDiff) == "" {
		return nil, nil
	}

	var payloads []DiffPayload

	// 获取文件分组索引（大变更集时注入）
	_, nameStatus := p.getDiffStatAndNameStatus(files)
	dirIndex := p.buildDirIndex(nameStatus)

	if len(fullDiff) <= p.cfg.MaxFullDiffBytes {
		content := fullDiff
		if dirIndex != "" {
			content = dirIndex + "\n\n" + content
		}
		payloads = append(payloads, DiffPayload{
			Mode:    "完整 diff",
			Content: content,
		})
		return payloads, nil
	}

	compact := p.buildCompactDiffInternal(fullDiff, files)
	if compact != "" {
		stat, nameStatus := p.getDiffStatAndNameStatus(files)
		contentCompact := fmt.Sprintf(`以下代码变更过大，已自动压缩。请优先依据变更统计、文件列表和关键 patch 生成一条准确的 commit message。

## 变更统计
%s

## 文件列表
%s

## 关键 Patch（已截断）
%s
`, strings.TrimSpace(stat), strings.TrimSpace(nameStatus), compact)
		if dirIndex != "" {
			contentCompact = dirIndex + "\n\n" + contentCompact
		}
		payloads = append(payloads, DiffPayload{
			Mode: "压缩摘要",
			Content: contentCompact,
		})
		return payloads, nil
	}

	stat, nameStatus := p.getDiffStatAndNameStatus(files)
	contentFile := fmt.Sprintf(`以下代码变更过大，仅包含文件列表。请先用 diff_overview 了解概览，再用 read_diff(<文件路径>) 读取关键文件的变更，用 read_file 读取代码上下文。

## 变更统计
%s

## 文件列表
%s
`, strings.TrimSpace(stat), strings.TrimSpace(nameStatus))
	if dirIndex != "" {
		contentFile = dirIndex + "\n\n" + contentFile
	}
	payloads = append(payloads, DiffPayload{
		Mode: "文件级摘要",
		Content: contentFile,
	})

	return payloads, nil
}

// buildDirIndex 按目录分组变更文件，生成可读的分组索引。
// 帮助 AI 快速理解文件间的模块归属关系。
func (p *DiffProcessor) buildDirIndex(nameStatus string) string {
	if nameStatus == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(nameStatus), "\n")
	if len(lines) <= 3 {
		return ""
	}

	type dirEntry struct {
		files []string
		total int
	}
	dirs := make(map[string]*dirEntry)
	dirOrder := make([]string, 0)

	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		path := parts[len(parts)-1]
		dir := filepath.Dir(path)
		if dir == "." {
			dir = "/"
		}
		if _, ok := dirs[dir]; !ok {
			dirs[dir] = &dirEntry{}
			dirOrder = append(dirOrder, dir)
		}
		dirs[dir].files = append(dirs[dir].files, path)
		dirs[dir].total++
	}

	var b strings.Builder
	b.WriteString("\n## 变更文件分组（按目录）\n")
	for _, dir := range dirOrder {
		e := dirs[dir]
		// 列出文件名，过多时只列前5个
		fileList := e.files
		if len(fileList) > 5 {
			fileList = fileList[:5]
		}
		b.WriteString(fmt.Sprintf("- %s/ (%d files): %s",
			dir, e.total, strings.Join(fileList, ", ")))
		if len(e.files) > 5 {
			b.WriteString(fmt.Sprintf(" ... 其他 %d 个", len(e.files)-5))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (p *DiffProcessor) getDiffStatAndNameStatus(files []string) (string, string) {
	var stat, nameStatus string
	if len(files) == 0 {
		stat, _ = p.getCmdOutput("git", "diff", "HEAD", "--stat")
		nameStatus, _ = p.getCmdOutput("git", "diff", "HEAD", "--name-status")
	} else {
		statArgs := append([]string{"diff", "HEAD", "--stat", "--"}, files...)
		stat, _ = p.getCmdOutput("git", statArgs...)
		nameStatusArgs := append([]string{"diff", "HEAD", "--name-status", "--"}, files...)
		nameStatus, _ = p.getCmdOutput("git", nameStatusArgs...)
	}
	return stat, nameStatus
}

func (p *DiffProcessor) getStagedDiff() (string, error) {
	return p.getCmdOutput("git", "diff", "--cached", "--no-ext-diff", "--unified=1")
}

func (p *DiffProcessor) getCmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = p.gitDir
	out, err := cmd.Output()
	if err != nil {
		debug.Logf("processor.getCmdOutput failed cmd=%s args=%v dir=%s err=%v", name, args, p.gitDir, err)
	} else {
		debug.Logf("processor.getCmdOutput ok cmd=%s args=%v dir=%s outBytes=%d", name, args, p.gitDir, len(out))
	}
	return string(out), err
}

func (p *DiffProcessor) buildCompactDiffInternal(fullDiff string, files []string) string {
	cfg := p.cfg
	if cfg.MaxCompactDiffBytes <= 0 || cfg.MaxCompactDiffFiles <= 0 || cfg.MaxPerFileDiffBytes <= 0 {
		return ""
	}

	parts := strings.Split(fullDiff, "diff --git ")
	if len(parts) <= 1 {
		return truncateText(fullDiff, cfg.MaxCompactDiffBytes)
	}

	var numStat string
	if len(files) == 0 {
		numStat, _ = p.getCmdOutput("git", "diff", "HEAD", "--numstat")
	} else {
		numStatArgs := append([]string{"diff", "HEAD", "--numstat", "--"}, files...)
		numStat, _ = p.getCmdOutput("git", numStatArgs...)
	}
	scores := parseNumStat(numStat)

	sections := make([]diffSection, 0, len(parts)-1)
	totalFiles := 0
	for _, part := range parts[1:] {
		section := strings.TrimSpace("diff --git " + part)
		if section == "" {
			continue
		}
		path := extractDiffPath(section)
		totalFiles++
		sections = append(sections, diffSection{
			Path:    path,
			Content: section,
			Score:   scores[path],
		})
	}
	sortSections(sections)

	var b strings.Builder
	fileCount := 0
	for _, section := range sections {
		if fileCount >= cfg.MaxCompactDiffFiles || b.Len() >= cfg.MaxCompactDiffBytes {
			continue
		}

		remaining := cfg.MaxCompactDiffBytes - b.Len()
		if remaining <= len(truncatedDiffNotice) {
			break
		}

		sectionText := truncateText(section.Content, cfg.MaxPerFileDiffBytes)
		if len(sectionText) > remaining {
			sectionText = truncateText(sectionText, remaining)
		}
		if strings.TrimSpace(sectionText) == "" {
			continue
		}

		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(sectionText)
		if len(section.Content) > len(sectionText) && !strings.HasSuffix(sectionText, truncatedDiffNotice) {
			b.WriteString(truncatedFileNotice)
		}
		fileCount++
	}

	if totalFiles > fileCount {
		fmt.Fprintf(&b, "\n\n[...其余 %d 个文件已省略...]", totalFiles-fileCount)
	}

	return strings.TrimSpace(b.String())
}

func parseNumStat(numStat string) map[string]int {
	scores := make(map[string]int)
	for _, line := range strings.Split(numStat, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		score := parseNumStatValue(fields[0]) + parseNumStatValue(fields[1])
		path := strings.Join(fields[2:], " ")
		scores[path] = score
	}
	return scores
}

func parseNumStatValue(v string) int {
	if v == "-" {
		return 0
	}
	value := 0
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return value
		}
		value = value*10 + int(ch-'0')
	}
	return value
}

func extractDiffPath(section string) string {
	firstLine, _, ok := strings.Cut(section, "\n")
	if !ok {
		firstLine = section
	}
	fields := strings.Fields(firstLine)
	if len(fields) < 4 {
		return ""
	}
	return strings.TrimPrefix(fields[3], "b/")
}

func sortSections(sections []diffSection) {
	sort.Slice(sections, func(i, j int) bool {
		if sections[i].Score != sections[j].Score {
			return sections[i].Score > sections[j].Score
		}
		return sections[i].Path < sections[j].Path
	})
}

func truncateText(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	if limit <= len(truncatedDiffNotice) {
		return s[:limit]
	}
	return s[:limit-len(truncatedDiffNotice)] + truncatedDiffNotice
}
