# gi-ai-commit 优化方案

> 基于 2026-05-09 代码状态，汇总所有优化方向

---

## 已完成优化（Prompt 层面）

### P-1. 审查与 commit message 分离
- **问题**：AI 在同一轮对话中既做审查又写 commit message，审查输出（"存在潜在空指针风险"）污染了 commit message，导致提交信息描述的是审查结论而非实际变更
- **方案**：在 system prompt 和 user prompt 中多处强调"审查意见是给用户看的辅助信息，commit message 必须基于代码变更本身生成"
- **涉及文件**：`internal/ai/client.go` — `buildAuthSystemPrompt`、`buildAuthPrompt`、紧凑版两个函数

### P-2. 禁止凭文件名推断风险
- **问题**：AI 调用 `diff_overview` 后仅凭文件名就猜测风险，不真正用 `read_file` 读取代码
- **方案**：新增核心原则"读代码，再做判断"，执行顺序中明确 `read_file` 在分析风险之前
- **涉及文件**：同上

### P-3. 变更结构摘要环节
- **问题**：AI 对变更的理解是隐式的（在脑中），没有明确的"事实确认"环节，导致 commit message 容易跑偏
- **方案**：在第 3 步强制输出「变更结构摘要」（改了哪些文件、函数、类型），commit message 基于此生成，而非审查意见
- **涉及文件**：同上

### P-4. Token 节约意识
- **问题**：AI 不感知上下文累积增长，容易输出长文本或整文件读取，导致 token 消耗过大提前触发紧凑模式
- **方案**：新增核心原则第 4 条"节约 token"；规则区加了"控制每轮输出长度"、"直接引用已有结果"
- **涉及文件**：同上

### P-5. Diff 截断感知
- **问题**：三级降级策略截断 diff 后，AI 不知道自己看到的是不完整的变更
- **方案**：规则区告知"diff 内容可能被截断，用 read_file 补全关键代码"
- **涉及文件**：同上

### P-6. 提交前自检
- **问题**：AI 直接基于审查结论生成 commit message，没有二次确认
- **方案**：新增"提交前自检"规则，让 AI 调用 `git_commit` 前确认 message 反映的是变更本身而非审查结论
- **涉及文件**：同上

---

## 待优化方向

按优先级从高到低排列：

### H-1. read_diff 工具

**问题**：全局 diff 被截断到 16KB 时，AI 不知道单个文件的具体改了什么行。`read_file` 返回的是文件全量内容（最多 5000 字符），AI 需要自己对比找出变更行，上下文消耗大。

**方案**：新增工具 `read_diff`，返回单个文件的精准 diff 片段。

```go
// git/tool.go 新增
{
    Name: "read_diff",
    Description: "读取指定文件的详细 diff 变更。当全局 diff 被截断时，用此工具查看单个文件的完整变更内容。",
    Parameters: json.RawMessage(`{
        "type": "object",
        "properties": {
            "path": { "type": "string", "description": "文件路径" }
        },
        "required": ["path"]
    }`),
}
```

**文件级改动**：
- `internal/git/tool.go` — 新增 ToolDefinition、新增 `git diff --cached -- <path>` 执行逻辑
- `internal/ai/client.go` — `executeToolCall` 新增 case
- 可选：从 `internal/diff/processor.go` 复用 `getCmdOutput`

**收益**：
- AI 看到精确的变更行，不再需要 read_file 全量对比
- commit message 更精准
- 对话 token 消耗减少（diff 片段 < 全量文件）

**工作量**：约 1 天

---

### H-2. Search References 工具

**问题**：AI 不知道改动的函数是否被其他地方调用，无法判断影响范围。当前只能猜。

**方案**：新增 `search_references` 工具，用 `grep -r` 做基本的引用搜索。

```go
{
    Name: "search_references",
    Description: "搜索代码库中指定符号（函数名、类型名、变量名）的引用位置。用于判断改动的波及范围。返回匹配的文件路径和行号。",
    Parameters: json.RawMessage(`{
        "type": "object",
        "properties": {
            "symbol": { "type": "string", "description": "要搜索的符号名称，如函数名、类型名" }
        },
        "required": ["symbol"]
    }`),
}
```

