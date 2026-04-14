package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DeepSeek DeepSeekConfig `yaml:"deepseek"`
	Commit   CommitConfig   `yaml:"commit"`
}

type DeepSeekConfig struct {
	APIKey  string `yaml:"api_key"`
	Model   string `yaml:"model"`
	BaseURL string `yaml:"base_url"`
	Timeout string `yaml:"timeout"`
}

type CommitConfig struct {
	DefaultScope string `yaml:"default_scope"`
	MaxDiffLines int    `yaml:"max_diff_lines"`
}

var defaultConfig = Config{
	DeepSeek: DeepSeekConfig{
		Model:   "deepseek-chat",
		BaseURL: "https://api.deepseek.com",
		Timeout: "30s",
	},
	Commit: CommitConfig{
		DefaultScope: "",
		MaxDiffLines: 500,
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

	overrideFromEnv(&config)

	if config.DeepSeek.APIKey == "" {
		return nil, fmt.Errorf("deepseek.api_key 未配置")
	}

	return &config, nil
}

func overrideFromEnv(cfg *Config) {
	if apiKey := os.Getenv("DEEPSEEK_API_KEY"); apiKey != "" {
		cfg.DeepSeek.APIKey = apiKey
	}
	if model := os.Getenv("DEEPSEEK_MODEL"); model != "" {
		cfg.DeepSeek.Model = model
	}
	if baseURL := os.Getenv("DEEPSEEK_BASE_URL"); baseURL != "" {
		cfg.DeepSeek.BaseURL = baseURL
	}
	if timeout := os.Getenv("DEEPSEEK_TIMEOUT"); timeout != "" {
		cfg.DeepSeek.Timeout = timeout
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

func (c *DeepSeekConfig) GetTimeout() time.Duration {
	if c.Timeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
}
