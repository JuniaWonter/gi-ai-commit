# 三级 Diff 降级策略 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现按 diff 大小预判的三级降级策略，解决大变更场景下的 token 超限问题。

**Architecture:** 新增 `DiffProcessor` 类型封装 diff 处理逻辑，按字节数预判选择完整 diff、压缩摘要或文件级摘要。更新配置结构添加 `diff_prompt` 段，替换 `cmd/commit.go` 中的 `FormatDiffForAI` 调用。

**Tech Stack:** Go, git commands, yaml.v3

---

### Task 1: 添加 DiffPromptConfig 到配置结构

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: 添加 DiffPromptConfig 类型和默认值**

在 `internal/config/config.go` 中添加新类型和默认配置：

```go
type DiffPromptConfig struct {
	MaxFullDiffBytes    int `yaml:"max_full_diff_bytes"`
	MaxCompactDiffBytes int `yaml:"max_compact_diff_bytes"`
	MaxPerFileDiffBytes int `yaml:"max_per_file_diff_bytes"`
	MaxCompactDiffFiles int `yaml:"max_compact_diff_files"`
}
```

更新 `Config` 结构：

```go
type Config struct {
	DeepSeek   DeepSeekConfig   `yaml:"deepseek"`
	Commit     CommitConfig     `yaml:"commit"`
	DiffPrompt DiffPromptConfig `yaml:"diff_prompt"`
}
```

更新 `defaultConfig` 添加默认值：

```go
var defaultConfig = Config{
	DeepSeek: DeepSeekConfig{
		Model:   "deepseek-chat",
		BaseURL: "https://api.deepseek.com",
		Timeout: "30s",
	},
	Commit: CommitConfig{
		DefaultScope: "",
		MaxDiffLines: 500,
	},
	DiffPrompt: DiffPromptConfig{
		MaxFullDiffBytes:    24_000,
		MaxCompactDiffBytes: 16_000,
		MaxPerFileDiffBytes: 2_200,
		MaxCompactDiffFiles: 12,
	},
}
```

在 `Load()` 函数中，在 `yaml.Unmarshal` 之后添加默认值填充逻辑（在 `overrideFromEnv(&config)` 之前）：

```go
config.applyDefaults()
```

添加 `applyDefaults()` 方法：

```go
func (c *Config) applyDefaults() {
	defaults := defaultConfig.DiffPrompt
	if c.DiffPrompt.MaxFullDiffBytes <= 0 {
		c.DiffPrompt.MaxFullDiffBytes = defaults.MaxFullDiffBytes
	}
	if c.DiffPrompt.MaxCompactDiffBytes <= 0 {
		c.DiffPrompt.MaxCompactDiffBytes = defaults.MaxCompactDiffBytes
	}
	if c.DiffPrompt.MaxPerFileDiffBytes <= 0 {
		c.DiffPrompt.MaxPerFileDiffBytes = defaults.MaxPerFileDiffBytes
	}
	if c.DiffPrompt.MaxCompactDiffFiles <= 0 {
		c.DiffPrompt.MaxCompactDiffFiles = defaults.MaxCompactDiffFiles
	}
}
```

- [ ] **Step 2: 更新 config.example.yaml**

在 `config.example.yaml` 末尾添加：

```yaml
# Diff 处理配置（三级降级策略）
diff_prompt:
  # 完整 diff 最大字节数（约 24KB）
  max_full_diff_bytes: 24000
  # 压缩摘要最大字节数（约 16KB）
  max_compact_diff_bytes: 16000
  # 单文件 diff 最大字节数（约 2.2KB）
  max_per_file_diff_bytes: 2200
  # 压缩摘要最大文件数
  max_compact_diff_files: 12
```

- [ ] **Step 3: 验证编译通过**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go config.example.yaml
git commit -m "feat(config): add DiffPromptConfig for three-level diff fallback"
```

---

### Task 2: 创建 DiffProcessor 核心类型

**Files:**
- Create: `internal/diff/processor.go`
- Test: `internal/diff/processor_test.go`

- [ ] **Step 1: 定义 DiffProcessor 类型和数据结构**

创建 `internal/diff/processor.go`：

```go
package diff

