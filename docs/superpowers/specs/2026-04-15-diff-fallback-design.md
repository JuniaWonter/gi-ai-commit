# 三级 Diff 降级策略设计文档

**日期**: 2026-04-15
**状态**: 待审核

## 概述

将参考项目 (`wefeed-utils-gen/git-ai`) 的智能 diff 处理逻辑移植到当前项目 (`gi-ai-commit`)，实现按 diff 大小预判的三级降级策略，解决大变更场景下的 token 超限问题。

## 架构设计

### 组件关系

```
cmd/commit.go
    │
    ▼
internal/diff/processor.go  ← 新增 DiffProcessor
    │
    ├── GetStagedDiff()     ← 现有 diff.go
    ├── BuildPayloads()     ← 新增：三级降级核心方法
    │   ├── Level 1: 完整 diff
    │   ├── Level 2: 压缩摘要
    │   └── Level 3: 文件级摘要
    │
    └── 辅助函数
        ├── buildCompactDiff()
        ├── parseNumStat()
        ├── sortSections()
        └── truncateText()
```

### 数据结构

```go
type DiffPromptConfig struct {
    MaxFullDiffBytes    int  // 完整 diff 最大字节数 (默认 24000)
    MaxCompactDiffBytes int  // 压缩摘要最大字节数 (默认 16000)
    MaxPerFileDiffBytes int  // 单文件 diff 最大字节数 (默认 2200)
    MaxCompactDiffFiles int  // 压缩摘要最大文件数 (默认 12)
}

type DiffPayload struct {
    Mode    string  // "完整 diff" | "压缩摘要" | "文件级摘要"
    Content string  // 实际内容
}

type diffSection struct {
    Path    string
    Content string
    Score   int  // 变更行数 (added + deleted)
}
```

## 核心逻辑

### BuildPayloads() 流程

1. 执行 `git diff --cached --no-ext-diff --unified=1` 获取完整 diff
2. 如果 `len(diff) <= MaxFullDiffBytes` → 返回 `[完整 diff]`
3. 否则构建压缩摘要：
   - 执行 `git diff --cached --numstat` 获取行数统计
   - 按变更量排序文件
   - 提取前 N 个文件的关键 patch（每文件 ≤ MaxPerFileDiffBytes）
   - 总大小 ≤ MaxCompactDiffBytes
4. 如果压缩摘要构建成功 → 返回 `[压缩摘要]`
5. 否则返回 `[文件级摘要]`（仅统计 + 文件列表）

### 压缩摘要格式

```
以下代码变更过大，已自动压缩。请优先依据变更统计、文件列表和关键 patch 生成一条准确的 commit message。

## 变更统计
<git diff --stat 输出>

## 文件列表
<git diff --name-status 输出>

## 关键 Patch（已截断）
<按变更量排序的前 N 个文件 diff，每个截断至 MaxPerFileDiffBytes>
```

### 文件级摘要格式

```
以下代码变更过大，未附带完整 patch。请仅根据变更统计和文件列表生成一条概括性的 commit message。

## 变更统计
<git diff --stat 输出>

## 文件列表
<git diff --name-status 输出>
```

## 配置更新

在 `config.yaml` 新增 `diff_prompt` 段：

```yaml
diff_prompt:
  max_full_diff_bytes: 24000
  max_compact_diff_bytes: 16000
  max_per_file_diff_bytes: 2200
  max_compact_diff_files: 12
```

### Config 结构变更

```go
type Config struct {
    DeepSeek   DeepSeekConfig    `yaml:"deepseek"`
    Commit     CommitConfig      `yaml:"commit"`
    DiffPrompt DiffPromptConfig  `yaml:"diff_prompt"`
}
```

## 与现有代码集成

### 替换点

- `cmd/commit.go` 中 `diff.FormatDiffForAI(diffContent, cfg.Commit.MaxDiffLines)` 
- 替换为 `diffProcessor.BuildPayloads()` 取 `payloads[0].Content`

### 保留函数

- `GetChangedFiles()` - 文件选择
- `StageFiles()` - 暂存文件
- `LimitDiffLines()` - 项目描述生成用
- `GetSmartDiffSummary()` - 可能用于其他场景

### 删除/废弃

- `FormatDiffForAI()` - 被新策略替代（可保留但标记 deprecated）

## 错误处理

| 场景 | 处理 |
|------|------|
| `git diff` 无输出 | 返回空数组，调用方判断无变更 |
| numstat 解析失败 | 跳过该行，继续处理 |
| 所有 payload 构建失败 | 返回错误 "没有可用的 diff 输入" |
| 文件路径解析失败 | 使用空路径，不影响排序 |

## 测试策略

1. **单元测试**: `parseNumStat()`, `sortSections()`, `truncateText()`
2. **集成测试**: `BuildPayloads()` 使用 mock git 输出
3. **边界测试**: 空 diff、超大 diff、单文件超大变更

## 文件清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/diff/processor.go` | 新增 | DiffProcessor 核心实现 |
| `internal/config/config.go` | 修改 | 添加 DiffPromptConfig |
| `cmd/commit.go` | 修改 | 使用新 BuildPayloads() |
| `config.example.yaml` | 修改 | 添加 diff_prompt 配置示例 |
| `internal/diff/diff.go` | 保留 | FormatDiffForAI 标记 deprecated |
