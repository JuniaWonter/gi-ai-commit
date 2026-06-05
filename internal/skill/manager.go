package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/oliver/git-ai-commit/internal/logger"
	"gopkg.in/yaml.v3"
)

type Manager struct {
	skills []Skill
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Discover(ctx context.Context, skillsDir string) error {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("skills 目录不存在: %s", skillsDir)
			return nil
		}
		return fmt.Errorf("读取 skills 目录失败: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(skillsDir, entry.Name(), "skill.yaml")
		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			continue
		}

		data, err := os.ReadFile(manifestPath)
		if err != nil {
			logger.Warn("读取 skill manifest 失败: %s, err: %v", manifestPath, err)
			continue
		}

		var manifest Manifest
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			logger.Warn("解析 skill manifest 失败: %s, err: %v", manifestPath, err)
			continue
		}

		if manifest.Transport != "stdio" {
			logger.Warn("不支持的 transport: %s, skill: %s", manifest.Transport, manifest.Name)
			continue
		}

		skill, err := NewMCPSkill(ctx, &manifest)
		if err != nil {
			logger.Warn("初始化 skill 失败: %s, err: %v", manifest.Name, err)
			continue
		}

		m.skills = append(m.skills, skill)
		logger.Info("加载 skill: %s (%s)", manifest.Name, manifest.Description)
	}

	return nil
}

func (m *Manager) AllTools() []ToolDefinition {
	var tools []ToolDefinition
	for _, s := range m.skills {
		tools = append(tools, s.Tools()...)
	}
	return tools
}

func (m *Manager) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	for _, s := range m.skills {
		for _, t := range s.Tools() {
			if t.Name == name {
				return s.CallTool(ctx, name, args)
			}
		}
	}
	return "", fmt.Errorf("未找到工具: %s", name)
}

func (m *Manager) HasTool(name string) bool {
	for _, s := range m.skills {
		for _, t := range s.Tools() {
			if t.Name == name {
				return true
			}
		}
	}
	return false
}

func (m *Manager) Shutdown() {
	for _, s := range m.skills {
		if err := s.Shutdown(); err != nil {
			logger.Warn("关闭 skill 失败: %s, err: %v", s.Name(), err)
		}
	}
}

func (m *Manager) SkillNames() []string {
	var names []string
	for _, s := range m.skills {
		names = append(names, s.Name())
	}
	return names
}

func GetSkillsDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	return filepath.Join(dir, "skills")
}

func IsSkillTool(name string) bool {
	return strings.HasPrefix(name, "codegraph_") || strings.HasPrefix(name, "code_graph")
}
