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
- **Session Continue**：提交后保存对话到 .git/ai-session.json，`--continue` 模式复用历史 + 追加新变更
- **描述懒加载**：项目描述在后台线程生成（首次/每 10 次），TUI 先启动不阻塞
- **文件描述符**：启动时自动提升 macOS 的 NOFILE 软限制（256→硬限制），避免 TUI + 多 git 子进程耗尽

### 技术栈

- Go 1.24 + 标准库
- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI 框架
- [lipgloss](https://github.com/charmbracelet/lipgloss) — 样式
- [go-openai](https://github.com/sashabaranov/go-openai) — OpenAI 兼容 API（DeepSeek/通义千问等）

### 行为规范
- 尽量不要使用python 脚本对文件进行修改，除了批量的文件操作

