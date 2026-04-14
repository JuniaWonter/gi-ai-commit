package project

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ProjectInfo struct {
	Language     string
	Dependencies []string
	RootFiles    []string
	DirStructure string
	MainFiles    []string
}

func Analyze(rootDir string) (*ProjectInfo, error) {
	info := &ProjectInfo{}

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, err
	}

	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "node_modules" && e.Name() != "vendor" && e.Name() != "venv" {
			dirs = append(dirs, e.Name())
		}
		info.RootFiles = append(info.RootFiles, e.Name())
	}

	info.DirStructure = strings.Join(dirs, ", ")

	if err := detectLanguage(rootDir, info); err != nil {
		return nil, err
	}

	if err := extractDependencies(rootDir, info); err != nil {
		return nil, err
	}

	info.MainFiles = findMainFiles(rootDir)

	return info, nil
}

func detectLanguage(rootDir string, info *ProjectInfo) error {
	files, err := os.ReadDir(rootDir)
	if err != nil {
		return err
	}

	extCounts := make(map[string]int)
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		ext := filepath.Ext(f.Name())
		if ext != "" {
			extCounts[ext]++
		}
	}

	languageExts := map[string][]string{
		"Go":         {".go"},
		"JavaScript": {".js", ".jsx", ".mjs"},
		"TypeScript": {".ts", ".tsx"},
		"Python":     {".py"},
		"Java":       {".java"},
		"C/C++":      {".c", ".cpp", ".h", ".hpp"},
		"Rust":       {".rs"},
		"Ruby":       {".rb"},
		"PHP":        {".php"},
		"Swift":      {".swift"},
		"Kotlin":     {".kt", ".kts"},
	}

	maxCount := 0
	for lang, exts := range languageExts {
		count := 0
		for _, ext := range exts {
			count += extCounts[ext]
		}
		if count > maxCount {
			maxCount = count
			info.Language = lang
		}
	}

	if info.Language == "" {
		info.Language = "Unknown"
	}

	return nil
}

func extractDependencies(rootDir string, info *ProjectInfo) error {
	depFiles := []string{
		"go.mod",
		"package.json",
		"requirements.txt",
		"Pipfile",
		"Cargo.toml",
		"pom.xml",
		"build.gradle",
		"Gemfile",
		"composer.json",
		"Cargo.toml",
	}

	for _, depFile := range depFiles {
		path := filepath.Join(rootDir, depFile)
		if data, err := os.ReadFile(path); err == nil {
			info.Dependencies = append(info.Dependencies, parseDependencies(depFile, string(data))...)
		}
	}

	return nil
}

func parseDependencies(filename, content string) []string {
	var deps []string

	switch filename {
	case "go.mod":
		scanner := bufio.NewScanner(strings.NewReader(content))
		inRequire := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "require (") {
				inRequire = true
				continue
			}
			if inRequire && line == ")" {
				break
			}
			if inRequire && line != "" {
				deps = append(deps, strings.Fields(line)[0])
			}
		}

	case "package.json":
		var buf bytes.Buffer
		buf.WriteString(content)
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if strings.Contains(content, "\"dependencies\"") {
			for k := range pkg.Dependencies {
				deps = append(deps, k)
			}
		}

	case "requirements.txt":
		scanner := bufio.NewScanner(strings.NewReader(content))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" && !strings.HasPrefix(line, "#") {
				pkg := strings.Split(line, "==")[0]
				pkg = strings.Split(line, ">=")[0]
				deps = append(deps, strings.Fields(pkg)[0])
			}
		}

	case "Cargo.toml":
		inDeps := false
		scanner := bufio.NewScanner(strings.NewReader(content))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "[dependencies]") || strings.HasPrefix(line, "[dev-dependencies]") {
				inDeps = true
				continue
			}
			if inDeps && strings.HasPrefix(line, "[") {
				break
			}
			if inDeps && line != "" && !strings.HasPrefix(line, "#") {
				name := strings.Fields(line)[0]
				deps = append(deps, name)
			}
		}
	}

	if len(deps) > 10 {
		deps = deps[:10]
		deps = append(deps, "...")
	}

	return deps
}

func findMainFiles(rootDir string) []string {
	mainPatterns := []string{
		"main.go", "app.go", "server.go",
		"index.js", "app.js", "server.js", "main.js",
		"main.py", "app.py", "run.py",
		"main.rs", "lib.rs",
		"main.java", "App.java",
		"main.swift", "main.kt",
	}

	var mains []string
	for _, pattern := range mainPatterns {
		path := filepath.Join(rootDir, pattern)
		if _, err := os.Stat(path); err == nil {
			mains = append(mains, pattern)
		}
	}

	return mains
}

func GetSummary(rootDir string) (string, error) {
	info, err := Analyze(rootDir)
	if err != nil {
		return "", err
	}

	gitRemote := getGitRemote()

	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("项目类型：%s\n", info.Language))

	if gitRemote != "" {
		summary.WriteString(fmt.Sprintf("仓库地址：%s\n", gitRemote))
	}

	if info.DirStructure != "" {
		summary.WriteString(fmt.Sprintf("目录结构：%s\n", info.DirStructure))
	}

	if len(info.MainFiles) > 0 {
		summary.WriteString(fmt.Sprintf("主入口文件：%s\n", strings.Join(info.MainFiles, ", ")))
	}

	if len(info.Dependencies) > 0 {
		summary.WriteString(fmt.Sprintf("主要依赖：%s\n", strings.Join(info.Dependencies, ", ")))
	}

	return summary.String(), nil
}

func getGitRemote() string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func GetFileSummary(rootDir string, files []string) (string, error) {
	info, err := Analyze(rootDir)
	if err != nil {
		return "", err
	}

	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("语言：%s\n", info.Language))

	if len(files) > 0 {
		var exts []string
		for _, f := range files {
			ext := filepath.Ext(f)
			if ext != "" && !contains(exts, ext) {
				exts = append(exts, ext)
			}
		}
		summary.WriteString(fmt.Sprintf("变更文件类型：%s\n", strings.Join(exts, ", ")))
		summary.WriteString(fmt.Sprintf("变更文件数：%d\n", len(files)))
	}

	return summary.String(), nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
