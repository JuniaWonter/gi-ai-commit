package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const sessionFileName = "ai-session.json"
const sessionMaxAge = 7 * 24 * time.Hour
const sessionVersion = 1

// PersistableSession 可持久化的会话状态
type PersistableSession struct {
	Version     int                          `json:"version"`
	Model       string                       `json:"model"`
	Branch      string                       `json:"branch"`
	LastHash    string                       `json:"last_commit_hash,omitempty"`
	CompactMode bool                         `json:"compact_mode"`
	Messages    []openai.ChatCompletionMessage `json:"messages"`
	CreatedAt   time.Time                    `json:"created_at"`
}

// sessionPath 返回会话文件路径 (.git/ai-session.json)
func sessionPath() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("获取 git 目录失败：%w", err)
	}
	gitDir := strings.TrimSpace(string(out))
	return filepath.Join(gitDir, sessionFileName), nil
}

// currentBranch 返回当前分支名
func currentBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// lastCommitHash 返回最新 commit hash
func lastCommitHash() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SaveSession 持久化当前会话到 .git/ai-session.json
func (s *CommitSession) SaveSession() error {
	path, err := sessionPath()
	if err != nil {
		return err
	}

	sess := PersistableSession{
		Version:     sessionVersion,
		Model:       s.client.config.Model,
		Branch:      currentBranch(),
		CompactMode: s.compactMode,
		Messages:    s.messages,
		CreatedAt:   time.Now(),
	}

	// 保存时压缩消息，丢弃过期的工具结果
	compacted := compressMessagesForSave(sess.Messages)
	sess.Messages = compacted

	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化会话失败：%w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("写入会话文件失败：%w", err)
	}
	return nil
}

// LoadSession 从 .git/ai-session.json 加载会话
func LoadSession(modelName string) (*PersistableSession, error) {
	path, err := sessionPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("没有找到之前的会话（%s）", path)
		}
		return nil, fmt.Errorf("读取会话文件失败：%w", err)
	}

	var sess PersistableSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("解析会话文件失败：%w", err)
	}

	// 版本检查
	if sess.Version != sessionVersion {
		return nil, fmt.Errorf("会话版本不兼容（文件: %d, 期望: %d），请重新开始", sess.Version, sessionVersion)
	}

	// 过期检查
	if time.Since(sess.CreatedAt) > sessionMaxAge {
		return nil, fmt.Errorf("会话已过期（超过 %d 小时），请重新开始", 7*24)
	}

	// 分支检查
	current := currentBranch()
	if sess.Branch != "" && sess.Branch != current {
		return nil, fmt.Errorf("分支不匹配（会话: %s, 当前: %s），请重新开始", sess.Branch, current)
	}

	// 模型检查
	if sess.Model != "" && sess.Model != modelName {
		return nil, fmt.Errorf("模型不匹配（会话: %s, 当前: %s），请重新开始", sess.Model, modelName)
	}

	return &sess, nil
}

// ClearSession 删除持久化会话文件
func ClearSession() error {
	path, err := sessionPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// compressMessagesForSave 压缩消息用于持久化存储。
// 丢弃过期工具结果，保留 AI 推理和关键信息。
func compressMessagesForSave(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) <= 2 {
		return messages
	}

	var keep []openai.ChatCompletionMessage
	keep = append(keep, messages[0]) // system

	// 保留所有 AI 回复和用户消息，但丢弃过期的工具结果
	// read_file、diff_overview、list_tree 结果只保留最近的 2 轮
	recentToolRounds := 0

	for i := 1; i < len(messages); i++ {
		msg := messages[i]

		if msg.Role == openai.ChatMessageRoleAssistant {
			keep = append(keep, msg)
		} else if msg.Role == openai.ChatMessageRoleUser {
			keep = append(keep, msg)
		} else if msg.Role == openai.ChatMessageRoleTool {
			// 判断这个 tool result 对应的 tool 类型
			isReadFile := strings.Contains(msg.Content, "file:")
			isListTree := strings.Contains(msg.Content, "PROJECT TREE") || strings.Contains(msg.Content, "PROJECT TREE")
			isDiffOverview := strings.Contains(msg.Content, "变更统计")

			if isReadFile || isListTree || isDiffOverview {
				// 只保留最近 2 轮的工具结果
				recentToolRounds++
				if recentToolRounds <= 2 {
					keep = append(keep, msg)
				}
			} else {
				// git_commit、report_review 等关键结果全部保留
				keep = append(keep, msg)
			}
		} else {
			keep = append(keep, msg)
		}
	}

	return keep
}

// BuildContinuePrompt 构建继续会话的用户提示词
func BuildContinuePrompt(newDiffContent string) string {
	var b strings.Builder
	b.WriteString("这是同一功能的后续变更。请基于你对代码库已有的理解，继续审查并提交。\n")
	b.WriteString("注意：之前已提交的变更不需要重复考虑，只关注本次新增的变更。\n\n")
	b.WriteString("新的代码变更：\n")
	b.WriteString(newDiffContent)
	b.WriteString("\n\n执行步骤：\n")
	b.WriteString("1. diff_overview → 了解本次新增变更\n")
	b.WriteString("2. read_file → 如有需要读取新变更代码\n")
	b.WriteString("3. report_review → 输出结构化审查结果\n")
	b.WriteString("4. git_commit → 提交\n")
	return b.String()
}
