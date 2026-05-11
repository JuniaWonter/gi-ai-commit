# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build      # 编译为 git-ai 二进制
make test       # 运行全部测试
make lint       # go vet ./...
make fmt        # go fmt ./...
make deps       # go mod tidy && go mod download
make clean      # 清理构建产物
make install    # 安装到 GOPATH/bin
make git-alias  # 创建 git ai 别名

# 单包测试
go test ./internal/diff/...
go test -v ./internal/diff/ -run TestParseNumStat
```

## Architecture

### 整体流程

`main.go` 解析 CLI 子命令 → `cmd/commit.go` 编排完整提交流程 → `tui/commit_flow.go` 驱动 bubbletea TUI。

关键流程: 获取变更文件 → TUI 文件选择 → stage → AI 生成 commit → 执行工具调用 → 提交 → 验证。

### 目录结构

| 路径 | 职责 |
|------|------|
| `cmd/commit.go` | 提交流程编排：Git 检查、配置加载、AI 客户端初始化、描述生成、TUI 启动 |
| `tui/commit_flow.go` | bubbletea 模型，三阶段：文件选择(phaseSelectFiles) → AI 流式输出(phaseStreaming) → 完成(phaseDone) |
| `internal/ai/client.go` | AI 客户端核心：CommitSession 管理对话、流式 API 调用、工具执行、截断检测与降级 |
| `internal/ai/session.go` | 会话持久化（--continue 模式）：保存/加载 .git/ai-session.json |
| `internal/config/config.go` | YAML 配置加载，支持 `AI_API_KEY`/`AI_MODEL`/`AI_BASE_URL` 等环境变量覆盖 |
| `internal/diff/processor.go` | 三级 diff 降级策略：完整 diff → 压缩摘要 → 文件级摘要（按变更量排序裁剪） |
| `internal/git/` | Git 操作集合，见下方说明 |
| `internal/counter/counter.go` | 提交计数（.git/ai-commit-count），每 10 次触发描述更新 |
| `internal/description/description.go` | 项目描述读写（.git/ai-description） |
| `internal/project/project.go` | 项目结构分析，供 AI 生成描述使用 |

### internal/git 包

| 文件 | 职责 |
|------|------|
| `commit.go` | git commit/amend/reset，提后验证（VerifyCommit） |
| `conventions.go` | 检测 commit-msg hook、commit template、历史提交风格 |
| `diff.go` | DiffOverview、GetFileDiff（AI 工具调用） |
| `errors.go` | 提交错误分类（可恢复/不可恢复） |
| `files.go` | 变更文件获取，gitignore 模式匹配 |
| `priority.go` | 文件审查优先级计算（改动量 × 引用数） |
| `search.go` | grep 引用搜索（search_references 工具） |
| `tool.go` | AI 工具定义：read_file, list_tree, git_commit, diff_overview, read_diff, search_references, report_review, git_commit_amend |

### AI 审查流程

1. 用户选择文件 → stage 后获取实际 diff
2. AI 接收到 diff 后自动调用工具：
   - `diff_overview` → 了解变更概览
   - `read_file` → 读取关键代码（限定行范围）
   - `report_review` → 输出结构化审查结果
   - `git_commit` → 提交（最终目标）
3. 提交失败自动重试（最多 3 次），使用 `git_commit_amend` 修正
4. 提交后独立验证（`git rev-parse HEAD`）

### 关键设计

- **Diff 降级**：根据字节数自动选择策略（完整 → 按变更量排序的压缩摘要 → 仅文件列表 + AI 按需 read_diff）
- **Token 管理**：启动时估计 token，超过上下文窗口 85% 启用紧凑模式（compact prompt + 更激进的 diff 截断）；对话历史自动压缩（保留最近 3 轮工具结果）
- **截断检测**：finish_reason=length + 启发式规则，自动降级重试或从截断内容中提取 commit message
- **并发工具执行**：非 commit 工具并行执行，commit 工具串行执行；read_file/list_tree 有调用上限防止 token 浪费
- **自适应 read_file 限制**：根据变更文件数动态调整 read_file 调用上限（<3文件:4次, <10文件:8次, <25文件:12次, ≥25文件:16次），大变更集有更多阅读配额。通过 `GIT_AI_MAX_READ_FILE_CALLS` 环境变量固定覆盖
- **大变更集文件分组索引**：diff payload 开头注入按目录分组的变更文件索引（>3文件时自动添加），帮助 AI 快速建立文件关系认知
- **文件类型感知优先级**：`internal/git/priority.go` 计算优先级时区分 core/test/config/generated 类型，core 文件权重 1.5x，test 文件 0.3x，config 文件 0.5x，generated 文件 0.1x
- **Session Continue**：提交后保存对话到 .git/ai-session.json，`--continue` 模式复用历史 + 追加新变更
- **Session Continue**：提交后保存对话到 .git/ai-session.json，`--continue` 模式复用历史 + 追加新变更
- **描述懒加载**：项目描述在后台线程生成（首次/每 10 次），TUI 先启动不阻塞
- **文件描述符**：启动时自动提升 macOS 的 NOFILE 软限制（256→硬限制），避免 TUI + 多 git 子进程耗尽

## 关键类型与接口

```go
// Panel — 所有阶段面板必须实现的接口
type Panel interface {
    Init() tea.Cmd                                       // 面板激活时的初始命令
    Update(msg tea.Msg) (Panel, tea.Cmd)                 // 消息处理
    View(width, height int) string                       // 渲染内容，width/height 为终端尺寸
    Help() []HelpEntry                                   // 底部快捷键说明
}

