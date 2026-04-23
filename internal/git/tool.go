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
}

func FindToolDef(name string) *ToolDefinition {
	for _, td := range ToolDefinitions {
		if td.Name == name {
			return &td
		}
	}
	return nil
}