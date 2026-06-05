package skill

import (
	"context"
	"fmt"

	"github.com/oliver/git-ai-commit/internal/logger"
	"github.com/oliver/git-ai-commit/internal/mcp"
)

type MCPSkill struct {
	manifest *Manifest
	client   *mcp.Client
	tools    []ToolDefinition
}

func NewMCPSkill(ctx context.Context, manifest *Manifest) (*MCPSkill, error) {
	client, err := mcp.Connect(ctx, manifest.Command, manifest.Args, manifest.Env)
	if err != nil {
		return nil, fmt.Errorf("连接 MCP server 失败: %w", err)
	}

	mcpTools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("列出工具失败: %w", err)
	}

	allowedTools := make(map[string]bool)
	for _, t := range manifest.Tools {
		allowedTools[t] = true
	}

	var tools []ToolDefinition
	for _, t := range mcpTools {
		if len(manifest.Tools) == 0 || allowedTools[t.Name] {
			tools = append(tools, ToolDefinition{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}

	logger.Info("MCPSkill %s 初始化成功，可用工具: %d", manifest.Name, len(tools))
	return &MCPSkill{
		manifest: manifest,
		client:   client,
		tools:    tools,
	}, nil
}

func (s *MCPSkill) Name() string {
	return s.manifest.Name
}

func (s *MCPSkill) Description() string {
	return s.manifest.Description
}

func (s *MCPSkill) Tools() []ToolDefinition {
	return s.tools
}

func (s *MCPSkill) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	result, err := s.client.CallTool(ctx, name, args)
	if err != nil {
		return "", err
	}
	if result.IsError {
		return "", fmt.Errorf("工具执行失败: %s", result.Content)
	}
	return result.Content, nil
}

func (s *MCPSkill) Shutdown() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
