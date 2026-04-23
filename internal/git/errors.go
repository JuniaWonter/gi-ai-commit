package git

import (
	"strings"
)

type ErrorCategory int

const (
	ErrorRecoverable ErrorCategory = iota
	ErrorUnrecoverable
	ErrorUnknown
)

type ClassifiedError struct {
	Category    ErrorCategory
	Message     string
	Suggestion  string
	RawStderr   string
}

func ClassifyCommitError(stderr string) ClassifiedError {
	stderrLower := strings.ToLower(stderr)

	if strings.Contains(stderrLower, "nothing to commit") ||
		strings.Contains(stderrLower, "no changes added to commit") {
		return ClassifiedError{
			Category:   ErrorUnrecoverable,
			Message:    "没有变更可提交",
			Suggestion: "请先使用 git add 暽存变更",
			RawStderr:  stderr,
		}
	}

	if strings.Contains(stderrLower, "hook") && strings.Contains(stderrLower, "reject") {
		suggestion := "commit message 不符合 hook 约束，需要调整格式"
		if strings.Contains(stderrLower, "conventional") ||
			strings.Contains(stderrLower, "convention") {
			suggestion = "需要使用 Conventional Commits 格式（如 feat/fix/docs 等）"
		}
		return ClassifiedError{
			Category:   ErrorRecoverable,
			Message:    "commit-msg hook 拒绝了提交",
			Suggestion: suggestion,
			RawStderr:  stderr,
		}
	}

	if strings.Contains(stderrLower, "invalid") && strings.Contains(stderrLower, "format") {
		return ClassifiedError{
			Category:   ErrorRecoverable,
			Message:    "commit message 格式无效",
			Suggestion: "根据错误信息调整 commit message 格式",
			RawStderr:  stderr,
		}
	}

	if strings.Contains(stderrLower, "merge conflict") ||
		strings.Contains(stderrLower, "conflict") {
		return ClassifiedError{
			Category:   ErrorUnrecoverable,
			Message:    "存在未解决的合并冲突",
			Suggestion: "请先解决合并冲突后再提交",
			RawStderr:  stderr,
		}
	}

	if strings.Contains(stderrLower, "failed") && strings.Contains(stderrLower, "commit") {
		return ClassifiedError{
			Category:   ErrorRecoverable,
			Message:    "提交失败",
			Suggestion: "根据错误信息修改 commit message 并重试",
			RawStderr:  stderr,
		}
	}

	if stderr != "" {
		return ClassifiedError{
			Category:   ErrorUnknown,
			Message:    "提交失败（未知原因）",
			Suggestion: "请查看错误信息并手动处理",
			RawStderr:  stderr,
		}
	}

	return ClassifiedError{
		Category:   ErrorUnknown,
		Message:    "提交失败",
		Suggestion: "请查看错误信息并手动处理",
		RawStderr:  stderr,
	}
}