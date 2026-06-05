# AGENTS.md

## Commands

```bash
make build      # go build -o git-ai
make test       # go test ./...
make lint       # go vet ./...
make fmt        # go fmt ./...
make deps       # go mod tidy && go mod download

# Single package test
go test ./internal/diff/...
go test -v ./internal/diff/ -run TestParseNumStat
```

## Architecture

**Flow**: `main.go` → `cmd/commit.go` (orchestration) → `tui/commit_flow.go` (bubbletea TUI)

**Key directories**:
- `cmd/` - Commit flow orchestration (git checks, config, AI init, TUI launch)
- `tui/` - bubbletea UI: file selection → AI streaming → done
- `internal/ai/` - AI client, session management, tool execution
- `internal/git/` - Git operations (commit, diff, files, search, tools)
- `internal/diff/` - Three-tier diff degradation (full → compact → file-level)
- `internal/config/` - YAML config with env var overrides (`AI_API_KEY`, `AI_MODEL`, `AI_BASE_URL`)
- `internal/skill/` - Skill system (MCP-based extensible tool plugins)
- `internal/mcp/` - MCP (Model Context Protocol) client for external tool servers
- `internal/memory/` - Project memory persistence (`.git/ai-memory`)
- `internal/logger/` - Structured logging to `~/.config/ai-commit/logs/`

**AI tool flow**: User selects files → stage → AI calls tools freely (diff_overview, read_file, git_status, git_log, report_review, ask_user, git_commit, etc.) → commit → verify

**Git as a tool**: AI has free access to all Git operations (status/log/branch/stash/add/restore/diff/blame/tag). No rigid execution order. AI uses `ask_user` to confirm commit message before calling `git_commit`.

## Critical patterns

**Diff degradation**: Auto-selects strategy by byte count (full → compact summary sorted by change size → file list + on-demand `read_diff`)

**Token management**: Estimates tokens at startup; >85% context window triggers compact mode (aggressive truncation + shorter prompt). Conversation history auto-compresses (keeps last 3 tool results).

**Truncation detection**: `finish_reason=length` + heuristic rules → auto-retry with degradation or extract commit message from truncated output

**Concurrent tools**: Non-commit tools run in parallel; commit tools run serially. `read_file`/`list_tree` have call limits to prevent token waste.

**Adaptive read_file limits**: Dynamic call limits based on changed file count (<3 files: 4 calls, <10: 8, <25: 12, ≥25: 16). Override with `GIT_AI_MAX_READ_FILE_CALLS` env var.

**Session continue**: Saves conversation to `.git/ai-session.json` after commit. `--continue` flag reuses history + appends new changes.

## TUI gotchas

- **WindowSizeMsg** must forward to both parent and Panel, else internal viewport never initializes (`vpReady=false`)
- **Spinner.Update** must run in Panel.Update() default branch, else spinner stops
- **StreamActor.Run()** returns `tea.Cmd` (uses `NextMsgCmd`), not `tea.Msg`
- **contentH** must clamp to ≥1 after subtraction
- **Overlay** is a bottom confirmation bar, not a screen overlay (replaces FooterBar). Only used for `summarize_changes` phase transition; git_commit uses `ask_user` tool instead
- **renderMarkdown** needs `Width()` set on all lines for wrapping
- **Streaming rendering**: Live `streamContent`/`streamThinking` use `renderStreamText` (plain text, no regex) to avoid layout jumps as incomplete markdown is parsed. Full `renderMarkdown` only applied to `reviewOutput` after `FlushStream`.
- **Unclosed code blocks**: `renderStreamText` shows code block content with dim styling even when `` ``` `` hasn't closed yet, preventing content from disappearing during streaming.
- **Viewport sizing**: `SetViewportSize` must be called once after Panel creation; `WindowSizeMsg` must forward to Panel on resize

## File type priority

`internal/git/priority.go` weights files: core (1.5x), test (0.3x), config (0.5x), generated (0.1x)

## Tech stack

Go 1.24, bubbletea (TUI), lipgloss (styles), go-openai (OpenAI-compatible API for DeepSeek/Qwen/OpenAI)

## Behavioral notes

- Avoid Python scripts for file modifications except batch operations
- For batch operations (rename, move, replace), review confirmation list before executing
- macOS file descriptor limit: auto-raises soft limit (256 → hard limit) at startup to avoid exhaustion with TUI + git subprocesses