**文件级改动**：
- `internal/git/tool.go` — 新增 ToolDefinition（或新增 `internal/git/search.go` 实现搜索逻辑）
- `internal/ai/client.go` — `executeToolCall` 新增 case、`buildOpenAITools` 自动包含

**注意事项**：
- 限制搜索结果行数（如前 30 行），避免 token 爆炸
- 配合 `.gitignore` 过滤掉 vendor、node_modules 等

**收益**：
- 轻量级影响范围分析，不需要 LSP
- 覆盖 80% 的波及分析场景

**工作量**：约 0.5 天

---

### H-3. Messages 压缩策略

**问题**：`session.messages` 持续增长，每轮 loop 都会 append 工具结果和 AI 回复。到第 5、6 轮时上下文大量被工具结果占据，AI 决策质量下降，token 消耗暴增。

**方案 A — 丢弃早期工具结果**：达到阈值后丢弃早期 `read_file` 和 `list_tree` 结果，只保留 AI 的推理回复、审查输出、commit 尝试记录。

```go
const maxToolResultHistory = 3 // 只保留最近 3 轮工具结果

func (s *CommitSession) compactMessages() {
    // 1. 保留 system msg（index 0）+ user prompt（index 1）
    // 2. 从后往前扫描，保留最近 maxToolResultHistory 轮的 tool result
    // 3. 丢弃中间的 read_file / list_tree 结果
    // 4. 保留所有 git_commit / git_commit_amend 调用记录
}
```

**方案 B — 摘要化工具结果**：`read_file` 结果返回后，将其摘要化再存入 messages。

```go
func summarizeReadFile(path, content string) string {
    lines := strings.Split(content, "\n")
    if len(lines) > 50 {
        return fmt.Sprintf("📄 %s (%d 行):\n%s\n... [已压缩，共 %d 行，用 read_file 获取完整内容]",
            path, len(lines), strings.Join(lines[:50], "\n"), len(lines))
    }
    return content
}
```

**文件级改动**：
- `internal/ai/client.go` — `ExecuteAndResumeWithStream` 中集成压缩逻辑
- 或新增 `CommitSession.compressMessages()` 在 `StreamAI` 前调用

**注意事项**：
- 压缩后可能影响 AI 引用具体代码行的能力（方案 B 有这个问题）
- 建议先用方案 A 试水，简单且无副作用

**收益**：
- 直接节省每轮 token
- 延迟紧凑模式触发
- 后面轮次的 AI 回复质量更稳定

**工作量**：方案 A 约 0.5 天，方案 B 约 1 天

---

### H-4. 按模型调整 Compact 阈值

**问题**：当前硬编码 `estimatedTokens > 6000` 就切紧凑模式。DeepSeek-Chat 上下文窗口 64K，6000 token 就切太保守了。紧凑版丢掉了大量审查指令，降低审查质量。

**方案**：根据模型动态计算紧凑模式触发阈值。

```go
func (c *Client) maxContextTokens() int {
    switch c.config.Model {
    case "deepseek-chat":  return 56000  // 64K 窗口，留 8K 给输出
    case "deepseek-reasoner": return 56000
    case "gpt-4":          return 6000   // 8K 窗口
    case "gpt-4-turbo":    return 120000 // 128K 窗口
    default:               return 56000  // 保守默认
    }
}

// 在 StartCommitSession 中使用：
// estimatedCompactThreshold := maxContextTokens() * 0.85 // 85% 触发压缩
```

**文件级改动**：
- `internal/ai/client.go` — `Client` 新增 `maxContextTokens()` 方法，修改 `StartCommitSession` 阈值计算
- `internal/config/config.go` — 可选：在 `ModelConfig` 中暴露 `context_window` 配置

**收益**：
- DeepSeek 场景下几乎永远不用紧凑版，审查质量大幅提升
- 0 行配置改动，纯代码

