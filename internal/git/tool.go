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
	{
		Name:        "read_diff",
		Description: "读取指定文件的详细 diff 变更内容。当全局 diff 被截断时，用此工具查看单个文件的精确变更行，比 read_file 更高效。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "相对于项目根目录的文件路径"
				},
				"context_lines": {
					"type": "number",
					"description": "变更行上下的上下文行数（默认 3），越大越能看清代码上下文"
				}
			},
			"required": ["path"]
		}`),
	},
	{
		Name:        "search_references",
		Description: "搜索代码库中指定符号（函数名、类型名、变量名）的引用位置。用于判断改动的波及范围，比如改了某个函数后看还有哪些地方调用了它。返回匹配的文件路径、行号和代码片段。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"symbol": {
					"type": "string",
					"description": "要搜索的符号名称，如函数名、类型名、变量名"
				},
				"path_filter": {
					"type": "string",
					"description": "可选，限定搜索目录范围，如 internal/service"
				}
			},
			"required": ["symbol"]
		}`),
	},
	{
		Name:        "report_review",
		Description: "输出结构化审查结果。在读代码完成分析后，用此工具提交审查意见，包含变更摘要和风险列表。无风险时 has_risk=false 即可。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"summary": {
					"type": "string",
					"description": "变更摘要：改了哪些文件、函数、功能（2-3行）"
				},
				"has_risk": {
					"type": "boolean",
					"description": "是否存在需要关注的风险"
				},
				"risks": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"severity": {
								"type": "string",
								"enum": ["high", "medium", "low"]
							},
							"category": {
								"type": "string",
								"enum": ["logic", "security", "performance", "error_handling", "maintainability"]
							},
							"file": { "type": "string", "description": "风险文件路径" },
							"line": { "type": "number", "description": "风险所在行号" },
							"description": { "type": "string", "description": "问题描述" },
							"suggestion": { "type": "string", "description": "修改建议" }
						},
						"required": ["severity", "category", "description"]
					}
				}
			},
			"required": ["summary", "has_risk"]
		}`),
	},
	{
		Name:        "git_hook_check",
		Description: "检查项目的 git hook 配置和 commit 规范约束。返回所有检测到的 hook（commit-msg、pre-commit 等）以及从中提取的规则摘要。在准备提交前调用此工具，确保 commit message 符合项目规范。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	},
	{
		Name:        "git_config_get",
		Description: "查询 git 配置项的值。可用于获取项目的 commit 相关配置（如 commit.template、user.name 等）。",
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
	{
		Name:        "summarize_changes",
		Description: "【关键阶段转换工具】在你完成代码阅读和理解后，调用此工具输出你对变更的结构化理解。调用后结束理解阶段进入审查提交阶段。务必在调用前确保已用 diff_overview 和 read_file 充分理解了代码变更。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"understanding": {
					"type": "string",
					"description": "对代码变更的完整理解总结（3-5行）：改了哪些文件/模块、修改目的、涉及的核心函数或类型、对项目的影响范围"
				}
			},
			"required": ["understanding"]
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
