### fafoing over harness engineering in zig and golang 

the filesystem tools (read, write, edit, glob, grep, list_dir) as a native zig binary that speaks JSON-RPC over stdio. go spawns it as a subprocess, talks to it over stdin/stdout, and feeds results back to the llms.

why zig instead of ts? big files. harness chokes on large file edits because node's GC and string copies eat memory like crazy. zig just scans raw bytes in place — no gc pauses, no extra allocations, starts in ~1ms instead of node's 100-200ms cold start.

the go side handles the agent loop, bedrock api, and tool dispatch. zig does the actual filesystem work. clean separation — go doesn't touch files directly, zig doesn't know about LLMs.

```
go main.go → go-ai agent → bedrock claude → tool_use
    ↓
go tool dispatch → stdin pipe → zig binary → filesystem ops
    ↓
zig stdout → go client → tool result → back to claude
```

built with zig 0.15 (yes we survived writergate) and go 1.25.