import (
	"fmt"
	"os/exec"
	"strings"
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
```

- [ ] **Step 2: 添加 BuildPayloads 方法**

```go
func (p *DiffProcessor) BuildPayloads() ([]DiffPayload, error) {
	fullDiff, err := p.getStagedDiff()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(fullDiff) == "" {
		return nil, nil
	}

	var payloads []DiffPayload

	if len(fullDiff) <= p.cfg.MaxFullDiffBytes {
		payloads = append(payloads, DiffPayload{
			Mode:    "完整 diff",
			Content: fullDiff,
		})
		return payloads, nil
	}

	compact := p.buildCompactDiff(fullDiff)
	if compact != "" {
		stat, _ := p.getCmdOutput("git", "diff", "--cached", "--stat")
		nameStatus, _ := p.getCmdOutput("git", "diff", "--cached", "--name-status")
		payloads = append(payloads, DiffPayload{
			Mode: "压缩摘要",
			Content: fmt.Sprintf(`以下代码变更过大，已自动压缩。请优先依据变更统计、文件列表和关键 patch 生成一条准确的 commit message。

## 变更统计
%s

## 文件列表
%s

## 关键 Patch（已截断）
%s
`, strings.TrimSpace(stat), strings.TrimSpace(nameStatus), compact),
		})
		return payloads, nil
	}

	stat, _ := p.getCmdOutput("git", "diff", "--cached", "--stat")
	nameStatus, _ := p.getCmdOutput("git", "diff", "--cached", "--name-status")
	payloads = append(payloads, DiffPayload{
		Mode: "文件级摘要",
		Content: fmt.Sprintf(`以下代码变更过大，未附带完整 patch。请仅根据变更统计和文件列表生成一条概括性的 commit message。

## 变更统计
%s

## 文件列表
%s
`, strings.TrimSpace(stat), strings.TrimSpace(nameStatus)),
	})

	return payloads, nil
}
```

- [ ] **Step 3: 添加辅助方法**

```go
func (p *DiffProcessor) getStagedDiff() (string, error) {
	return p.getCmdOutput("git", "diff", "--cached", "--no-ext-diff", "--unified=1")
}

func (p *DiffProcessor) getCmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = p.gitDir
	out, err := cmd.Output()
	return string(out), err
}

