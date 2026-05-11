package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// FilePriority 表示一个文件的审查优先级信息
type FilePriority struct {
	Path         string
	ChangeScore  int    // 改动行数
	ReferencedBy int    // 项目中有多少文件引用了这个包
	Priority     int    // 综合优先级分数
	Category     string // core / test / config / generated / other
}

// fileCategory 检测文件类型分类
func fileCategory(path string) string {
	name := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(path))
	dir := strings.ToLower(filepath.Dir(path))

	// 剔除 Vendor
	if strings.Contains(dir, "vendor/") || strings.Contains(dir, "node_modules") || strings.Contains(dir, ".git") {
		return "generated"
	}

	// 生成代码
	if strings.Contains(dir, "/pb/") || strings.Contains(dir, "/proto/") || strings.Contains(dir, "/generated/") || strings.Contains(dir, "/gen/") {
		return "generated"
	}
	if strings.HasSuffix(name, ".pb.go") || strings.HasSuffix(name, ".pb.swift") || strings.HasSuffix(name, ".pyi") || strings.Contains(name, "_generated.") {
		return "generated"
	}
	if ext == ".sql" && strings.Contains(dir, "/migrations") {
		return "generated"
	}

	// 配置文件
	if ext == ".yaml" || ext == ".yml" || ext == ".json" || ext == ".toml" || ext == ".ini" || ext == ".cfg" {
		if !strings.HasSuffix(name, "_test.go") {
			return "config"
		}
	}
	if strings.Contains(name, "dockerfile") || name == "makefile" || name == ".gitignore" || ext == ".mod" || ext == ".sum" || ext == ".lock" {
		return "config"
	}

	// 测试文件
	if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".test.js") {
		return "test"
	}
	if strings.Contains(dir, "/test/") || strings.Contains(dir, "/tests/") || strings.Contains(dir, "/__tests__/") || strings.Contains(dir, "/mock/") {
		return "test"
	}

	// 核心业务代码：internal/ 或核心包目录
	if strings.Contains(dir, "/internal/") || strings.HasPrefix(dir, "internal") {
		return "core"
	}
	coreDirs := []string{"pkg/", "src/", "lib/", "app/", "cmd/", "api/", "service/", "handler/", "controller/", "repository/", "domain/"}
	for _, d := range coreDirs {
		if strings.Contains(dir, d) {
			return "core"
		}
	}

	// 文档
	if ext == ".md" || ext == ".txt" || ext == ".doc" || ext == ".rst" {
		return "config"
	}

	return "other"
}

// categoryWeight 返回文件类型权重乘数
// >1 提高优先级，<1 降低优先级
func categoryWeight(cat string) float64 {
	switch cat {
	case "core":
		return 1.5 // 核心逻辑加分
	case "test":
		return 0.3 // 测试文件降权（review 价值相对低）
	case "config":
		return 0.5 // 配置降权
	case "generated":
		return 0.1 // 生成代码几乎不需要 review
	default:
		return 1.0
	}
}

// GetFilePriorities 计算变更文件的审查优先级。
// 综合考虑改动量、被引用数、文件类型。
func GetFilePriorities() string {
	gitRoot, err := GetGitRoot()
	if err != nil {
		return ""
	}

	// 获取 numstat（每文件的增删行数）
	cmd := exec.Command("git", "diff", "--cached", "--numstat")
	cmd.Dir = gitRoot
	out, _ := cmd.Output()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return ""
	}

	var files []FilePriority
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		added := parseInt(fields[0])
		deleted := parseInt(fields[1])
		path := fields[2]

		score := added + deleted
		if score == 0 {
			continue
		}

		cat := fileCategory(path)
		files = append(files, FilePriority{
			Path:        path,
			ChangeScore: score,
			Category:    cat,
		})
	}

	if len(files) == 0 {
		return ""
	}

	// 计算每个文件被引用的次数（基于包路径的 grep）
	for i, f := range files {
		pkgRefs := countPackageReferences(f.Path, gitRoot)
		files[i].ReferencedBy = pkgRefs
		// 综合评分 = 改动行数 * 类型权重 * 2 + 引用数 * 3
		w := categoryWeight(f.Category)
		files[i].Priority = int(float64(f.ChangeScore*2)*w + float64(pkgRefs*3)*w)
	}

	// 按优先级排序
	sort.Slice(files, func(i, j int) bool {
		if files[i].Priority != files[j].Priority {
			return files[i].Priority > files[j].Priority
		}
		return files[i].Path < files[j].Path
	})

	const maxDisplay = 12
	if len(files) > maxDisplay {
		files = files[:maxDisplay]
	}

	var b strings.Builder
	b.WriteString("\n\n【变更文件优先级（按影响面+类型排序）】\n")
	b.WriteString(fmt.Sprintf("共 %d 个文件，仅展示前 %d 个高优先级文件\n", len(lines), len(files)))
	b.WriteString("优先级标签: [高/中/低] + 文件类型(core/test/config/...)\n")
	for _, f := range files {
		label := "低"
		if f.Priority >= 100 {
			label = "高"
		} else if f.Priority >= 30 {
			label = "中"
		}
		b.WriteString(fmt.Sprintf("  [%s-%s] %s (改动 %d 行, 被 %d 个文件引用)\n",
			label, f.Category, f.Path, f.ChangeScore, f.ReferencedBy))
	}
	b.WriteString("建议优先阅读标记 [高-core] 或高 [中-core] 的文件\n")

	return b.String()
}

func parseInt(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return n
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// countPackageReferences 统计项目中引用指定文件所在包的文件数。
func countPackageReferences(filePath, gitRoot string) int {
	dir := extractPackageDir(filePath)
	if dir == "" {
		return 0
	}

	cmd := exec.Command("grep", "-r", "--include=*.go", "-l",
		fmt.Sprintf(`"%s"`, dir),
		".")
	cmd.Dir = gitRoot

	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	matches := strings.Split(strings.TrimSpace(string(out)), "\n")
	count := 0
	for _, m := range matches {
		if m != "" && !strings.HasPrefix(m, "vendor/") {
			count++
		}
	}
	return count
}

// extractPackageDir 从文件路径中提取包路径部分。
// 如 "internal/service/user.go" → "internal/service"
func extractPackageDir(filePath string) string {
	parts := strings.Split(filePath, "/")
	if len(parts) > 1 {
		return strings.Join(parts[:len(parts)-1], "/")
	}
	return ""
}
