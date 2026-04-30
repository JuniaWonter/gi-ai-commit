package git

import "encoding/json"

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

var ToolDefinitions = []ToolDefinition{
	{
		Name:        "read_file",
		Description: "读取项目中指定路径的文件内容，用于代码审查时获取更多上下文。返回带行号的文件内容（最多 5000 字符），超出部分会被截断。可通过 start_line/end_line 指定读取范围。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "相对于项目根目录的文件路径"
				},
				"start_line": {
					"type": "number",
					"description": "起始行号（1-indexed），默认从第 1 行开始"
				},
				"end_line": {
					"type": "number",
					"description": "结束行号，默认读到文件末尾"
				}
			},
			"required": ["path"]
		}`),
	},
	{
		Name:        "list_tree",
		Description: "获取项目的目录树结构（最多 3 层），用于了解项目整体结构。自动执行，无需授权。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"max_depth": {
					"type": "number",
					"description": "目录树最大深度（默认 3）"
				}
			}
		}`),
	},
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
		Name:        "diff_overview",
		Description: "获取变更文件的紧凑概览（git diff --stat + --name-status），了解哪些文件变更和变更量，无需读取完整 diff 内容。自动执行，无需授权。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {}
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
