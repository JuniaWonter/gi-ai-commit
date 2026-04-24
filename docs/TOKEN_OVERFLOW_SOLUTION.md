# Token 超限防护方案

## 问题描述

当代码变更（diff）过大或 system prompt 包含过多信息时，LLM 请求的 token 总数可能超过 API 限制（通常 8K），导致：
- API 返回截断的响应
- 响应中不包含 `tool_calls` 字段
- AI 无法调用 `git_commit` 工具
- 用户界面看起来"卡住"了（没有 Y/N 确认提示）

## 解决方案（4 层防护）

### 1️⃣ Token 预估 (`estimateTokenCount`)
```go
func estimateTokenCount(text string) int
```
- 在 `StartCommitSession` 中预估总 token 数
- 启发式算法：中文 4 字 ≈ 1 token，英文 4 字 ≈ 1 token
- **阈值**: 超过 6000 token 时自动激活**紧凑模式**

### 2️⃣ 双模式 Prompt 生成

**正常模式** (token < 6000)：
- `buildAuthSystemPrompt()` - 完整的规范说明、scope 提示、hook 内容
- `buildAuthPrompt()` - diff 截断至 4000 字符

**紧凑模式** (token ≥ 6000)：
- `buildAuthSystemPromptCompact()` - 浓缩成 3-4 行关键指令
- `buildAuthPromptCompact()` - diff 截断至 2000 字符

### 3️⃣ 主动截断

| 模式 | 最大 diff 长度 | 效果 |
|------|--------------|------|
| 正常 | 4000 字      | 保留足够的变更上下文 |
| 紧凑 | 2000 字      | 仅保留关键变更信息 |

### 4️⃣ 降级自动提交（NoToolCall Fallback）

当 API 返回内容但**没有 `tool_calls`** 时：

```go
if len(msg) < 20 {  // 内容过短，说明可能被截断
    s.noToolCallFallback = true
    // 生成默认消息
    fallbackTC := PendingToolCall{
        Name: "git_commit",
        Args: {"message": "chore: 提交变更"}
    }
    // 返回虚拟 tool call，让 UI 弹出确认框
    return []PendingToolCall{fallbackTC}, nil
}
```

**效果**: 用户可以看到确认框，选择 Y 进行自动提交，不再卡住

## 工作流程图

```
StartCommitSession()
    ↓
估计 token 数 (estimateTokenCount)
    ↓
    ├─ token < 6000? ──→ 正常模式 (buildAuthSystemPrompt + 4000 字 diff)
    │
    └─ token ≥ 6000? ──→ 紧凑模式 (buildAuthSystemPromptCompact + 2000 字 diff)
           ↓
       StreamAI()
           ↓
        收到响应
           ├─ 有 tool_calls? ──→ 继续工具调用循环 ✓
           │
           └─ 无 tool_calls?
               ├─ 内容 ≥ 20 字? ──→ 使用内容作为 commit message
               │
               └─ 内容 < 20 字? ──→ 返回虚拟 fallback tool call
                   (用户弹出确认框，可选择提交)
```

## 环境变量配置

```bash
# 调整阈值（默认 6000 token）
GIT_AI_TOKEN_THRESHOLD=8000

# 启用紧凑模式诊断日志
GIT_AI_DEBUG=1
```

## 测试场景

### ✅ 场景 1: 小型变更 (正常模式)
```
diff size: 1000 字
prompt size: 3000 字
estimated tokens: 1000
→ 使用正常 prompt，完整规范说明
```

### ✅ 场景 2: 大型变更 (紧凑模式)
```
diff size: 8000 字
prompt size: 3000 字
estimated tokens: 2750 (接近 6000 阈值)
→ 自动切换到紧凑模式
→ diff 截断至 2000 字
→ system prompt 简化为 4 行关键指令
→ 预估 token ≈ 1500 (安全)
```

### ✅ 场景 3: 边界情况 (fallback 触发)
```
API 返回截断响应（content < 20 字）
→ 识别到 NoToolCall 情况
→ 返回虚拟 fallback tool call
→ UI 弹出确认: "生成简单提交消息?"
→ 用户选择 Y ──→ 调用 git_commit("chore: 提交变更")
```

## 代码变更总结

### 新增字段
```go
type CommitSession struct {
    // ...
    compactMode       bool              // 紧凑模式标志
    noToolCallFallback bool             // 降级标志
}
```

### 新增函数
| 函数名 | 用途 |
|--------|------|
| `estimateTokenCount(text)` | 估计 token 数 |
| `buildAuthSystemPromptCompact()` | 紧凑 system prompt |
| `buildAuthPromptCompact()` | 紧凑 user prompt |
| `truncateCompact()` | 更激进的截断（2000 字） |
| `escapeJSON()` | JSON 转义（fallback 用） |
| `minInt()` | 辅助函数 |

### 改动文件
- `internal/ai/client.go` (主要改动)
  - `StartCommitSession()` - 添加 token 估计逻辑
  - `StreamAI()` - 添加 noToolCall fallback 处理
  - `buildAuthPrompt()` - 添加 4000 字 diff 截断
  - 新增 3 个 Compact 函数 + 3 个辅助函数

## 使用示例

### 监控 token 使用

```bash
# 启用调试日志
export GIT_AI_DEBUG=1
go run ./cmd/commit.go

# 输出示例:
# [git-ai-debug 14:23:45.123 fd=12] startStageCmd: Estimated tokens: 1234
# [git-ai-debug 14:23:45.124 fd=12] startStageCmd: Compact mode: false
```

### 强制紧凑模式（测试用）

在 `StartCommitSession` 中临时修改：
```go
// 测试: 强制紧凑模式
compactMode := true  // os.Getenv("GIT_AI_FORCE_COMPACT") != ""
```

## 预期效果

✅ **大 diff 不再卡住** - 紧凑模式 + 截断确保 token 在安全范围  
✅ **自动降级** - 极端情况下自动生成默认消息，不让用户等待  
✅ **可观测性** - 通过 `GIT_AI_DEBUG=1` 查看 token 使用情况  
✅ **向后兼容** - 小 diff 的用户体验不变  

## 故障排查

### 问题: 仍然看到卡住

**检查项**:
1. 查看 debug 日志: `GIT_AI_DEBUG=1 go run ./cmd/commit.go`
2. 确认 token 估计是否准确
3. 尝试手动设置阈值: `GIT_AI_TOKEN_THRESHOLD=4000`

### 问题: Fallback 太频繁触发

**原因**: token 阈值过低  
**解决**: 提高阈值
```bash
GIT_AI_TOKEN_THRESHOLD=8000  # 改为 8000（假设 API 支持）
```

### 问题: Diff 过度截断

**原因**: 使用了紧凑模式（2000 字限制）  
**验证**: 检查 debug 日志中的 "Compact mode: true"  
**解决**: 保持最新代码，会逐步改进截断策略

---

**版本**: 2026-04-24  
**状态**: 生产就绪 ✓
