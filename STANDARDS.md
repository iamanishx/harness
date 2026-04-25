# Contributor Standards

This document explains how the project is structured, how tools work, and what to follow when contributing.

---

## Project Philosophy

- Keep logs minimal. Only errors and things that actually matter operationally.
- No backward compatibility. If something needs to change, change it.
- Make the smallest change that solves the problem.

---

## How Tools Work

A tool in this project is a `provider.Tool` from the `go-ai` SDK:

```go
type Tool struct {
    Name        string
    Description string
    Parameters  map[string]interface{}
    Execute     func(input map[string]interface{}) (string, error)
}
```

LLMs never executes anything directly. It only sees `Name`, `Description`, and `Parameters` - these go into the  API request so LLMs knows what tools exist and what arguments they accept. When LLMs decides to call a tool, the SDK calls `Execute(input)` and sends the returned string back to LLM as the tool result.

So defining a tool means:

1. Give it a clear `Name` (snake_case, matches what LLM will call)
2. Write a `Description` that tells LLM exactly when to use it
3. Define `Parameters` as a JSON Schema object
4. Implement `Execute` - read from `input`, do the work, return a string

### Where tools live

```
tools/
  filesystem/
    tools.go     read_file, write_file, edit_file, grep, glob, list_dir
    client.go    Go client that talks to the Zig binary over JSON-RPC
  shell/
    shell.go     run_command
```

Filesystem tools delegate to the Zig binary via `client.Call(toolName, params, &result)`. Shell tool runs `sh -c` on Linux/Mac and `powershell -NoProfile -Command` on Windows.

If you add a new tool that does file I/O, put it in `tools/filesystem/tools.go`. If it runs external commands, put it in `tools/shell/`. If it is something else entirely, create a new package under `tools/`.

### Adding a new tool

1. Define the `provider.Tool` in the appropriate package
2. Add it to the list returned by `Tools()` or expose it as a standalone function like `shell.Tool(cwd)`
3. Register it in `agent/agent.go` where `allTools` is assembled:

```go
allTools := append(toolFactory.Tools(), shell.Tool(cwd))
```

4. Add the tool name to `toolKind()` in `acp/server.go` so Zed renders the right icon:

```go
func toolKind(name string) acp.ToolKind {
    switch name {
    case "read_file":
        return acp.ToolKindRead
    case "write_file", "edit_file":
        return acp.ToolKindEdit
    case "grep", "glob", "list_dir":
        return acp.ToolKindSearch
    case "run_command":
        return acp.ToolKindExecute
    default:
        return acp.ToolKindOther
    }
}
```

5. Update `agent/prompt.txt` to tell the agent the tool exists and when to use it.

---

## How instrumentedTools Works

`instrumentedTools` is middleware. It takes the raw tool list and wraps every `Execute` function with a closure that adds persistence, diff capture, LSP checking, and event publishing - without touching the tool definitions themselves.

Here is the sequence for every single tool call:

```
OnToolCallStart fires (SDK callback, just before Execute)
  └── stores LLM's callID in pendingCallIDs[toolName]

wrapped Execute(input) called by SDK
  │
  ├── 1. grab callID from pendingCallIDs[toolName] and delete it
  │
  ├── 2. if write op: read current file content from disk (oldContent)
  │
  ├── 3. AddToolPart(state=pending) to SQLite + fires EventPartCreated
  │         Zed receives this and shows the tool widget
  │
  ├── 4. UpdateToolPart(state=running)
  │         Zed shows spinner
  │
  ├── 5. originalExecute(input)
  │         the real work happens here (Zig binary or sh -c)
  │
  ├── 6. on success:
  │     ├── read new file content from disk (newContent)
  │     ├── run lsp.Check(filePath) if write op
  │     │     if diagnostics found, append them to the tool result
  │     │     so LLM sees them and self-corrects
  │     └── UpdateToolPart(state=completed, output, oldContent, newContent)
  │           Zed shows checkmark + diff view
  │
  ├── 7. on error:
  │     └── UpdateToolPart(state=error, errorMsg)
  │           Zed shows red X
  │
  ├── 8. if write op: publish EventFileChanged
  │
  └── 9. return result to SDK → SDK sends it to LLM
```

### The callID handshake

The LLM assigns each tool call a unique ID (e.g. `tooluse_abc123`). This ID needs to be on the tool part stored in SQLite so Zed can track the right widget. The problem is that `Execute` does not receive the callID as a parameter - only `input` comes through.

