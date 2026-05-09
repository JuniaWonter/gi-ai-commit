package git

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// FilePriority 表示一个文件的审查优先级信息
type FilePriority struct {
	Path         string
	ChangeScore  int // 改动行数
	ReferencedBy int // 项目中有多少文件引用了这个包
	Priority     int // 综合优先级分数
}

// GetFilePriorities 计算变更文件的审查优先级。
// 综合考虑改动量和被引用数，帮助 AI 决定哪些文件优先阅读。
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

		fp := FilePriority{
			Path:        path,
			ChangeScore: score,
		}
		files = append(files, fp)
	}

	if len(files) == 0 {
		return ""
	}

	// 计算每个文件被引用的次数（基于包路径的 grep）
	for i, f := range files {
		pkgRefs := countPackageReferences(f.Path, gitRoot)
		files[i].ReferencedBy = pkgRefs
		files[i].Priority = f.ChangeScore*2 + pkgRefs*3
	}

	// 按优先级排序
	sort.Slice(files, func(i, j int) bool {
		return files[i].Priority > files[j].Priority
	})

	// 只取前 8 个
	if len(files) > 8 {
		files = files[:8]
	}

	var b strings.Builder
	b.WriteString("\n\n【变更文件优先级（按影响面排序）】\n")
	for _, f := range files {
		label := "低"
		if f.Priority >= 50 {
			label = "高"
		} else if f.Priority >= 20 {
			label = "中"
		}
		b.WriteString(fmt.Sprintf("  [%s] %s (改动 %d 行, 被 %d 个文件引用)\n",
			label, f.Path, f.ChangeScore, f.ReferencedBy))
	}
	b.WriteString("建议优先阅读标记 [高] 的文件\n")

	return b.String()
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// countPackageReferences 统计项目中引用指定文件所在包的文件数。
func countPackageReferences(filePath, gitRoot string) int {
	// 从文件路径推断包导入路径
	// 如 internal/service/user.go → 搜索 import "*service" 或 import "*service/user"
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
		// grep exit code 1 = no matches
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
	// Go 包的惯例：最后一个 / 前的部分是包目录
	if len(parts) > 1 {
		return strings.Join(parts[:len(parts)-1], "/")
	}
	return ""
}
