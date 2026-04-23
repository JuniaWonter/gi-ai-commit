package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ConventionInfo struct {
	HookExists      bool
	HookPath        string
	HookContent     string
	TemplateExists  bool
	TemplatePath    string
	TemplateContent string
	RecentMessages  []CommitLogEntry
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

	info.HookPath = filepath.Join(gitRoot, ".git", "hooks", "commit-msg")
	if fileExists(info.HookPath) {
		info.HookExists = true
		content, _ := readFile(info.HookPath)
		info.HookContent = content
	}

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