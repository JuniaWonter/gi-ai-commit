# git-ai-commit

> [English](./README.en.md) | 中文

AI 驱动的 Git commit message 生成工具，支持 DeepSeek、通义千问等多种 AI 模型，自动生成符合 Conventional Commits 规范的提交信息。

## 功能特性

- ✨ 自动生成符合 Conventional Commits 规范的 commit message
- 🎯 支持 scope 自动识别
- 📝 首次提交自动生成项目描述
- 🔄 每 10 次提交自动更新项目描述
- 🎨 TUI 交互式文件选择
- ⚡ 支持自动确认提交模式
- 🔄 智能 diff 处理：三级降级策略，自动适配变更大小
- 🤖 支持多种 AI 模型：DeepSeek、通义千问、OpenAI 等

## 安装

### 1. 编译安装

```bash
# 下载依赖
make deps

# 编译
make build

# 安装到 GOPATH/bin
make install

# 创建 git 别名 (可选)
make git-alias
```

### 2. 配置

安装后会自动创建配置文件 `~/.config/ai-commit/config.yaml`

编辑配置文件，配置多个模型：

```yaml
ai:
  default_model: "deepseek"  # 默认使用的模型
  models:
    deepseek:
      api_key: "sk-your-deepseek-api-key"
      model: "deepseek-chat"
      base_url: "https://api.deepseek.com"
    qwen:
      api_key: "sk-your-dashscope-api-key"
      model: "qwen-plus"
      base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
    openai:
      api_key: "sk-your-openai-api-key"
      model: "gpt-4"
      base_url: "https://api.openai.com/v1"
```

## 使用方法

### 基本用法

```bash
# 使用默认模型提交
git ai commit

# 自动提交模式
git ai commit -y

# 指定模型提交（使用 models 中配置的模型名称）
git ai commit --model qwen
git ai commit --model openai
```

### 工作流程

1. **选择文件** - 使用 TUI 界面选择要提交的文件
   - `↑↓` 或 `j/k` 移动光标
   - `Space` 选择/取消选择
   - `A` 全选
   - `D` 取消全选
   - `S` 确认选择
   - `Q` 退出

2. **生成 commit message** - AI 分析代码变更生成提交信息

3. **确认提交** - 预览生成的 message，输入 `Y` 确认或 `N` 取消

## 项目描述

工具会在 `.git/ai-description` 文件中存储项目描述：

- **首次提交**：自动生成项目描述
- **每 10 次提交**：自动更新项目描述
- **后续提交**：使用已有描述生成更准确的 commit message

## 提交计数

工具会在 `.git/ai-commit-count` 文件中记录 AI 提交次数，用于触发描述更新。

## Commit Message 格式

遵循 [Conventional Commits](https://www.conventionalcommits.org/) 规范：

```
<type>(<scope>): <subject>

<body>
```

**Type 类型：**
- `feat`: 新功能
- `fix`: 修复 bug
- `docs`: 文档变更
- `style`: 代码格式
- `refactor`: 重构
- `test`: 测试相关
- `chore`: 构建/工具/配置

## 示例

```bash
$ git ai commit

📊 检查 Git 仓库...
📊 获取变更文件...
📝 选择要提交的文件...
[交互式文件选择界面]

📦 暂存文件...
📊 获取代码变更...
⚙️  加载配置...
🤖 初始化 AI 客户端...
📋 检查仓库描述...
🤖 生成 commit message...

📝 生成的 commit message:
──────────────────────────────────────────
feat(auth): 实现 JWT 令牌刷新机制

- 添加 refreshToken 接口
- 实现令牌有效期验证
- 更新中间件处理逻辑
──────────────────────────────────────────

确认提交？(Y/n): Y
✅ 提交中...
✅ 提交成功!
```

## 配置说明

配置文件位于 `~/.config/ai-commit/config.yaml`

```yaml
ai:
  default_model: "deepseek"   # 默认使用的模型名称
  models:                     # 多个模型配置
    deepseek:
      api_key: "sk-xxx"
      model: "deepseek-chat"
      base_url: "https://api.deepseek.com"
      timeout: "30s"
    qwen:
      api_key: "sk-xxx"
      model: "qwen-plus"
      base_url: "https://dashscope.aliyuncs.com/compatible-mode/v1"

commit:
  default_scope: ""           # 默认 scope
  max_diff_lines: 500         # Diff 最大行数限制

diff_prompt:                  # 三级降级策略配置
  max_full_diff_bytes: 24000    # 完整 diff 最大字节数
  max_compact_diff_bytes: 16000 # 压缩摘要最大字节数
  max_per_file_diff_bytes: 2200 # 单文件 diff 最大字节数
  max_compact_diff_files: 12    # 压缩摘要最大文件数
```

### 支持的模型

| 模型名称 | model 值 | base_url |
|----------|----------|----------|
| DeepSeek | `deepseek-chat` | `https://api.deepseek.com` |
| 通义千问 | `qwen-plus`, `qwen-turbo`, `qwen-max` | `https://dashscope.aliyuncs.com/compatible-mode/v1` |
| OpenAI | `gpt-3.5-turbo`, `gpt-4` | `https://api.openai.com/v1` |

### 切换模型

```bash
# 使用默认模型
git ai commit

# 使用指定模型（models 中定义的 key）
git ai commit --model qwen
git ai commit --model openai
```

## 开发

```bash
# 格式化代码
make fmt

# 代码检查
make lint

# 运行测试
make test

# 清理
make clean
```

## 依赖

- [bubbletea](https://github.com/charmbracelet/bubbletea) - TUI 框架
- [lipgloss](https://github.com/charmbracelet/lipgloss) - 样式库
- [go-openai](https://github.com/sashabaranov/go-openai) - OpenAI 兼容 API 客户端
- [yaml.v3](https://gopkg.in/yaml.v3) - YAML 解析

## License

MIT
