# harness

A minimal coding agent that speaks the [Agent Client Protocol (ACP)](https://agentclientprotocol.com/) over stdio. It connects to editors like Zed as a custom agent server, talks to AWS Bedrock for LLM inference, and uses a native Zig binary for fast filesystem operations.

The whole thing is built in Go, except the filesystem tools which are written in Zig for performance reasons (more on that below).

## How it works

```
Editor (Zed)
  |  ACP over stdio (JSON-RPC)
  v
src/main.go          -- entry point, wires everything together
  |
  ├── acp/           -- ACP protocol server + interceptor for unstable methods
  ├── runtime/       -- agent loop, session management, event bus, tool runner
  ├── storage/       -- SQLite persistence (sessions, messages, parts)
  └── tools/
      └── filesystem/
          ├── client.go       -- Go client that talks to the Zig binary over stdin/stdout
          └── zig-bindings/   -- native Zig binary for file operations
```

When you type a message in the editor:

1. Zed sends a `session/prompt` JSON-RPC call over stdin
2. The ACP server picks it up, loads any conversation history from SQLite, and starts the agent loop
3. The agent loop streams the prompt (with history) to Claude on Bedrock
4. If Claude asks to use a tool (read a file, edit something, grep for code), the tool runner spawns the Zig binary with `cmd.Dir` set to the session's working directory and pipes the request over JSON-RPC
5. Tool results go back to Claude, and the cycle continues until Claude is done
6. Text deltas stream back to Zed in real time through ACP session updates

Sessions, messages, and tool call results all get persisted to SQLite at `~/.goai/harness.db`, so you can resume conversations after restarting.

## Why Zig for filesystem tools

The filesystem tools (read, write, edit, glob, grep, list_dir) handle raw file I/O. Node.js based tools struggle with large files because of V8's garbage collector and immutable string copies. Zig just scans raw bytes in place with no GC pauses, no extra allocations, and starts in about 1ms vs Node's 100-200ms cold start.

Measured with `/usr/bin/time -v` (peak RSS):

| File size | Zig binary | Node/TS (typical) |
|-----------|------------|-------------------|
| 1 KB      | ~2 MB      | ~50 MB            |
| 10 MB     | ~22 MB     | ~100-150 MB       |
| 50 MB     | ~102 MB    | ~300-500 MB       |

Zig's memory usage is almost entirely the file data itself. Node has the V8 heap baseline (~30-50MB) before it even opens a file, plus 3-4x copies from string operations and GC overhead.

## Project structure

```
goai-test/
├── acp/
│   ├── server.go        -- ACP protocol handler (initialize, newSession, prompt, loadSession, cancel)
│   └── intercept.go     -- stdin/stdout interceptor for session/list and sessionCapabilities
├── runtime/
│   ├── agent.go         -- LLM agent loop with streaming, tool correlation, history
│   ├── session.go       -- session manager + message store (CRUD to SQLite)
│   ├── event.go         -- pub/sub event bus with wildcard support
│   ├── replay.go        -- reconstructs session history from DB for loadSession
│   ├── tool.go          -- tool runner wrapping the Zig filesystem client
│   └── permission.go    -- permission service (auto-allow for now)
├── storage/
│   ├── schema.go        -- SQLite table definitions
│   ├── db.go            -- database operations
│   └── db_test.go       -- storage tests
├── tools/
│   └── filesystem/
│       ├── client.go    -- Go client for the Zig binary (JSON-RPC over stdio)
│       ├── tools.go     -- tool definitions (name, description, parameters)
│       └── zig-bindings/
│           └── src/main.zig
├── types/
│   ├── message.go       -- Message, Part, TextData, PatchData
│   ├── session.go       -- Session struct
│   └── tool.go          -- ToolState enum, ToolPart struct
├── src/
│   └── main.go          -- entry point
├── go.mod
└── go.sum
```

## Setup

You need Go 1.25+, Zig 0.15+, and an AWS profile with Bedrock access.

```bash
# build the zig binary first
cd tools/filesystem/zig-bindings && zig build && cd ../../..

# build the go binary
go build -o goai-test ./src/

# or just run it (it will build the zig binary automatically if missing)
go build -o goai-test ./src/ && ./goai-test
```

The binary reads from stdin and writes to stdout using ACP's JSON-RPC protocol. You don't run it directly. Instead, configure your editor to launch it.

### Zed configuration

Add this to your Zed `settings.json`:

```json
{
  "agent_servers": {
    "goai": {
      "type": "custom",
      "command": "/path/to/goai-test",
      "args": []
    }
  }
}
```

Then open the Agent panel in Zed and pick "goai" from the model dropdown.

### CLI flags

```
--db-path    path to SQLite database (default: ~/.goai/harness.db)
--region     AWS region for Bedrock (default: us-east-1)
--profile    AWS profile for Bedrock (default: clickpe)
--model      Bedrock model ID (default: us.anthropic.claude-sonnet-4-5-20250929-v1:0)
```

Logs go to `~/.goai/logs/goai.log`.

## How sessions work

Every conversation is a session. Sessions are stored in SQLite with their messages and parts (text chunks, tool calls with input/output/timing).

When Zed starts the agent, it calls `initialize`, then `session/new` to create a fresh session. It also calls `session/list` to populate the session history panel. When you pick an old session, Zed calls `session/load` and the agent replays all the stored messages back as ACP session updates so the editor can render the full conversation.

Within a running session, every new prompt loads the full conversation history from the DB and sends it to the LLM along with the new message. This gives the model context of everything that happened before.

The ACP Go SDK (v0.6.3) doesn't support `session/list` or `sessionCapabilities` natively, so we use a stdin/stdout interceptor that catches these methods before the SDK sees them and handles them directly.

## Dependencies

- [github.com/coder/acp-go-sdk](https://github.com/coder/acp-go-sdk) for the ACP protocol
- [github.com/iamanishx/go-ai](https://github.com/iamanishx/go-ai) for the LLM agent loop and Bedrock streaming
- [modernc.org/sqlite](https://modernc.org/sqlite) for pure-Go SQLite (no CGO)
