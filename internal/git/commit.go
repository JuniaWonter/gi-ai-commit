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

// GetGitRoot returns the git repository root directory.
// Simple implementation without caching to avoid issues with failed calls being cached.
func GetGitRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("获取 git 根目录失败：%w (stderr: %s)", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func getGitRoot() (string, error) {
	return GetGitRoot()
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

// VerifiedCommitResult holds the result of post-commit verification.
type VerifiedCommitResult struct {
	Hash            string   // verified HEAD hash
	RemainingStaged []string // files still staged after commit (unexpected)
	RemainingDirty  []string // files with unstaged modifications
	UntrackedFiles  []string // untracked files
	IsPartial       bool     // true if there are remaining dirty files
	Verified        bool     // true if verification ran successfully
	Error           string   // error message if verification failed
}

// VerifyCommit runs git commands to independently verify that a commit was made
// and reports the current state of the working tree.
func VerifyCommit() VerifiedCommitResult {
	gitRoot, err := getGitRoot()
	if err != nil {
		return VerifiedCommitResult{Error: err.Error()}
	}

	// Get current HEAD hash
	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = gitRoot
	headOut, err := headCmd.Output()
	if err != nil {
		return VerifiedCommitResult{Error: fmt.Sprintf("获取 HEAD 失败：%v", err)}
	}
	hash := strings.TrimSpace(string(headOut))
	if hash == "" {
		return VerifiedCommitResult{Error: "HEAD 为空，提交可能未创建"}
	}

	// Check remaining staged files (should be empty after successful commit)
	stagedCmd := exec.Command("git", "diff", "--cached", "--name-only")
	stagedCmd.Dir = gitRoot
	stagedOut, _ := stagedCmd.Output()
	var remainingStaged []string
	for _, f := range strings.Split(strings.TrimSpace(string(stagedOut)), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			remainingStaged = append(remainingStaged, f)
		}
	}

	// Check unstaged modified files
	dirtyCmd := exec.Command("git", "diff", "--name-only")
	dirtyCmd.Dir = gitRoot
	dirtyOut, _ := dirtyCmd.Output()
	var remainingDirty []string
	for _, f := range strings.Split(strings.TrimSpace(string(dirtyOut)), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			remainingDirty = append(remainingDirty, f)
		}
	}

	// Check untracked files
	untrackedCmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	untrackedCmd.Dir = gitRoot
	untrackedOut, _ := untrackedCmd.Output()
	var untrackedFiles []string
	for _, f := range strings.Split(strings.TrimSpace(string(untrackedOut)), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			untrackedFiles = append(untrackedFiles, f)
		}
	}

	isPartial := len(remainingDirty) > 0

	return VerifiedCommitResult{
		Hash:            hash,
		RemainingStaged: remainingStaged,
		RemainingDirty:  remainingDirty,
		UntrackedFiles:  untrackedFiles,
		IsPartial:       isPartial,
		Verified:        true,
		Error:           "",
	}
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