func (p *DiffProcessor) buildCompactDiff(fullDiff string) string {
	cfg := p.cfg
	if cfg.MaxCompactDiffBytes <= 0 || cfg.MaxCompactDiffFiles <= 0 || cfg.MaxPerFileDiffBytes <= 0 {
		return ""
	}

	parts := strings.Split(fullDiff, "diff --git ")
	if len(parts) <= 1 {
		return truncateText(fullDiff, cfg.MaxCompactDiffBytes)
	}

	numStat, _ := p.getCmdOutput("git", "diff", "--cached", "--numstat")
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
```

- [ ] **Step 4: 添加解析和排序辅助函数**

```go
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
	for i := 0; i < len(sections)-1; i++ {
		best := i
		for j := i + 1; j < len(sections); j++ {
			if sections[j].Score > sections[best].Score ||
				(sections[j].Score == sections[best].Score && sections[j].Path < sections[best].Path) {
				best = j
			}
		}
		if best != i {
			sections[i], sections[best] = sections[best], sections[i]
		}
	}
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
```

- [ ] **Step 5: 编写单元测试**

创建 `internal/diff/processor_test.go`：

```go
package diff

import (
	"testing"
)

func TestParseNumStat(t *testing.T) {
	input := "10\t5\tpkg/file.go\n-\t-\tbinary.bin\n3\t2\tpkg/other.go"
	result := parseNumStat(input)

	if result["pkg/file.go"] != 15 {
		t.Errorf("expected 15, got %d", result["pkg/file.go"])
	}
	if result["binary.bin"] != 0 {
		t.Errorf("expected 0 for binary, got %d", result["binary.bin"])
	}
	if result["pkg/other.go"] != 5 {
		t.Errorf("expected 5, got %d", result["pkg/other.go"])
	}
}

func TestTruncateText(t *testing.T) {
	s := "hello world"
	if truncateText(s, 20) != s {
		t.Error("should not truncate short text")
	}
	result := truncateText(s, 8)
	if len(result) != 8 {
		t.Errorf("expected length 8, got %d", len(result))
	}
}

func TestExtractDiffPath(t *testing.T) {
	section := "diff --git a/pkg/file.go b/pkg/file.go\nindex 1234567..abcdef"
	path := extractDiffPath(section)
	if path != "pkg/file.go" {
		t.Errorf("expected pkg/file.go, got %s", path)
	}
}

func TestSortSections(t *testing.T) {
	sections := []diffSection{
		{Path: "b.go", Score: 5},
		{Path: "a.go", Score: 10},
		{Path: "c.go", Score: 5},
	}
	sortSections(sections)

	if sections[0].Path != "a.go" || sections[0].Score != 10 {
		t.Errorf("first should be a.go with score 10")
	}
	if sections[1].Path != "c.go" {
		t.Errorf("second should be c.go (same score, alphabetical)")
	}
}
```

- [ ] **Step 6: 运行测试**

Run: `go test ./internal/diff/ -v`
Expected: 所有测试通过

- [ ] **Step 7: Commit**

```bash
git add internal/diff/processor.go internal/diff/processor_test.go
git commit -m "feat(diff): add DiffProcessor with three-level fallback strategy"
```

---

### Task 3: 添加按文件列表获取 diff 的方法

**Files:**
- Modify: `internal/diff/processor.go`

- [ ] **Step 1: 添加 BuildPayloadsForFiles 方法**

在 `internal/diff/processor.go` 中添加新方法，支持传入文件列表：

```go
func (p *DiffProcessor) BuildPayloadsForFiles(files []string) ([]DiffPayload, error) {
	var fullDiff string
	var err error

	if len(files) == 0 {
		fullDiff, err = p.getStagedDiff()
	} else {
		args := append([]string{"diff", "--cached", "--no-ext-diff", "--unified=1", "--"}, files...)
		fullDiff, err = p.getCmdOutput("git", args...)
	}

	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(fullDiff) == "" {
		return nil, nil
	}

	var payloads []DiffPayload

	if len(fullDiff) <= p.cfg.MaxFullDiffBytes {
		payloads = append(payloads, DiffPayload{
			Mode:    "完整 diff",
			Content: fullDiff,
		})
		return payloads, nil
	}

	compact := p.buildCompactDiffForFiles(fullDiff, files)
	if compact != "" {
		var stat, nameStatus string
		if len(files) == 0 {
			stat, _ = p.getCmdOutput("git", "diff", "--cached", "--stat")
			nameStatus, _ = p.getCmdOutput("git", "diff", "--cached", "--name-status")
		} else {
			stat, _ = p.getCmdOutput("git", "diff", "--cached", "--stat", "--", files)
			nameStatus, _ = p.getCmdOutput("git", "diff", "--cached", "--name-status", "--", files)
		}
		payloads = append(payloads, DiffPayload{
			Mode: "压缩摘要",
			Content: fmt.Sprintf(`以下代码变更过大，已自动压缩。请优先依据变更统计、文件列表和关键 patch 生成一条准确的 commit message。

## 变更统计
%s

## 文件列表
%s

## 关键 Patch（已截断）
%s
`, strings.TrimSpace(stat), strings.TrimSpace(nameStatus), compact),
		})
		return payloads, nil
	}

	var stat, nameStatus string
	if len(files) == 0 {
		stat, _ = p.getCmdOutput("git", "diff", "--cached", "--stat")
		nameStatus, _ = p.getCmdOutput("git", "diff", "--cached", "--name-status")
	} else {
		stat, _ = p.getCmdOutput("git", "diff", "--cached", "--stat", "--", files)
		nameStatus, _ = p.getCmdOutput("git", "diff", "--cached", "--name-status", "--", files)
	}
	payloads = append(payloads, DiffPayload{
		Mode: "文件级摘要",
		Content: fmt.Sprintf(`以下代码变更过大，未附带完整 patch。请仅根据变更统计和文件列表生成一条概括性的 commit message。

## 变更统计
%s

## 文件列表
%s
`, strings.TrimSpace(stat), strings.TrimSpace(nameStatus)),
	})

	return payloads, nil
}
```

- [ ] **Step 2: 添加 buildCompactDiffForFiles 方法**

```go
func (p *DiffProcessor) buildCompactDiffForFiles(fullDiff string, files []string) string {
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
		numStat, _ = p.getCmdOutput("git", "diff", "--cached", "--numstat")
	} else {
		numStat, _ = p.getCmdOutput("git", "diff", "--cached", "--numstat", "--", files)
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
```

- [ ] **Step 3: 验证编译通过**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 4: Commit**

```bash
git add internal/diff/processor.go
git commit -m "feat(diff): add BuildPayloadsForFiles for selective file diff"
```

---

### Task 4: 集成到 cmd/commit.go

**Files:**
- Modify: `cmd/commit.go`

- [ ] **Step 1: 更新 diff 获取和 AI 调用逻辑**

在 `cmd/commit.go` 中，替换第 89-127 行的 diff 处理和 AI 调用部分。

找到这段代码：

```go
fmt.Println("📊 获取代码变更...")
diffContent, err = getSelectedFilesDiff(selectedFiles)
if err != nil {
	return fmt.Errorf("获取 diff 失败：%w", err)
}

if strings.TrimSpace(diffContent) == "" {
	return fmt.Errorf("选中的文件没有实际变更")
}

fmt.Println("⚙️  加载配置...")
cfg, err = config.Load()
if err != nil {
	return fmt.Errorf("加载配置失败：%w", err)
}

fmt.Println("🤖 初始化 AI 客户端...")
client, err = ai.NewDeepSeekClient(ai.DeepSeekConfig{
	APIKey:  cfg.DeepSeek.APIKey,
	Model:   cfg.DeepSeek.Model,
	BaseURL: cfg.DeepSeek.BaseURL,
	Timeout: cfg.DeepSeek.GetTimeout(),
})
if err != nil {
	return fmt.Errorf("初始化 AI 客户端失败：%w", err)
}

fmt.Println("📋 检查仓库描述...")
desc, _, err = handleDescription(client, diffContent, selectedFiles, cfg)
if err != nil {
	return fmt.Errorf("处理描述失败：%w", err)
}

fmt.Println("🤖 生成 commit message...")
formattedDiff := diff.FormatDiffForAI(diffContent, cfg.Commit.MaxDiffLines)

spinner := loading.New("正在生成 commit message...")
spinner.Start()
commitMessage, err := client.GenerateCommitMessage(formattedDiff, desc)
spinner.Stop("生成完成")
```

替换为：

```go
fmt.Println("⚙️  加载配置...")
cfg, err = config.Load()
if err != nil {
	return fmt.Errorf("加载配置失败：%w", err)
}

fmt.Println("📊 获取代码变更...")
gitRoot, err := getProjectRoot()
if err != nil {
	return fmt.Errorf("获取项目根目录失败：%w", err)
}

diffProcessor := diff.NewDiffProcessor(diff.DiffPromptConfig{
	MaxFullDiffBytes:    cfg.DiffPrompt.MaxFullDiffBytes,
	MaxCompactDiffBytes: cfg.DiffPrompt.MaxCompactDiffBytes,
	MaxPerFileDiffBytes: cfg.DiffPrompt.MaxPerFileDiffBytes,
	MaxCompactDiffFiles: cfg.DiffPrompt.MaxCompactDiffFiles,
}, gitRoot)

payloads, err := diffProcessor.BuildPayloadsForFiles(selectedFiles)
if err != nil || len(payloads) == 0 {
	return fmt.Errorf("没有检测到任何代码变更")
}

diffContent = payloads[0].Content
diffMode := payloads[0].Mode

if strings.TrimSpace(diffContent) == "" {
	return fmt.Errorf("选中的文件没有实际变更")
}

fmt.Println("🤖 初始化 AI 客户端...")
client, err = ai.NewDeepSeekClient(ai.DeepSeekConfig{
	APIKey:  cfg.DeepSeek.APIKey,
	Model:   cfg.DeepSeek.Model,
	BaseURL: cfg.DeepSeek.BaseURL,
	Timeout: cfg.DeepSeek.GetTimeout(),
})
if err != nil {
	return fmt.Errorf("初始化 AI 客户端失败：%w", err)
}

fmt.Println("📋 检查仓库描述...")
desc, _, err = handleDescription(client, diffContent, selectedFiles, cfg)
if err != nil {
	return fmt.Errorf("处理描述失败：%w", err)
}

fmt.Println("🤖 生成 commit message...")
if diffMode != "完整 diff" {
	fmt.Printf("ℹ️  变更较大，已使用 %s 模式\n", diffMode)
}

spinner := loading.New("正在生成 commit message...")
spinner.Start()
commitMessage, err := client.GenerateCommitMessage(diffContent, desc)
spinner.Stop("生成完成")
```

- [ ] **Step 2: 删除不再需要的 getSelectedFilesDiff 函数**

删除 `cmd/commit.go` 中的 `getSelectedFilesDiff` 函数（第 207-220 行）。

- [ ] **Step 3: 验证编译通过**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 4: Commit**

```bash
git add cmd/commit.go
git commit -m "refactor(cmd): integrate DiffProcessor three-level fallback"
```

---

### Task 5: 更新文档和最终验证

**Files:**
- Modify: `README.md`
- Modify: `README.en.md`

- [ ] **Step 1: 更新 README.md 配置说明**

在 `README.md` 的配置说明部分（第 136-150 行），更新为：

```yaml
deepseek:
  api_key: "sk-xxx"      # DeepSeek API Key
  model: "deepseek-chat" # 模型名称
  base_url: "https://api.deepseek.com"
  timeout: "30s"         # 请求超时

commit:
  default_scope: ""      # 默认 scope
  max_diff_lines: 500    # Diff 最大行数限制

diff_prompt:             # 三级降级策略配置
  max_full_diff_bytes: 24000    # 完整 diff 最大字节数
  max_compact_diff_bytes: 16000 # 压缩摘要最大字节数
  max_per_file_diff_bytes: 2200 # 单文件 diff 最大字节数
  max_compact_diff_files: 12    # 压缩摘要最大文件数
```

- [ ] **Step 2: 在 README.md 添加功能说明**

在功能特性列表（第 7-14 行）中添加：

```
- 🔄 智能 diff 处理：三级降级策略，自动适配变更大小
```

- [ ] **Step 3: 运行完整测试**

Run: `go test ./... -v`
Expected: 所有测试通过

Run: `go build -o git-ai-commit .`
Expected: 编译成功

- [ ] **Step 4: Commit**

```bash
git add README.md README.en.md
git commit -m "docs: update README with diff_prompt configuration"
```

---

## Self-Review

**1. Spec coverage:**
- ✅ DiffPromptConfig 添加到配置结构 (Task 1)
- ✅ DiffProcessor 核心类型 (Task 2)
- ✅ 三级降级策略实现 (Task 2)
- ✅ 按文件列表获取 diff (Task 3)
- ✅ 集成到 cmd/commit.go (Task 4)
- ✅ 文档更新 (Task 5)
- ✅ 单元测试 (Task 2)

**2. Placeholder scan:** 无 TBD/TODO，所有步骤包含完整代码。

**3. Type consistency:** `DiffPromptConfig` 在 `internal/config/config.go` 和 `internal/diff/processor.go` 中定义一致（后者为内部类型，通过字段映射传递）。

**4. Scope check:** 聚焦于三级 diff 降级，不包含多模型支持或 context 错误重试（这些是后续优化）。