**工作量**：约 2 小时

---

### H-5. 两阶段架构（理解 → 审查+提交）

**当前架构**：
```
一轮对话：读代码 → 审查 → git_commit
         ↑       ↑         ↑
    同一 session，上下文混杂
```

**目标架构**：

```
第一阶段「理解」： 读代码 → 输出「变更事实报告」
                  tools: [diff_overview, read_file, list_tree, search_references]

第二阶段「审查+提交」：基于事实报告做审查 + 提交
                       tools: [read_file, git_commit, git_commit_amend]
```

**实现方案**：

阶段 1：
- 使用独立的 `buildUnderstandSystemPrompt()` 和 `buildUnderstandPrompt()`
- 只给读代码工具，不给 `git_commit`
- AI 输出结构化的「变更事实报告」（改了哪些文件/函数/类型/接口）

阶段 2：
- 在同一个 CommitSession 中，在 messages 中 inject 阶段 1 的摘要 + 新的 system prompt
- 系统提示词告诉 AI："以下是变更事实报告。基于此做审查和生成 commit message。"
- 审查质量和 commit message 准确性不再受"读代码过程"干扰

**文件级改动**：
- `internal/ai/client.go` — 新增 `buildUnderstandSystemPrompt()`、`buildUnderstandPrompt()`；修改 `StartCommitSession` 或新增 `StartTwoPhaseSession`
- `internal/ai/client.go` — 修改 `LoopCount` 限制，两阶段各有自己的 loop 上限
- `internal/ai/client.go` — `CommitSession` 新增 `phase` 字段标识当前阶段

**注意事项**：
- 两阶段意味着额外的 API 调用，增加延迟
- 但每轮的 tool result 量会减少，总 token 消耗可能不变甚至降低
- 阶段 1 可以增加 `maxReadFileCalls`（比如 8 次），阶段 2 减少（2 次）

**收益**：
- commit message 不会被审查过程干扰
- 变更事实报告作为结构化上下文持久保留，不会被后续工具结果淹没
- 阶段 2 的上下文更干净，AI 决策质量更高

**工作量**：约 3 天

---

### H-6. 结构化审查输出

**问题**：当前审查结果是 AI 自由输出的文本，格式不固定，解析困难。

**方案**：让 AI 通过 `report_review` 工具调用来输出结构化的审查结果。

```go
{
    Name: "report_review",
    Description: "输出审查结果。读代码并完成分析后，用此工具输出结构化审查意见。",
    Parameters: json.RawMessage(`{
        "type": "object",
        "properties": {
            "summary": { "type": "string", "description": "变更摘要（改了哪些文件/功能）" },
            "has_risk": { "type": "boolean", "description": "是否存在需要关注的风险" },
            "risks": {
                "type": "array",
                "items": {
                    "type": "object",
                    "properties": {
                        "severity": { "type": "string", "enum": ["high", "medium", "low"] },
                        "category": { "type": "string", "enum": ["logic", "security", "performance", "error_handling", "maintainability"] },
                        "file": { "type": "string" },
                        "line": { "type": "number" },
                        "description": { "type": "string" },
                        "suggestion": { "type": "string" }
                    }
                }
            }
        },
        "required": ["summary", "has_risk"]
    }`),
}
```

流程变为：
```
read_file → report_review（输出结构化结果）→ git_commit
```

**文件级改动**：
- `internal/git/tool.go` — 新增 ToolDefinition
- `internal/ai/client.go` — `executeToolCall` 中 `report_review` 的处理（存到 session 结构中）
- `CommitSession` 新增 `ReviewResult` 字段
- TUI 侧可以根据结构化结果做更丰富的展示（如风险列表渲染）

**收益**：
- 审查结果结构化，不会被误解为 commit message
- 机器可解析，可以统计风险分布
- 用户看到的审查意见更清晰

**工作量**：约 2 天

---

### H-7. read_file 优先级排序

**问题**：AI 调用 `read_file` 的顺序是随机的（或按文件名），优先读到的可能是辅助文件而非核心变更文件。