// StreamActor — goroutine 生命周期管理
// Run(fn) → 启动 goroutine，返回 tea.Cmd
// NextMsgCmd() → 轮询 channel 取一条消息
// Stop() → 关闭 channel 终止 goroutine
```

### 关键消息类型（额外自行看文件）

| 消息 | 定义位置 | 用途 |
|------|----------|------|
| `streamChunkMsg` | `commit_flow.go` | AI 流式输出块（Thinking/Content/Done） |
| `aiRoundMsg` | `commit_flow.go` | AI 一轮工具调用完成（pending or err） |
| `stageDoneMsg` | `commit_flow.go` | Stage 操作完成 |
| `OverlayResult` | `overlay.go` | 用户确认/取消 |
| `filePanelMsg` | `file_panel.go` | 文件选择完成 |

### 消息流

```
FileSelector → filePanelMsg → commit_flow.handleFilePanelMsg
  → startStageCmd → stageDoneMsg → handleStageDone
  → startGenerateCmd
    → StreamActor goroutine
      → streamChunkMsg → handleStreamChunk (更新 StreamingPanel 显示)
      → aiRoundMsg → handleAiRound (处理工具调用/完成)

handleAiRound: pending 需要确认 → overlay → OverlayResult → handleOverlayResult
                           → execPendingCmd
handleAiRound: 无 pending  → switchToDone
```

### Viewport & 尺寸约定

```
View(width, height) 的 width/height 是终端总尺寸。
Panel 内部偏移：
  contentH = 终端高度 - 2(header) - 2(footer 余量)  // 实际 View 调用传 h-2
  contentW = 终端宽度 - 4(左右 padding + border 余量)
  
StreamingPanel 内部：
  viewport.Width = width (终端宽度)
  viewport.Height = contentH = height - 4
  viewport 自带 Padding(0, 1) → 内容区 = width - 2
  
重要：SetViewportSize 必须在 Panel 创建后立即调用一次（保证 vpReady），
    窗口变化时 WindowSizeMsg 必须转发到 Panel。
```

### 常见踩坑

- **WindowSizeMsg** 必须向上层和 Panel 都转发，否则内部 viewport 永不初始化（vpReady=false）
- **Spinner.Update** 必须在 Panel.Update() 的 default 分支调用，否则 spinner 停转
- **StreamActor.Run()** 返回 `tea.Cmd`（用 NextMsgCmd），不是直接返回 `tea.Msg`
- **contentH** 不能 ≤0，必须在减法后 clamp 到 ≥1
- **Overlay 作为底部栏**：不覆盖屏幕，只替换 FooterBar 为 3 行高的确认条
- **renderMarkdown** 的 `Width()` 必须在所有行上设置，否则长文本不换行
- **contentW** 不够时列表渲染要单独 clamp 避免负宽度

### 技术栈

- Go 1.24 + 标准库
- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI 框架
- [lipgloss](https://github.com/charmbracelet/lipgloss) — 样式
- [go-openai](https://github.com/sashabaranov/go-openai) — OpenAI 兼容 API（DeepSeek/通义千问等）

### 行为规范
- 尽量不要使用python 脚本对文件进行修改，除了批量的文件操作
- 对于批量的文件操作（重命名、移动、批量替换），先 review 确认列表再执行

## Agent 协作设计模式

以下模式基于实际经验总结，能显著提高处理效果：

### 1. 广度优先探索 → 深度优先执行
复杂任务前先让 Agent 做全貌扫描：
```
用 explore agent 搜索：哪些文件引用了 StreamActor？列出所有调用方
```
得到精确文件列表和行号后再操作。避免 Agent 在搜索上消耗上下文。

### 2. 精确路径引用的 Prompt
```
坏: AI 输出不换行
好: tui/stream_panel.go:168 的 outputLog 没有 Width 约束
```
好的 prompt 省掉 Agent 一轮全文搜索。

### 3. 小步验证
每次只改少量文件 → `go build ./...` → 确认。比批量改完再编译少很多回退成本。

### 4. 利用 Explore Agent 做独立研究
```
Explore: 搜索 tui/ 下所有调用 View(width, height) 的地方
```
主 Agent 上下文保持清洁，只拿结果。

### 5. 大文件拆分
>500 行的文件 Agent 读一次就消耗大量上下文。按功能拆分会直接影响输出质量。

### 6. 跨会话上下文用 Memory
```
保存到记忆: 这个项目 Overlay 已经改为底部确认栏，不覆盖屏幕
```
下次对话 Agent 直接读到，不需要重新分析。

