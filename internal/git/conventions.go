package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ConventionInfo struct {
	HookExists           bool
	HookPath             string
	HookContent          string
	HookRules            []string // 从 hook 脚本中提取的可读规则
	PreCommitHookExists  bool
	PreCommitHookPath    string
	PreCommitHookContent string
	PrepareCommitHookExists  bool
	PrepareCommitHookPath    string
	PrepareCommitHookContent string
	TemplateExists       bool
	TemplatePath         string
	TemplateContent      string
	RecentMessages       []CommitLogEntry
	AllConventionTools   string // 所有检测到的规范工具的格式化摘要
}

type CommitLogEntry struct {
	Hash    string
	Message string
}

func DetectConventions() ConventionInfo {
	info := ConventionInfo{}
	gitRoot, err := getGitRoot()
	if err != nil {
		return info
	}

	hooksDir := filepath.Join(gitRoot, ".git", "hooks")

	// 检测 commit-msg hook（约束 commit message 格式）
	info.HookPath = filepath.Join(hooksDir, "commit-msg")
	if fileExists(info.HookPath) {
		info.HookExists = true
		content, _ := readFile(info.HookPath)
		info.HookContent = content
		info.HookRules = analyzeHookRules(content)
	}

	// 检测 pre-commit hook（约束代码质量，可能间接影响提交格式）
	info.PreCommitHookPath = filepath.Join(hooksDir, "pre-commit")
	if fileExists(info.PreCommitHookPath) {
		info.PreCommitHookExists = true
		content, _ := readFile(info.PreCommitHookPath)
		info.PreCommitHookContent = content
	}

	// 检测 prepare-commit-msg hook（自动填充 commit message 模板）
	info.PrepareCommitHookPath = filepath.Join(hooksDir, "prepare-commit-msg")
	if fileExists(info.PrepareCommitHookPath) {
		info.PrepareCommitHookExists = true
		content, _ := readFile(info.PrepareCommitHookPath)
		info.PrepareCommitHookContent = content
	}

	// 汇总所有规范信息
	info.AllConventionTools = buildConventionSummary(info)

	templatePath, err := getConfig("commit.template")
	if err == nil && templatePath != "" {
		if strings.HasPrefix(templatePath, "~") {
			home, _ := os.UserHomeDir()
			templatePath = filepath.Join(home, templatePath[1:])
		} else if !filepath.IsAbs(templatePath) {
			templatePath = filepath.Join(gitRoot, templatePath)
		}
		info.TemplatePath = templatePath
		if fileExists(templatePath) {
			info.TemplateExists = true
			content, _ := readFile(templatePath)
			info.TemplateContent = content
		}
	}

	info.RecentMessages = getRecentCommits(5)

	return info
}