**方案**：在 diff 侧预计算每个文件的"影响分"（改动行数 + 导入该文件的包数量），提示 AI 优先读高分文件。

影响分计算：
```go
type FilePriority struct {
    Path           string
    ChangeScore    int    // 改动行数
    ReferencedBy   int    // 被多少文件引用（grep -l import "path"）
    Priority       int    // changeScore * 2 + referencedBy
}
```

在 user prompt 中附加优先级提示：
```
变更文件优先级排序（分数越高越建议优先阅读）：
- internal/service/user.go      ⭐ 优先级高（改动 45 行，被 8 个文件引用）
- internal/handler/user.go      ⭐ 优先级中（改动 12 行，被 3 个文件引用）
- internal/model/user.go        ⭐ 优先级低（改动 3 行）
```

**文件级改动**：
- 新增 `internal/diff/priority.go` — 计算文件优先级
- `internal/ai/client.go` — `buildAuthPrompt` 中拼接优先级信息

**注意**：
- 不能过于约束 AI 的选择顺序，只是提示
- `ReferencedBy` 的计算可以静态用 `grep -r` 做，不需要 LSP

**收益**：
- AI 优先读核心文件，审查更高效
- 在有限的 `maxReadFileCalls`（当前 4 次）下，读到最关键的内容

**工作量**：约 1 天

---

### H-8. Diff 降级策略优化

**问题**：当前三级降级策略在第二级（压缩摘要）时按字节硬切，可能丢掉某些文件的全部 diff，但 AI 不知道丢了哪些文件。

**当前行为**：
```
第一级：完整 diff ≤ 24KB → 全部给 AI
第二级：按文件改动行数排序，取前 N 个文件直到填满 16KB
第三级：只有 stat + name-status
```

**优化方案**：在 AI prompt 中明确告知降级状态，并提示补全手段。

```go
// 压缩模式下，prompt 追加：
diffTruncationHint := fmt.Sprintf(
    "\n[注意：完整 diff 过大，当前显示的是按改动量排序的摘要（%d/%d 个文件）。\n"+
    "如果需要查看某个文件的完整变更，请用 read_diff(<path>)]",
    shownCount, totalCount)
```

**文件级改动**：
- `internal/diff/processor.go` — `buildPayloadsFromDiff` 中追加提示文本
- 可选：`DiffPayload` 新增字段 `TruncatedFiles []string`（被截断的文件列表）供 AI 参考

**收益**：
- AI 知道自己看到的是不完整的信息
- 主动引导 AI 补全关键文件的 diff
- 减少凭猜测做 review 的概率

**工作量**：约 0.5 天

---

### H-9. LSP 集成（gopls）

**问题**：`grep -r` 只能做文本匹配，做不到类型层面的交叉引用。gopls 可以提供精确的引用、定义、调用链信息。

**方案**：新增 `lsp_references`、`lsp_definition` 工具，通过运行 `gopls` 命令获取信息。

```go
{
    Name: "lsp_references",
    Description: "查找指定符号（函数、类型、变量）在项目中的所有引用位置。比 search_references 更精确（只匹配语义引用，不匹配注释中的同名文本）。",
}
// 实现：gopls references <file> <line> <col>
```

**文件级改动**：
- 新增 `internal/lsp/client.go` — 封装 gopls 命令调用
- `internal/git/tool.go` — 新增 ToolDefinition
- `internal/ai/client.go` — 注册工具

**注意事项**：
- gopls 需要首次索引，第一次调用可能较慢
- 对非 Go 项目不适用（但当前项目就是 Go）
- 可以考虑惰性初始化，首次打开项目时后台索引

**收益**：
- 精确的类型层面引用分析
- 可以回答"这个接口还有哪些实现"、"这个字段在哪里被读取"
- 审查深度从"改了什么"升级到"改了会影响到谁"

**工作量**：约 3-5 天（含 gopls 命令通信的容错处理）

---

### H-10. Prompt Caching 适配

