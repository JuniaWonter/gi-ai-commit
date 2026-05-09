package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	AI         AIConfig         `yaml:"ai"`
	Commit     CommitConfig     `yaml:"commit"`
	DiffPrompt DiffPromptConfig `yaml:"diff_prompt"`
}

type AIConfig struct {
	DefaultModel string                   `yaml:"default_model"`
	Models       map[string]ModelConfig   `yaml:"models"`
}

type ModelConfig struct {
	APIKey        string `yaml:"api_key"`
	Model         string `yaml:"model"`
	BaseURL       string `yaml:"base_url"`
	Timeout       string `yaml:"timeout"`
	ContextWindow int    `yaml:"context_window"`
}

type CommitConfig struct {
	DefaultScope string `yaml:"default_scope"`
	MaxDiffLines int    `yaml:"max_diff_lines"`
}

type DiffPromptConfig struct {
	MaxFullDiffBytes    int `yaml:"max_full_diff_bytes"`
	MaxCompactDiffBytes int `yaml:"max_compact_diff_bytes"`
	MaxPerFileDiffBytes int `yaml:"max_per_file_diff_bytes"`
	MaxCompactDiffFiles int `yaml:"max_compact_diff_files"`
}

var defaultConfig = Config{
	AI: AIConfig{
		DefaultModel: "deepseek",
		Models: map[string]ModelConfig{
			"deepseek": {
				Model:   "deepseek-chat",
				BaseURL: "https://api.deepseek.com",
				Timeout: "30s",
			},
		},
	},
	Commit: CommitConfig{
		DefaultScope: "",
		MaxDiffLines: 500,
	},
	DiffPrompt: DiffPromptConfig{
		MaxFullDiffBytes:    24_000,
		MaxCompactDiffBytes: 16_000,
		MaxPerFileDiffBytes: 2_200,
		MaxCompactDiffFiles: 12,
	},
}

func GetConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "ai-commit", "config.yaml")
}

func Load() (*Config, error) {
	configPath := GetConfigPath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("配置文件不存在: %s", configPath)
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	config := defaultConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	config.applyDefaults()

	overrideFromEnv(&config)

	if config.AI.DefaultModel == "" {
		return nil, fmt.Errorf("ai.default_model 未配置")
	}

	if config.AI.Models == nil || len(config.AI.Models) == 0 {
		return nil, fmt.Errorf("ai.models 未配置")
	}

	if _, ok := config.AI.Models[config.AI.DefaultModel]; !ok {
		return nil, fmt.Errorf("默认模型 %q 未在 ai.models 中配置", config.AI.DefaultModel)
	}

	return &config, nil
}

func overrideFromEnv(cfg *Config) {
	defaultModel := cfg.AI.DefaultModel
	if defaultModel == "" {
		defaultModel = "deepseek"
	}
	if cfg.AI.Models == nil {
		cfg.AI.Models = make(map[string]ModelConfig)
	}
	m := cfg.AI.Models[defaultModel]
	if v := os.Getenv("AI_API_KEY"); v != "" {
		m.APIKey = v
	}
	if v := os.Getenv("AI_MODEL"); v != "" {
		m.Model = v
	}
	if v := os.Getenv("AI_BASE_URL"); v != "" {
		m.BaseURL = v
	}
	if v := os.Getenv("AI_TIMEOUT"); v != "" {
		m.Timeout = v
	}
	cfg.AI.Models[defaultModel] = m
}

func (c *Config) applyDefaults() {
	defaults := defaultConfig.DiffPrompt
	if c.DiffPrompt.MaxFullDiffBytes <= 0 {
		c.DiffPrompt.MaxFullDiffBytes = defaults.MaxFullDiffBytes
	}
	if c.DiffPrompt.MaxCompactDiffBytes <= 0 {
		c.DiffPrompt.MaxCompactDiffBytes = defaults.MaxCompactDiffBytes
	}
	if c.DiffPrompt.MaxPerFileDiffBytes <= 0 {
		c.DiffPrompt.MaxPerFileDiffBytes = defaults.MaxPerFileDiffBytes
	}
	if c.DiffPrompt.MaxCompactDiffFiles <= 0 {
		c.DiffPrompt.MaxCompactDiffFiles = defaults.MaxCompactDiffFiles
	}
}

func Save(config *Config) error {
	configPath := GetConfigPath()

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return nil
}

func (c *Config) GetModelConfig(modelName string) (*ModelConfig, error) {
	if modelName == "" {
		modelName = c.AI.DefaultModel
	}

	m, ok := c.AI.Models[modelName]
	if !ok {
		return nil, fmt.Errorf("模型 %q 未配置，请在 ai.models 中添加", modelName)
	}

	if m.APIKey == "" {
		return nil, fmt.Errorf("模型 %q 的 api_key 未配置", modelName)
	}

	return &m, nil
}

func (m *ModelConfig) GetTimeout() time.Duration {
	if m.Timeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(m.Timeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
}
