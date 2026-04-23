package git

import "encoding/json"

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

var ToolDefinitions = []ToolDefinition{
	{
		Name:        "git_commit",
		Description: "执行 git commit 提交代码变更。返回提交结果（成功或失败及错误信息）。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": {
					"type": "string",
					"description": "commit message 内容"
				}
			},
			"required": ["message"]
		}`),
	},
	{
		Name:        "git_commit_amend",
		Description: "修改上次提交的 commit message（git commit --amend）。适用于提交失败后需要修正格式的场景。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": {
					"type": "string",
					"description": "新的 commit message 内容"
				}
			},
			"required": ["message"]
		}`),
	},
	{
		Name:        "git_log_recent",
		Description: "查看最近 N 条 commit 记录，了解仓库已有的 commit message 格式风格。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"count": {
					"type": "integer",
					"description": "查看的 commit 数量，默认 5",
					"default": 5
				}
			},
			"required": []
		}`),
	},
	{
		Name:        "git_hook_check",
		Description: "检查仓库是否存在 commit-msg hook，如果有则 commit message 需满足 hook 的格式约束。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {},
			"required": []
		}`),
	},
	{
		Name:        "git_config_get",
		Description: "获取 git 配置项的值，如 commit.template（commit message 模板路径）。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"key": {
					"type": "string",
					"description": "git 配置键名，如 commit.template"
				}
			},
			"required": ["key"]
		}`),
	},
}

func FindToolDef(name string) *ToolDefinition {
	for _, td := range ToolDefinitions {
		if td.Name == name {
			return &td
		}
	}
	return nil
}