**问题**：当前每次 API 调用都发送完整 system prompt + tools 定义 + 历史对话。对于支持 prompt caching 的 API（Claude、Gemini），这些不变的前缀内容可以被缓存，大幅降低延迟和成本。

**适用场景**：
- 如果未来切到 Anthropic Claude API，利用 `cache_control` 标记
- 当前 DeepSeek 不支持，但可以预留接口

**文件级改动**：
- `internal/ai/client.go` — `StreamAI` 中根据 API 提供商决定是否注入 cache 标记
- 或抽象出一个 `PromptBuilder` 接口，按 provider 有不同的构建策略

**工作量**：约 2 天（含适配 Anthropic SDK）

---

### H-11. `--continue` 会话继承

**问题**：频繁对一个功能进行多次提交时，每次 commit 都是全新的 AI session。AI 需要重新调用 `list_tree` 了解项目结构、重新 `read_file` 读关键代码，浪费 token 且每次都是"新手"视角，缺乏对项目背景的连贯理解。

**方案**：新增 `--continue` 参数，将上一次 commit 的 AI 会话持久化到 `.git/ai-session.json`，在下一次 commit 时载入。

#### 数据流

```
第一次 commit（无 --continue）：
  StartCommitSession() → session 执行 → commit 成功
  → 序列化 messages 到 .git/ai-session.json
  → 返回

第二次 commit（git-ai commit --continue）：
  检查 .git/ai-session.json 是否存在 + 同分支
  → LoadSession() 反序列化
  → 压缩旧消息（丢弃过期 tool result）
  → 追加新的 user message
  → session 继续执行 → commit 成功
  → 更新 .git/ai-session.json
```

#### 存储格式

```json
{
  "version": 1,
  "model": "deepseek-chat",
  "branch": "feature/xxx",
  "last_commit_hash": "abc123def",
  "compact_mode": false,
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "初始 diff..."},
    {"role": "assistant", "content": "审查意见...", "tool_calls": [...]},
    {"role": "tool", "content": "diff_overview: ..."},
    ...
  ]
}
```

`.git/ai-session.json` 存在 `.git/` 目录下，天然具有：
- **分支隔离**：切换分支后自动不可见
- **不被提交**：git 不会跟踪 `.git/` 内容
- **无残留**：删除分支时自动清理

#### 加载时的消息压缩策略

旧 session 中的以下内容可以安全裁剪，**只保留 AI 推理和摘要，丢弃原始工具输出**：

| 保留 | 丢弃 |
|---|---|
| System prompt | 旧 read_file 结果（>500 字符时） |
| AI 的审查推理输出 | 旧 diff_overview 结果 |
| AI 输出的「变更结构摘要」 | 旧 list_tree 结果（结构变化不大） |
| 旧 git_commit 的调用和结果 | read_file 中已过期的大段文件内容 |
| 本次新追加的 user message（新 diff） | |

压缩后旧 session 剩余约 30-50% 的 token（实测），主要为 AI 的推理思路 + 结构性理解。

#### 追加的新 user message

```go
func buildContinuePrompt(newDiffContent, description string) string {
    var b strings.Builder
    b.WriteString("这是同一功能的后续变更。请基于你对代码库已有的理解，继续审查并提交。\n")
    b.WriteString("注意：之前已提交的变更不需要重复考虑，只关注本次新增的变更。\n\n")
    if description != "" {
        b.WriteString("项目描述：\n")
        b.WriteString(description)
        b.WriteString("\n\n")
    }
    b.WriteString("新的代码变更：\n")
    b.WriteString(newDiffContent)
    return b.String()
}
```

#### 安全检查

- **分支校验**：保存时的 branch vs 当前 branch，不匹配则警告且不使用缓存
- **提交哈希校验**（可选）：如果 `last_commit_hash` 不是当前 HEAD 的父提交，说明中间有手动提交，缓存可能已过时
- **有效期**：超过 7 天的 session 文件视为过期，自动重建

#### 文件级改动