// analyzeHookRules 分析 hook 脚本内容，提取人类可读的规则摘要。
// 支持常见模式：commitlint、Conventional Commits、issue 引用检查、长度限制等。
func analyzeHookRules(content string) []string {
	var rules []string
	lower := strings.ToLower(content)

	// 1. 检测 Conventional Commits 关键字
	ccKeywords := []string{"conventional", "convention"}
	for _, kw := range ccKeywords {
		if strings.Contains(lower, kw) {
			rules = append(rules, "必须使用 Conventional Commits 格式（type(scope): subject）")
			break
		}
	}

	// 2. 检测允许的 type 列表
	typePatterns := []string{"feat", "fix", "docs", "style", "refactor", "perf", "test", "build", "ci", "chore", "revert"}
	var foundTypes []string
	for _, t := range typePatterns {
		if strings.Contains(lower, t) {
			foundTypes = append(foundTypes, t)
		}
	}
	if len(foundTypes) > 0 {
		rules = append(rules, fmt.Sprintf("允许的 type: %s", strings.Join(foundTypes, ", ")))
	}

	// 3. 检测 commitlint
	if strings.Contains(lower, "commitlint") {
		rules = append(rules, "使用 commitlint 验证 commit message，需符合其配置规则")
	}
	if strings.Contains(lower, "@commitlint") || strings.Contains(lower, "commitlint-config") {
		rules = append(rules, "项目配置了 commitlint 规则")
	}

	// 4. 检测 issue 引用要求
	issuePatterns := []string{"issue", "jira", "redmine", "linear", "#[0-9]", "ref:", "refs", "closes", "fixes"}
	for _, p := range issuePatterns {
		if strings.Contains(lower, p) {
			rules = append(rules, "需要关联 issue/任务编号")
			break
		}
	}

	// 5. 检测行长度限制
	lenPatterns := []string{"72", "50", "length", "maxlen", "max-length", "max length"}
	for _, p := range lenPatterns {
		if strings.Contains(lower, p) {
			rules = append(rules, fmt.Sprintf("commit message 有长度限制（检测到 %s）", p))
			break
		}
	}

	// 6. 检测 scope 限制
	if strings.Contains(lower, "scope") && (strings.Contains(lower, "valid") || strings.Contains(lower, "allow") || strings.Contains(lower, "check")) {
		rules = append(rules, "scope 有特定限制，需查阅 hook 规则")
	}

	// 7. 检测 sign-off 要求
	if strings.Contains(lower, "sign-off") || strings.Contains(lower, "signed-off") {
		rules = append(rules, "需要在 commit message 中添加 Signed-off-by")
	}

	// 8. 检测模板/格式要求
	if strings.Contains(lower, "template") || strings.Contains(lower, "format") {
		if strings.Contains(lower, "regex") || strings.Contains(lower, "pattern") || strings.Contains(lower, "match") {
			rules = append(rules, "commit message 必须匹配特定的正则表达式模式")
		}
	}

	// 9. 检测 changelog 相关
	if strings.Contains(lower, "changelog") {
		rules = append(rules, "commit message 会影响 changelog 生成")
	}

	// 10. 检测 pre-commit 对代码格式的约束（非直接提交约束，但影响提交质量）
	if strings.Contains(lower, "gofmt") || strings.Contains(lower, "go fmt") {
		rules = append(rules, "代码需要经过 gofmt 格式化（pre-commit 强制）")
	}
	if strings.Contains(lower, "golint") || strings.Contains(lower, "golangci") {
		rules = append(rules, "代码需要通过 golangci-lint 检查（pre-commit 强制）")
	}

	if len(rules) == 0 {
		rules = append(rules, "项目配置了自定义 hook 规则，提交失败时请根据错误信息调整")
	}

	return rules
}

// buildConventionSummary 生成所有检测到的规范工具的格式化摘要。
func buildConventionSummary(info ConventionInfo) string {
	var b strings.Builder

	if info.HookExists {
		b.WriteString("commit-msg hook: 存在")
		if len(info.HookRules) > 0 {
			b.WriteString("\n  规则:\n")
			for _, rule := range info.HookRules {
				b.WriteString(fmt.Sprintf("    • %s\n", rule))
			}
		}
	}

	if info.PreCommitHookExists {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("pre-commit hook: 存在（代码质量检查，可能影响提交流程）")
	}

	if info.PrepareCommitHookExists {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("prepare-commit-msg hook: 存在（自动填充 commit message 模板）")
	}

	if info.TemplateExists {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("commit template: %s", info.TemplatePath))
	}

	if b.Len() > 0 {
		b.WriteString("\ncommit message 必须通过所有 hook 验证才能提交成功")
	} else {
		b.WriteString("未检测到项目级别的提交规范约束")
	}

	return b.String()
}

func GetConfigValue(key string) (string, error) {
	return getConfig(key)
}

func GetRecentCommits(count int) []CommitLogEntry {
	return getRecentCommits(count)
}

func getConfig(key string) (string, error) {
	cmd := exec.Command("git", "config", "--get", key)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func getRecentCommits(count int) []CommitLogEntry {
	cmd := exec.Command("git", "log", fmt.Sprintf("-%d", count), "--pretty=format:%h %s")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var entries []CommitLogEntry
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) >= 2 {
			entries = append(entries, CommitLogEntry{
				Hash:    parts[0],
				Message: parts[1],
			})
		}
	}
	return entries
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}