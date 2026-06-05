package skill

import (
	"context"
	"encoding/json"
)

type Skill interface {
	Name() string
	Description() string
	Tools() []ToolDefinition
	CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error)
	Shutdown() error
}

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type Manifest struct {
	Name        string            `yaml:"name"`
	Version     string            `yaml:"version"`
	Description string            `yaml:"description"`
	Transport   string            `yaml:"transport"`
	Command     string            `yaml:"command"`
	Args        []string          `yaml:"args"`
	Env         map[string]string `yaml:"env"`
	Tools       []string          `yaml:"tools"`
}
