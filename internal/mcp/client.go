package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Client struct {
	session *mcp.ClientSession
	cmd     *exec.Cmd
}

type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type ToolResult struct {
	Content string
	IsError bool
}

func Connect(ctx context.Context, command string, args []string, env map[string]string) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "git-ai-commit",
		Version: "1.0.0",
	}, nil)

	transport := &mcp.CommandTransport{Command: cmd}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 MCP server 失败: %w", err)
	}

	return &Client{
		session: session,
		cmd:     cmd,
	}, nil
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	result, err := c.session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("列出工具失败: %w", err)
	}

	tools := make([]Tool, len(result.Tools))
	for i, t := range result.Tools {
		schemaBytes, _ := json.Marshal(t.InputSchema)
		tools[i] = Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schemaBytes,
		}
	}
	return tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolResult, error) {
	result, err := c.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("调用工具 %s 失败: %w", name, err)
	}

	var content string
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			content += tc.Text
		}
	}

	return &ToolResult{
		Content: content,
		IsError: result.IsError,
	}, nil
}

func (c *Client) Close() error {
	if c.session != nil {
		c.session.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}
	return nil
}