The fix: `OnToolCallStart` fires with the callID just before `Execute` is called. We store it in `pendingCallIDs[toolName]`. Inside the wrapper, we read and delete it immediately. Since the SDK calls tools sequentially, there is no race condition for the same tool name.

```go
OnToolCallStart: func(e agentpkg.OnToolCallStartEvent) {
    pendingMu.Lock()
    pendingCallIDs[e.ToolName] = e.ToolCallID
    pendingMu.Unlock()
},

// inside wrapped Execute:
pendingMu.Lock()
callID := pendingCallIDs[t.Name]
delete(pendingCallIDs, t.Name)
pendingMu.Unlock()
```

### What instrumentedTools does NOT do

It does not change what the tool returns to LLM. If you append diagnostics to `toolPart.Output` for storage purposes, you also need to include them in the actual return value so LLM sees them. Currently the LSP diagnostics are appended to the returned string so LLM does see them.

It does not retry on error. If `originalExecute` fails, the error is stored and returned. LLM decides what to do next.

---

## Storage Model

Everything is stored in SQLite at `~/.goai/harness.db`.

```
session
  id, cwd, title, time_created, time_updated

message
  id, session_id, time_created, data (JSON: {role})

part
  id, message_id, session_id, time_created, data (JSON envelope)
```

Parts are stored as JSON envelopes:

```json
{"type": "text", "payload": {"text": "..."}}
{"type": "tool", "payload": {"call_id": "...", "tool": "read_file", "state": "completed", ...}}
```

The `type` field on the envelope is how `GetParts` knows what struct to deserialize the payload into. Always use `marshalPartData` and `UnmarshalPartData` from `runtime/session.go` - never write raw JSON for parts.

---

## Event Bus

The event bus in `runtime/event.go` is how the agent loop notifies the ACP server of things happening during a run. It is a simple pub/sub with wildcard support.

Events:

| Event | When | Data |
|-------|------|------|
| `message.created` | new message persisted | `types.Message` |
| `message.part.created` | new part persisted | `types.Part` |
| `message.part.updated` | part state changed | `map[string]any{"part_id": ..., "tool": types.ToolPart}` |
| `message.part.delta` | text streaming delta | `map[string]string{"text": ..., "message_id": ...}` |
| `file.changed` | write op completed | `map[string]string{"path": ..., "op": ..., "cwd": ...}` |

The ACP server subscribes to all events for the current session and translates them into ACP `session_update` notifications sent to Zed over stdout.

Always publish events with `SessionID` set. The server filters by session ID - events without it are silently dropped.

---

## ACP Protocol Notes

ACP is JSON-RPC 2.0 over stdio. One JSON object per line. Zed spawns the binary once and keeps it alive for the session.

The Go SDK (v0.6.3) does not support `session/list` or `sessionCapabilities`. Both are handled in `acp/intercept.go` which wraps stdin/stdout and intercepts these methods before the SDK sees them.

`session/list` is filtered by `cwd`. The cwd is captured from the `session/new` params when Zed first connects, since Zed never sends cwd in the `session/list` params themselves.

When adding new ACP methods the SDK does not support, add them to the intercept layer, not the SDK layer.

---

## LSP Diagnostics

After every `write_file` or `edit_file`, `lsp.Check(filePath, cwd)` is called. It:

1. Detects the file extension
2. Finds the first available checker on PATH for that extension
3. Runs it with a 30 second timeout
4. Parses the output into `[]Diagnostic{File, Line, Col, Level, Message}`

To add a new language checker, edit `lsp/detect.go` and add an entry to `extCheckers`. To support a new output format, add a parser in `lsp/parse.go`.

Checkers are tried in order. The first one found on PATH wins. Put the most reliable or fastest checker first.

---

## System Prompt

The system prompt lives in `agent/prompt.txt` and is embedded into the binary at compile time via `//go:embed`. It is rendered at runtime with `{{.CWD}}` replaced by the session working directory.

Edit `agent/prompt.txt` to change agent behavior. Rebuild after changes. The prompt tells the agent:

- What tools are available and when to use each one
- How to behave (explore before acting, prefer edit over write)
- How to handle diagnostics (fix errors silently)
- Where to write planning files (always `.agent/` folder)

---

## Code Standards

- No global state outside of `main.go`
- No init() functions
- Errors are wrapped with context: `fmt.Errorf("doing thing: %w", err)`
- Log only at the top level of an operation, not inside helpers
- No `panic` outside of truly unrecoverable situations
- All exported types and functions in a package should be intentional - do not export things just because another package needs them temporarily
- Tests go in `_test.go` files in the same package
