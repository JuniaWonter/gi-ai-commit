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
		Name:        "analyze_changed_functions",
		Description: "分析变更文件中被修改的函数，提取这些函数的完整定义。帮助深入理解变更的逻辑上下文，而不仅仅是看 diff 的几行变更。返回每个变更函数的完整代码、行号范围和变更说明。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "相对于项目根目录的文件路径"
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
		Description: "【必须调用】在 git_commit 之前输出结构化审查结果。包含变更摘要、风险列表和审查建议。对于极为简单的变更（如纯注释、单行修复），可设置 is_simple=true 跳过详细审查。",
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
				"is_simple": {
					"type": "boolean",
					"description": "变更是否极为简单（如纯注释、单行修复、配置微调），可跳过详细审查"
				},
				"recommendation": {
					"type": "string",
					"enum": ["approve", "approve_with_warnings", "request_changes"],
					"description": "审查建议：approve=无风险可提交, approve_with_warnings=有警告但可提交, request_changes=有严重问题需修改"
				},
				"highlights": {
					"type": "array",
					"items": { "type": "string" },
					"description": "值得肯定的设计决策或代码亮点（可选）"
				},
				"breaking_changes": {
					"type": "boolean",
					"description": "是否包含破坏性变更（API 变更、接口不兼容等）"
				},
				"risks": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"severity": {
								"type": "string",
								"enum": ["critical", "high", "medium", "low"]
							},
							"category": {
								"type": "string",
								"enum": ["correctness", "security", "performance", "error_handling", "design", "testing", "maintainability", "consistency"]
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
			"required": ["summary", "has_risk", "recommendation"]
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
	{
		Name:        "update_memory",
		Description: "更新项目记忆。当你发现项目的重要架构模式、团队约定、易错点或审查规则时，调用此工具记录到项目记忆中。记忆会在后续提交时自动加载，帮助 AI 更好地理解项目上下文。每次会话最多调用 1 次。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"content": {
					"type": "string",
					"description": "要记录的项目知识（建议 200-500 字），包含：架构模式、代码约定、易错点、审查规则等"
				},
				"action": {
					"type": "string",
					"enum": ["append", "replace"],
					"description": "append=追加到现有记忆，replace=完全替换（谨慎使用）"
				}
			},
			"required": ["content", "action"]
		}`),
	},
	{
		Name:        "git_status",
		Description: "获取工作区状态（git status --short）。显示暂存区、工作区和未跟踪文件的状态。自动执行，无需授权。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	},
	{
		Name:        "git_log",
		Description: "获取最近的提交历史（git log）。可查看提交哈希、作者、日期和提交信息。自动执行，无需授权。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"count": {
					"type": "number",
					"description": "返回的提交数量（默认 10，最大 50）"
				},
				"oneline": {
					"type": "boolean",
					"description": "是否使用单行格式（默认 true）"
				}
			}
		}`),
	},
	{
		Name:        "git_branch",
		Description: "获取分支信息（git branch）。显示当前分支和所有本地分支。自动执行，无需授权。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"all": {
					"type": "boolean",
					"description": "是否包含远程分支（默认 false）"
				}
			}
		}`),
	},
	{
		Name:        "git_diff_unstaged",
		Description: "获取未暂存的变更（git diff）。显示工作区中未 add 的修改内容。自动执行，无需授权。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "可选，限定查看指定文件的未暂存变更"
				}
			}
		}`),
	},
	{
		Name:        "git_add",
		Description: "将文件添加到暂存区（git add）。可以暂存指定文件或目录。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"paths": {
					"type": "array",
					"items": { "type": "string" },
					"description": "要暂存的文件或目录路径列表"
				}
			},
			"required": ["paths"]
		}`),
	},
	{
		Name:        "git_restore",
		Description: "从暂存区移除文件（git restore --staged）或恢复工作区文件（git restore）。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"paths": {
					"type": "array",
					"items": { "type": "string" },
					"description": "要操作的文件路径列表"
				},
				"staged": {
					"type": "boolean",
					"description": "true=从暂存区移除（git restore --staged），false=恢复工作区文件（git restore）"
				}
			},
			"required": ["paths"]
		}`),
	},
	{
		Name:        "git_stash",
		Description: "暂存当前工作区变更（git stash）。可以保存或恢复工作区状态。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {
					"type": "string",
					"enum": ["push", "pop", "list", "drop"],
					"description": "push=保存当前变更, pop=恢复最近一次暂存, list=列出所有暂存, drop=删除指定暂存"
				},
				"message": {
					"type": "string",
					"description": "push 时的描述信息（可选）"
				},
				"index": {
					"type": "number",
					"description": "drop 时指定要删除的暂存索引（可选，默认 0）"
				}
			},
			"required": ["action"]
		}`),
	},
	{
		Name:        "git_blame",
		Description: "获取文件的 blame 信息（git blame）。显示每一行的最后修改者和提交信息。自动执行，无需授权。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "文件路径"
				},
				"start_line": {
					"type": "number",
					"description": "起始行号（可选）"
				},
				"end_line": {
					"type": "number",
					"description": "结束行号（可选）"
				}
			},
			"required": ["path"]
		}`),
	},
	{
		Name:        "git_tag",
		Description: "管理 Git 标签。可以列出标签或创建新标签。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {
					"type": "string",
					"enum": ["list", "create"],
					"description": "list=列出所有标签, create=创建新标签"
				},
				"name": {
					"type": "string",
					"description": "create 时的标签名称"
				},
				"message": {
					"type": "string",
					"description": "create 时的标签说明（可选，有则为 annotated tag）"
				}
			},
			"required": ["action"]
		}`),
	},
	{
		Name:        "ask_user",
		Description: "向用户提问并等待回答。当你遇到不确定的决策、需要用户选择方案、或需要用户确认某个选择时使用此工具。会弹出交互界面让用户选择或输入。提交代码前建议用此工具向用户确认 commit message。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"question": {
					"type": "string",
					"description": "要向用户提出的问题"
				},
				"options": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"label": {
								"type": "string",
								"description": "选项的简短标签（1-5 个词）"
							},
							"description": {
								"type": "string",
								"description": "选项的详细说明"
							}
						},
						"required": ["label"]
					},
					"description": "可选项列表，用户可以从中选择"
				},
				"allow_custom": {
					"type": "boolean",
					"description": "是否允许用户输入自定义答案（默认 true）"
				}
			},
			"required": ["question"]
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