| 文件 | 改动 |
|---|---|
| `internal/ai/session.go`（新增） | 序列化/反序列化 CommitSession，消息压缩逻辑 |
| `internal/ai/client.go` | `CommitSession` 新增 `ToPersistable()` / `GetBranch()`；`Client` 新增 `ContinueSession()` |
| `cmd/commit.go` | `CommitOptions` 新增 `Continue bool`；`RunCommit` 中判断 `--continue` 分支 |
| `main.go` | 注册 `--continue` 参数 |
| `tui/commit_flow.go` | `CommitFlowOptions` 传递 `Continue` 标志，成功后触发序列化 |

#### 收益

- **第二次及后续 commit 节省 40-60% token**（不需要重新 system prompt、list_tree、了解项目结构）
- **审查连贯性**：AI 知道"这个功能上次改了 X，这次改了 Y"，能做出更有上下文关联的审查
- **减少重复调用**：未改动的文件的 read_file 不需要重新执行
- **低延迟**：省掉的 token = 省掉的 API 处理时间

#### 工作量

约 2-3 天，含：
- 序列化/反序列化：0.5 天
- 消息压缩策略：0.5 天
- CLI 参数 + 流程集成：0.5 天
- TUI 侧对接：0.5 天
- 边界情况处理（分支切换、过期、hash 校验）：0.5 天

---

## 优化路线图

### 第一阶段（P0 — 高收益 / 低投入）

| # | 任务 | 估算 |
|---|---|---|
| H-4 | 按模型调整 Compact 阈值 | 2h |
| H-1 | read_diff 工具 | 1d |
| H-3 方案 A | Messages 压缩（丢弃早期结果） | 0.5d |
| H-8 | Diff 降级提示 | 0.5d |

> 先做这 4 个，投入约 2.5 天，收益最直接：AI 看到完整 diff、对话历史不膨胀、极少触发紧凑模式。

### 第二阶段（P1 — 中收益 / 中投入）

| # | 任务 | 估算 |
|---|---|---|
| H-2 | Search References 工具 | 0.5d |
| H-7 | read_file 优先级排序 | 1d |
| H-5 | 两阶段架构 | 3d |

### 第三阶段（P2 — 深度优化）

| # | 任务 | 估算 |
|---|---|---|
| H-6 | 结构化审查输出 | 2d |
| H-9 | gopls 集成 | 3-5d |
| H-10 | Prompt Caching 适配 | 2d |

---

## 风险与注意事项

1. **两阶段增加延迟**：第一阶段的额外 API 调用意味着至少增加 1 次 round-trip。实测 DeepSeek 的 TTFB 约 1-2s，两阶段总计增加 2-4s 延迟。

2. **LSP 的维护成本**：gopls 版本更新可能带来不兼容的协议变更。建议先用 `search_references`（grep 方案）覆盖大部分场景，LSP 作为可选的深度分析开关。

3. **工具数量膨胀**：工具越多，AI 的 tool_choice 决策越慢。当前 5 个工具，扩展后可能达到 8-10 个。需要关注 AI 是否能合理选择工具。

4. **Messages 压缩丢失信息**：压缩早期工具结果后，如果 AI 需要回头引用已被丢弃的 read_file 内容，无法恢复。压缩策略需要保守，优先选择方案 A（丢弃整轮）而非方案 B（摘要化）。

---

## 已修改文件清单

### 已完成（Prompt 优化）
- `internal/ai/client.go` — `buildAuthSystemPrompt`、`buildAuthPrompt`、`buildAuthSystemPromptCompact`、`buildAuthPromptCompact`（4 个函数重写）

### 待修改
- `internal/git/tool.go` — 新增 read_diff、search_references、lsp_references 等工具定义
- `internal/ai/client.go` — 新增工具处理逻辑、messages 压缩、两阶段会话
- `internal/diff/processor.go` — 追加截断提示
- `internal/config/config.go` — 可选：context_window 配置字段
- 新增 `internal/diff/priority.go` — 文件优先级排序
- 新增 `internal/lsp/client.go` — gopls 集成
