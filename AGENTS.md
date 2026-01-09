# AGENTS.md

This file provides guidance for agentic coding assistants working in this repository.

## 🔒 File Editing Rules (MANDATORY)

### ❌ Forbidden File Modification Methods

Agents MUST NOT modify files using shell commands or pipelines, including but not limited to:

- sed
- awk
- perl -pi
- cat > file
- cat <<EOF > file
- tee
- echo > file
- printf > file
- ed
- ex
- dd
- apply_patch
- Any command that pipes, redirects, or transforms file contents

These commands may be used ONLY for reading (e.g. `cat file`, `sed -n`, `less`) and NEVER for writing or editing.

---

### ✅ Allowed File Editing Method (ONLY METHOD)

Agents MUST edit files by directly editing the file contents using the editor or file-editing interface provided by the environment.

- Edits must be made as full or partial file edits
- The agent must open the file, modify it, and save it
- Changes must be intentional, minimal, and clearly scoped

If the agent cannot directly edit a file, it MUST stop and report that the task cannot be completed under these rules.

---

### 📐 Editing Standards

When editing a file:

- Do NOT rewrite unrelated sections
- Preserve formatting, ordering, and comments
- Do NOT auto-reformat unless explicitly instructed
- Prefer small, targeted edits
- Never regenerate an entire file unless explicitly required

---

### 🛑 Failure Mode

If a task would normally require shell-based file manipulation:

1. Do NOT attempt a workaround
2. Do NOT use sed/cat/tee or similar tools
3. Clearly report:

   > “This change requires direct file editing, which is not available in the current environment.”

---

### 🧠 Rationale

Shell-based edits:
- Obscure intent
- Are hard to review
- Frequently introduce subtle corruption
- Make diffs unreliable

Direct file edits are mandatory for correctness and reviewability.



## Development Commands

### Build
```bash
go build -v -o build/claude-squad
```

### Testing
```bash
# Run all tests
go test -v ./...

# Run tests for a specific package
go test -v ./config

# Run a single test
go test -v ./config -run TestGetClaudeCommand

# Run tests with race detection
go test -race ./...
```

### Linting & Formatting
```bash
# Format code
gofmt -w .

# Check formatting
gofmt -l .

# Run linter (same as CI)
golangci-lint run --timeout=3m --out-format=line-number --fast --max-issues-per-linter=0 --max-same-issues=0
```

### Go Version
- Go: 1.23.0
- Toolchain: go1.24.1

## Code Style Guidelines

### Imports
1. Standard library
2. Local packages (claude-squad/*)
3. Third-party packages

Group imports with blank lines between sections. Within sections, sort alphabetically.

### Naming Conventions
- **Public types/functions**: PascalCase (e.g., `NewInstance`, `TmuxSession`)
- **Private types/functions**: camelCase (e.g., `newTmuxSession`, `combineErrors`)
- **Constants**: PascalCase for exported, camelCase for private (e.g., `GlobalInstanceLimit`)
- **Interface names**: Simple, descriptive names (e.g., `PtyFactory`, `Executor`)
- **Receiver names**: Single letters that reflect the type (e.g., `t *TmuxSession`, `i *Instance`)

### Error Handling
- Always wrap errors with `fmt.Errorf` using `%w` for error chains
- Log errors using `log.ErrorLog.Printf` when they occur
- Use early returns for error conditions
- When combining multiple errors, create a clear error message listing all failures
- Test error paths in unit tests

```go
if err != nil {
    return fmt.Errorf("failed to do something: %w", err)
}
```

### Testing Guidelines
- Use `testify/assert` for assertions and `testify/require` for failures
- Implement table-driven tests for multiple test cases
- Use `TestMain` for package-level setup (e.g., logger initialization)
- Use `t.TempDir()` for temporary file/directory creation
- Clean up after tests (use defer for cleanup)
- Name tests with `TestFunctionName` or `TestFunctionName_Scenario`

```go
func TestSomething(t *testing.T) {
    t.Run("scenario", func(t *testing.T) {
        // test code
    })
}
```

### Documentation
- Exported functions, types, and package-level variables must have doc comments
- Struct fields should have inline comments explaining their purpose
- Comments should be concise and explain "why" not "what"

### Formatting
- Use `gofmt -w .` to format all code
- No trailing whitespace
- Maximum line length is not strictly enforced but should be reasonable (~120 chars)

### UI Architecture (Bubble Tea)
This project uses the Bubble Tea TUI framework:
- Models implement `Init()`, `Update(msg)`, and `View()`
- Update methods return `(model, tea.Cmd)`
- Use type assertions for custom messages
- State management uses explicit state enums (e.g., `stateDefault`, `stateNew`)

### Project-Specific Patterns
- **Git Worktrees**: Each instance gets its own worktree for isolation
- **Tmux Sessions**: Managed via `tmux` package with session prefix `claudesquad_`
- **Configuration**: Stored in `~/.claude-squad/config.json`
- **State Persistence**: Uses JSON serialization for instance storage
- **Cleanup**: Always implement proper cleanup in defer blocks or Close methods

### Package Organization
- `app/`: Main TUI application and home screen
- `config/`: Configuration management
- `session/`: Instance management (tmux, git worktrees)
- `ui/`: UI components (list, menu, preview, overlays)
- `daemon/`: Background daemon for auto-yes mode
- `log/`: Logging utilities
- `keys/`: Key bindings management

### Additional Notes
- The project manages multiple AI agent instances (Claude, Aider, Codex, etc.)
- Each instance operates in an isolated git branch
- Tmux is used for session management and isolation
- Auto-yes mode can automatically accept prompts from AI agents
- Maximum instance limit is 10 (GlobalInstanceLimit)
