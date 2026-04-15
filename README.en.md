# git-ai-commit

> English | [中文](./README.md)

AI-powered Git commit message generator that uses DeepSeek API to automatically generate commit messages following the Conventional Commits specification.

## Features

- ✨ Auto-generate commit messages following Conventional Commits
- 🎯 Automatic scope detection
- 📝 Auto-generate project description on first commit
- 🔄 Auto-update project description every 10 commits
- 🎨 TUI interactive file selection
- ⚡ Auto-confirm mode with `-y` flag
- 🔄 Smart diff processing: three-level degradation strategy, auto-adapt to change size

## Installation

### 1. Build from source

```bash
# Download dependencies
make deps

# Build
make build

# Install to GOPATH/bin
make install

# Create git alias (optional)
make git-alias
```

### 2. Configuration

After installation, config file will be created at `~/.config/ai-commit/config.yaml`

Edit the config and add your DeepSeek API Key:

```yaml
deepseek:
  api_key: "sk-your-api-key-here"
```

## Usage

### Basic Usage

```bash
# Preview mode (confirm before committing)
git ai commit

# Auto-confirm mode
git ai commit -y

# Or
git ai commit --yes
```

### Workflow

1. **Select files** - Use TUI to select files to commit
   - `↑↓` or `j/k` to move
   - `Space` to select/deselect
   - `A` to select all
   - `D` to deselect all
   - `S` to confirm
   - `Q` to quit

2. **Generate commit message** - AI analyzes code changes

3. **Confirm** - Preview message, input `Y` to confirm or `N` to cancel

## Project Description

The tool stores project description in `.git/ai-description`:

- **First commit**: Auto-generate project description
- **Every 10 commits**: Auto-update description
- **Subsequent commits**: Use existing description for more accurate messages

## Commit Count

The tool records AI commit count in `.git/ai-commit-count` to trigger description updates.

## Commit Message Format

Follows [Conventional Commits](https://www.conventionalcommits.org/) specification:

```
<type>(<scope>): <subject>

<body>
```

**Type options:**
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes
- `refactor`: Refactoring
- `test`: Test related
- `chore`: Build/tools/config

## Example

```bash
$ git ai commit

📊 Checking Git repository...
📊 Getting changed files...
📝 Selecting files to commit...
[Interactive file selection UI]

📦 Staging files...
📊 Getting code changes...
⚙️  Loading config...
🤖 Initializing AI client...
📋 Checking project description...
🤖 Generating commit message...

📝 Generated commit message:
──────────────────────────────────────────
feat(auth): implement JWT token refresh

- Add refreshToken endpoint
- Implement token expiration validation
- Update middleware logic
──────────────────────────────────────────

Confirm commit? (Y/n): Y
✅ Committing...
✅ Commit successful!
```

## Configuration

Config file location: `~/.config/ai-commit/config.yaml`

```yaml
deepseek:
  api_key: "sk-xxx"      # DeepSeek API Key
  model: "deepseek-chat" # Model name
  base_url: "https://api.deepseek.com"
  timeout: "30s"         # Request timeout

commit:
  default_scope: ""      # Default scope
  max_diff_lines: 500    # Max diff lines limit

diff_prompt:             # Three-level degradation strategy config
  max_full_diff_bytes: 24000    # Max full diff bytes
  max_compact_diff_bytes: 16000 # Max compact diff bytes
  max_per_file_diff_bytes: 2200 # Max per-file diff bytes
  max_compact_diff_files: 12    # Max compact diff files
```

## Environment Variables

You can also use environment variables to override config:

```bash
export DEEPSEEK_API_KEY="sk-xxx"
export DEEPSEEK_MODEL="deepseek-chat"
export DEEPSEEK_BASE_URL="https://api.deepseek.com"
export DEEPSEEK_TIMEOUT="30s"
```

## Development

```bash
# Format code
make fmt

# Lint
make lint

# Run tests
make test

# Clean
make clean
```

## Options

```bash
git-ai-commit commit [options]

Options:
  -y, --yes       Auto-confirm commit
  -d, --dry-run   Preview only, don't commit
```

## Dependencies

- [bubbletea](https://github.com/charmbracelet/bubbletea) - TUI framework
- [lipgloss](https://github.com/charmbracelet/lipgloss) - Style library
- [go-openai](https://github.com/sashabaranov/go-openai) - OpenAI compatible API client
- [yaml.v3](https://gopkg.in/yaml.v3) - YAML parser

## License

MIT