package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

type CommitResult struct {
	Success bool
	Hash    string
	Error   string
	Stderr  string
}

func Commit(message string) CommitResult {
	gitRoot, err := getGitRoot()
	if err != nil {
		return CommitResult{Success: false, Error: err.Error()}
	}

	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = gitRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return CommitResult{
			Success: false,
			Error:   err.Error(),
			Stderr:  stderr.String(),
		}
	}

	hash := parseCommitHash(stdout.String())
	return CommitResult{
		Success: true,
		Hash:    hash,
	}
}

func CommitAmend(message string) CommitResult {
	gitRoot, err := getGitRoot()
	if err != nil {
		return CommitResult{Success: false, Error: err.Error()}
	}

	cmd := exec.Command("git", "commit", "--amend", "-m", message)
	cmd.Dir = gitRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return CommitResult{
			Success: false,
			Error:   err.Error(),
			Stderr:  stderr.String(),
		}
	}

	hash := parseCommitHash(stdout.String())
	return CommitResult{
		Success: true,
		Hash:    hash,
	}
}

func parseCommitHash(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.Contains(line, " ") {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) >= 1 {
				hash := strings.TrimPrefix(parts[0], "[")
				hash = strings.TrimSuffix(hash, "]")
				if len(hash) >= 7 {
					return hash
				}
			}
		}
	}
	return ""
}

func ResetLastCommit() CommitResult {
	gitRoot, err := getGitRoot()
	if err != nil {
		return CommitResult{Success: false, Error: err.Error()}
	}

	cmd := exec.Command("git", "reset", "--soft", "HEAD~1")
	cmd.Dir = gitRoot

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return CommitResult{
			Success: false,
			Error:   err.Error(),
			Stderr:  stderr.String(),
		}
	}

	return CommitResult{Success: true}
}

func GetGitRoot() (string, error) {
	return getGitRoot()
}

func getGitRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("获取 git 根目录失败：%w", err)
	}
	return strings.TrimSpace(string(output)), nil
}