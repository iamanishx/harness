# axeai

A minimal coding agent that speaks the [Agent Client Protocol (ACP)](https://agentclientprotocol.com/) over stdio. It connects to editors like Zed as a custom agent server, talks to AWS Bedrock for LLM inference, and uses a native Zig binary for fast filesystem operations.

Built in Go, except the filesystem tools which are written in Zig for performance.

## How it works

```
Editor (Zed)
  |  ACP over stdio (JSON-RPC 2.0)
  v
src/main.go              entry point, wires everything together
  |
  ├── acp/               ACP protocol server + interceptor for unstable methods
  ├── agent/             LLM agent loop, system prompt, tool instrumentation
  ├── lsp/               post-write diagnostic checks (gopls, tsc, deno, cargo, zig, pyright, ruff)
  ├── runtime/           session manager, message store, event bus, replay
  ├── storage/           SQLite persistence (sessions, messages, parts)
  └── tools/
      ├── filesystem/    Go client + Zig binary for file operations
      └── shell/         run_command tool (sh/powershell)
```

When you type a message in Zed:

1. Zed sends a `prompt` JSON-RPC call over stdin
2. The ACP server loads conversation history from SQLite and starts the agent loop
3. The agent streams the prompt to Claude on AWS Bedrock
4. If Claude calls a tool, it runs via the Zig binary (file ops) or `sh -c` (shell commands)
5. After every file write or edit, the file is checked by whatever language checker is on PATH and diagnostics are fed back to the agent so it can self-correct
6. Text deltas and tool updates stream back to Zed in real time

Sessions, messages, and tool call results persist to SQLite at `~/.goai/harness.db`.

## Project structure

```
axeai/
├── acp/
│   ├── server.go        ACP protocol handler (initialize, newSession, prompt, loadSession, cancel)
│   └── intercept.go     stdin/stdout interceptor for session/list and sessionCapabilities
├── agent/
│   ├── agent.go         LLM agent loop, tool instrumentation, diff capture, LSP integration
│   ├── prompt.go        embeds prompt.txt at compile time
│   └── prompt.txt       system prompt (edit this to change agent behavior)
├── lsp/
│   ├── lsp.go           Check() and Format() entry points
│   ├── detect.go        maps file extensions to available checkers on PATH
│   └── parse.go         parses checker output into []Diagnostic
├── runtime/
│   ├── session.go       session manager + message store (CRUD to SQLite)
│   ├── event.go         pub/sub event bus with wildcard support
│   ├── replay.go        reconstructs session history from DB for loadSession
│   ├── tool.go          tool runner
│   └── permission.go    permission service
├── storage/
│   ├── schema.go        SQLite table definitions
│   ├── db.go            database operations
│   └── db_test.go       storage tests
├── tools/
│   ├── filesystem/
│   │   ├── client.go    Go client for the Zig binary (JSON-RPC over stdio)
│   │   ├── tools.go     tool definitions (read_file, write_file, edit_file, grep, glob, list_dir)
│   │   └── zig-bindings/src/main.zig
│   └── shell/
│       └── shell.go     run_command tool (sh on Linux/Mac, powershell on Windows)
├── types/
│   ├── message.go       Message, Part, TextData
│   ├── session.go       Session struct
│   └── tool.go          ToolState, ToolPart
├── src/
│   └── main.go          entry point
├── Makefile
├── go.mod
└── go.sum
```

## Setup

You need:
- Go 1.22+
- Zig 0.13+
- AWS account with Bedrock access and Claude enabled in your region

```bash
git clone https://github.com/youruser/axeai
cd axeai

# build and configure Zed in one shot
make zed AWS_PROFILE=myprofile AWS_REGION=us-east-1
```

Then open Zed, open the Agent panel, and pick **axeai** from the dropdown.

### Makefile targets

```
make                  build zig + go binaries
make install          build and copy binary to ~/.local/bin
make zed              build, install, and patch Zed settings.json
make clean            remove build artifacts
make test             run go tests
```

### Options

```
make zed AWS_PROFILE=myprofile AWS_REGION=us-west-2 MODEL=us.anthropic.claude-opus-4-5-v1:0
```

| Variable | Default | Description |
|----------|---------|-------------|
| `AWS_PROFILE` | `default` | AWS credentials profile |
| `AWS_REGION` | `us-east-1` | Bedrock region |
| `MODEL` | `us.anthropic.claude-sonnet-4-7-20250219-v1:0` | Bedrock model ID |

### Manual Zed configuration

If you prefer to edit Zed's `settings.json` yourself:

```json
{
  "agent_servers": {
    "axeai": {
      "command": "/home/youruser/.local/bin/axeai",
      "args": ["--profile", "myprofile", "--region", "us-east-1"]
    }
  }
}
```

### CLI flags

```
--db-path    SQLite database path  (default: ~/.goai/harness.db)
--region     AWS region            (default: us-east-1)
--profile    AWS profile           (default: default)
--model      Bedrock model ID      (default: us.anthropic.claude-sonnet-4-7-20250219-v1:0)
```

Logs go to `~/.goai/logs/goai.log`.

## Why Zig for filesystem tools

The filesystem tools handle raw file I/O. Zig scans raw bytes in place with no GC pauses, no extra allocations, and starts in about 1ms.

| File size | Zig binary | Node/TS |
|-----------|------------|---------|
| 1 KB      | ~2 MB RSS  | ~50 MB  |
| 10 MB     | ~22 MB RSS | ~100 MB |
| 50 MB     | ~102 MB RSS| ~400 MB |

## LSP diagnostics

After every `write_file` or `edit_file`, the file is checked by the first available tool on PATH:

| Language | Checkers tried (in order) |
|----------|--------------------------|
| Go | `gopls check`, `go build ./...` |
| TypeScript | `deno check`, `tsc --noEmit`, `npx tsc` |
| JavaScript | `deno check`, `eslint` |
| Python | `pyright`, `ruff check`, `flake8` |
| Rust | `cargo check --message-format short` |
| Zig | `zig build-exe --dry-run`, `zig build check` |

If errors are found they are appended to the tool result and the agent fixes them automatically before responding.

## Dependencies

- [github.com/coder/acp-go-sdk](https://github.com/coder/acp-go-sdk) ACP protocol
- [github.com/iamanishx/go-ai](https://github.com/iamanishx/go-ai) LLM agent loop and Bedrock streaming
- [modernc.org/sqlite](https://modernc.org/sqlite) pure-Go SQLite, no CGO
