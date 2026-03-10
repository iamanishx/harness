const std = @import("std");

const JsonValue = std.json.Value;

pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer {
        _ = gpa.deinit();
    }
    const allocator = gpa.allocator();

    var stdin_buf: [65536]u8 = undefined;
    var stdout_buf: [65536]u8 = undefined;

    var stdin_reader = std.fs.File.stdin().reader(&stdin_buf);
    var stdout_writer = std.fs.File.stdout().writer(&stdout_buf);

    const reader: *std.Io.Reader = &stdin_reader.interface;
    const writer: *std.Io.Writer = &stdout_writer.interface;

    while (true) {
        const line_opt = readLineAlloc(allocator, reader, 4 * 1024 * 1024) catch |err| {
            try writeErrorResponse(allocator, writer, null, -32700, "Read error", err);
            try writer.flush();
            continue;
        };
        defer if (line_opt) |line| allocator.free(line);
        if (line_opt == null) break;

        const line = line_opt.?;
        if (line.len == 0) continue;

        var parsed = std.json.parseFromSlice(std.json.Value, allocator, line, .{}) catch |err| {
            try writeErrorResponse(allocator, writer, null, -32700, "Parse error", err);
            try writer.flush();
            continue;
        };
        defer parsed.deinit();

        const root = parsed.value;
        if (root != .object) {
            try writeErrorResponse(allocator, writer, null, -32600, "Invalid Request", error.InvalidRequest);
            try writer.flush();
            continue;
        }

        const obj = root.object;
        const id_val = obj.get("id");
        const method_val = obj.get("method");

        if (method_val == null or method_val.? != .string) {
            try writeErrorResponse(allocator, writer, id_val, -32600, "Invalid Request: missing method", error.InvalidRequest);
            try writer.flush();
            continue;
        }

        const method = method_val.?.string;
        const params = obj.get("params");

        if (std.mem.eql(u8, method, "read_file")) {
            const result = handleReadFile(allocator, params) catch |err| {
                try writeErrorResponse(allocator, writer, id_val, -32000, "read_file failed", err);
                try writer.flush();
                continue;
            };
            defer result.deinit(allocator);
            try writeResultResponse(writer, id_val, result);
        } else if (std.mem.eql(u8, method, "write_file")) {
            const result = handleWriteFile(allocator, params) catch |err| {
                try writeErrorResponse(allocator, writer, id_val, -32000, "write_file failed", err);
                try writer.flush();
                continue;
            };
            defer result.deinit(allocator);
            try writeResultResponse(writer, id_val, result);
        } else if (std.mem.eql(u8, method, "edit_file")) {
            const result = handleEditFile(allocator, params) catch |err| {
                try writeErrorResponse(allocator, writer, id_val, -32000, "edit_file failed", err);
                try writer.flush();
                continue;
            };
            defer result.deinit(allocator);
            try writeResultResponse(writer, id_val, result);
        } else if (std.mem.eql(u8, method, "list_dir")) {
            const result = handleListDir(allocator, params) catch |err| {
                try writeErrorResponse(allocator, writer, id_val, -32000, "list_dir failed", err);
                try writer.flush();
                continue;
            };
            defer result.deinit(allocator);
            try writeResultResponse(writer, id_val, result);
        } else if (std.mem.eql(u8, method, "glob")) {
            const result = handleGlob(allocator, params) catch |err| {
                try writeErrorResponse(allocator, writer, id_val, -32000, "glob failed", err);
                try writer.flush();
                continue;
            };
            defer result.deinit(allocator);
            try writeResultResponse(writer, id_val, result);
        } else if (std.mem.eql(u8, method, "grep")) {
            const result = handleGrep(allocator, params) catch |err| {
                try writeErrorResponse(allocator, writer, id_val, -32000, "grep failed", err);
                try writer.flush();
                continue;
            };
            defer result.deinit(allocator);
            try writeResultResponse(writer, id_val, result);
        } else {
            try writeErrorResponse(allocator, writer, id_val, -32601, "Method not found", error.MethodNotFound);
        }

        try writer.flush();
    }
}

const OwnedResult = struct {
    json: []u8,
    pub fn deinit(self: OwnedResult, allocator: std.mem.Allocator) void {
        allocator.free(self.json);
    }
};

fn handleReadFile(allocator: std.mem.Allocator, params_val: ?JsonValue) !OwnedResult {
    const p = try requireObject(params_val);
    const path = try requireString(p.get("path"));
    const offset = optionalInt(p.get("offset")) orelse 1;
    const limit = optionalInt(p.get("limit")) orelse 2000;

    if (offset < 1 or limit < 1) return error.InvalidParams;

    const data = try std.fs.cwd().readFileAlloc(allocator, path, 50 * 1024 * 1024);
    defer allocator.free(data);

    var it = std.mem.splitScalar(u8, data, '\n');
    var line_no: i64 = 0;
    var returned: i64 = 0;

    var out = std.ArrayList(u8).init(allocator);
    defer out.deinit();
    var w = out.writer();

    while (it.next()) |line_raw| {
        line_no += 1;
        if (line_no < offset) continue;
        if (returned >= limit) break;

        const line = if (line_raw.len > 0 and line_raw[line_raw.len - 1] == '\r')
            line_raw[0 .. line_raw.len - 1]
        else
            line_raw;

        try w.print("{d}: {s}\n", .{ line_no, line });
        returned += 1;
    }

    const text = out.items;
    return makeOutput(allocator, text);
}

fn handleWriteFile(allocator: std.mem.Allocator, params_val: ?JsonValue) !OwnedResult {
    const p = try requireObject(params_val);
    const path = try requireString(p.get("path"));
    const content = try requireString(p.get("content"));

    try ensureParentDirs(path);
    var file = try std.fs.cwd().createFile(path, .{ .truncate = true });
    defer file.close();
    try file.writeAll(content);

    var msg = std.ArrayList(u8).init(allocator);
    defer msg.deinit();
    try msg.writer().print("Wrote {d} bytes to {s}", .{ content.len, path });
    return makeOutput(allocator, msg.items);
}

fn handleEditFile(allocator: std.mem.Allocator, params_val: ?JsonValue) !OwnedResult {
    const p = try requireObject(params_val);
    const path = try requireString(p.get("path"));
    const old_string = try requireString(p.get("old_string"));
    const new_string = try requireString(p.get("new_string"));
    const replace_all = optionalBool(p.get("replace_all")) orelse false;

    const src = try std.fs.cwd().readFileAlloc(allocator, path, 50 * 1024 * 1024);
    defer allocator.free(src);

    var out = std.ArrayList(u8).init(allocator);
    defer out.deinit();

    if (old_string.len == 0) {
        try out.appendSlice(new_string);
    } else if (!replace_all) {
        const idx_opt = std.mem.indexOf(u8, src, old_string);
        if (idx_opt == null) return error.OldStringNotFound;
        const idx = idx_opt.?;
        try out.appendSlice(src[0..idx]);
        try out.appendSlice(new_string);
        try out.appendSlice(src[idx + old_string.len ..]);
    } else {
        var pos: usize = 0;
        while (true) {
            const rel = std.mem.indexOf(u8, src[pos..], old_string);
            if (rel == null) {
                try out.appendSlice(src[pos..]);
                break;
            }
            const i = pos + rel.?;
            try out.appendSlice(src[pos..i]);
            try out.appendSlice(new_string);
            pos = i + old_string.len;
        }
    }

    try ensureParentDirs(path);
    var file = try std.fs.cwd().createFile(path, .{ .truncate = true });
    defer file.close();
    try file.writeAll(out.items);

    var msg = std.ArrayList(u8).init(allocator);
    defer msg.deinit();
    try msg.writer().print("Edited {s} successfully", .{path});
    return makeOutput(allocator, msg.items);
}

fn handleListDir(allocator: std.mem.Allocator, params_val: ?JsonValue) !OwnedResult {
    const p = try requireObject(params_val);
    const path = try requireString(p.get("path"));

    var dir = try std.fs.cwd().openDir(path, .{ .iterate = true });
    defer dir.close();

    var it = dir.iterate();

    var out = std.ArrayList(u8).init(allocator);
    defer out.deinit();
    var w = out.writer();

    while (try it.next()) |entry| {
        const suffix = switch (entry.kind) {
            .directory => "/",
            else => "",
        };
        try w.print("{s}{s}\n", .{ entry.name, suffix });
    }

    return makeOutput(allocator, out.items);
}

fn handleGlob(allocator: std.mem.Allocator, params_val: ?JsonValue) !OwnedResult {
    const p = try requireObject(params_val);
    const pattern = try requireString(p.get("pattern"));
    const base_path = optionalString(p.get("path")) orelse ".";

    var out = std.ArrayList(u8).init(allocator);
    defer out.deinit();

    try walkAndCollectGlob(allocator, base_path, pattern, &out);

    return makeOutput(allocator, out.items);
}

fn walkAndCollectGlob(
    allocator: std.mem.Allocator,
    base_path: []const u8,
    pattern: []const u8,
    out: *std.ArrayList(u8),
) !void {
    _ = allocator;
    var stack = std.ArrayList([]u8).init(std.heap.page_allocator);
    defer {
        for (stack.items) |p| std.heap.page_allocator.free(p);
        stack.deinit();
    }

    try stack.append(try std.heap.page_allocator.dupe(u8, base_path));

    while (stack.items.len > 0) {
        const current = stack.pop();
        defer std.heap.page_allocator.free(current);

        var dir = std.fs.cwd().openDir(current, .{ .iterate = true }) catch continue;
        defer dir.close();

        var it = dir.iterate();
        while (try it.next()) |entry| {
            const child = try std.fs.path.join(std.heap.page_allocator, &.{ current, entry.name });
            defer std.heap.page_allocator.free(child);

            const rel = child;
            if (globMatch(pattern, rel)) {
                try out.writer().print("{s}\n", .{rel});
            }

            if (entry.kind == .directory) {
                try stack.append(try std.heap.page_allocator.dupe(u8, child));
            }
        }
    }
}

fn handleGrep(allocator: std.mem.Allocator, params_val: ?JsonValue) !OwnedResult {
    const p = try requireObject(params_val);
    const pattern = try requireString(p.get("pattern"));
    const base_path = optionalString(p.get("path")) orelse ".";
    const include = optionalString(p.get("include"));
    const case_sensitive = optionalBool(p.get("case_sensitive")) orelse false;

    var out = std.ArrayList(u8).init(allocator);
    defer out.deinit();

    try walkAndGrep(allocator, base_path, pattern, include, case_sensitive, &out);

    if (out.items.len == 0) {
        try out.appendSlice("No matches found\n");
    }

    return makeOutput(allocator, out.items);
}

fn walkAndGrep(
    allocator: std.mem.Allocator,
    base_path: []const u8,
    pattern: []const u8,
    include: ?[]const u8,
    case_sensitive: bool,
    out: *std.ArrayList(u8),
) !void {
    _ = allocator;

    var stack = std.ArrayList([]u8).init(std.heap.page_allocator);
    defer {
        for (stack.items) |p| std.heap.page_allocator.free(p);
        stack.deinit();
    }

    try stack.append(try std.heap.page_allocator.dupe(u8, base_path));

    while (stack.items.len > 0) {
        const current = stack.pop();
        defer std.heap.page_allocator.free(current);

        var dir = std.fs.cwd().openDir(current, .{ .iterate = true }) catch continue;
        defer dir.close();

        var it = dir.iterate();
        while (try it.next()) |entry| {
            const child = try std.fs.path.join(std.heap.page_allocator, &.{ current, entry.name });
            defer std.heap.page_allocator.free(child);

            if (entry.kind == .directory) {
                try stack.append(try std.heap.page_allocator.dupe(u8, child));
                continue;
            }

            if (include) |inc| {
                if (!globMatch(inc, child)) continue;
            }

            const data = std.fs.cwd().readFileAlloc(std.heap.page_allocator, child, 10 * 1024 * 1024) catch continue;
            defer std.heap.page_allocator.free(data);

            var line_it = std.mem.splitScalar(u8, data, '\n');
            var line_no: usize = 0;
            while (line_it.next()) |line_raw| {
                line_no += 1;
                const line = if (line_raw.len > 0 and line_raw[line_raw.len - 1] == '\r')
                    line_raw[0 .. line_raw.len - 1]
                else
                    line_raw;

                if (containsPattern(line, pattern, case_sensitive)) {
                    try out.writer().print("{s}:{d}:{s}\n", .{ child, line_no, line });
                }
            }
        }
    }
}

fn containsPattern(haystack: []const u8, needle: []const u8, case_sensitive: bool) bool {
    if (needle.len == 0) return true;
    if (case_sensitive) return std.mem.indexOf(u8, haystack, needle) != null;

    var buf_h: [4096]u8 = undefined;
    var buf_n: [512]u8 = undefined;

    if (haystack.len <= buf_h.len and needle.len <= buf_n.len) {
        for (haystack, 0..) |c, i| buf_h[i] = std.ascii.toLower(c);
        for (needle, 0..) |c, i| buf_n[i] = std.ascii.toLower(c);
        return std.mem.indexOf(u8, buf_h[0..haystack.len], buf_n[0..needle.len]) != null;
    }

    var arena = std.heap.ArenaAllocator.init(std.heap.page_allocator);
    defer arena.deinit();
    const a = arena.allocator();

    const h = a.alloc(u8, haystack.len) catch return false;
    const n = a.alloc(u8, needle.len) catch return false;

    for (haystack, 0..) |c, i| h[i] = std.ascii.toLower(c);
    for (needle, 0..) |c, i| n[i] = std.ascii.toLower(c);

    return std.mem.indexOf(u8, h, n) != null;
}

fn globMatch(pattern: []const u8, text: []const u8) bool {
    return globMatchRec(pattern, text, 0, 0);
}

fn globMatchRec(pattern: []const u8, text: []const u8, pi: usize, ti: usize) bool {
    if (pi == pattern.len) return ti == text.len;

    if (pattern[pi] == '*') {
        if (pi + 1 < pattern.len and pattern[pi + 1] == '*') {
            var k = ti;
            while (k <= text.len) : (k += 1) {
                if (globMatchRec(pattern, text, pi + 2, k)) return true;
            }
            return false;
        } else {
            var k = ti;
            while (k <= text.len) : (k += 1) {
                if (k > ti and text[k - 1] == '/') break;
                if (globMatchRec(pattern, text, pi + 1, k)) return true;
            }
            return false;
        }
    }

    if (pattern[pi] == '?') {
        if (ti < text.len and text[ti] != '/') {
            return globMatchRec(pattern, text, pi + 1, ti + 1);
        }
        return false;
    }

    if (ti < text.len and pattern[pi] == text[ti]) {
        return globMatchRec(pattern, text, pi + 1, ti + 1);
    }

    return false;
}

fn ensureParentDirs(path: []const u8) !void {
    const maybe_dir = std.fs.path.dirname(path);
    if (maybe_dir) |d| {
        if (d.len > 0) {
            try std.fs.cwd().makePath(d);
        }
    }
}

fn makeOutput(allocator: std.mem.Allocator, content: []const u8) !OwnedResult {
    var out = std.ArrayList(u8).init(allocator);
    defer out.deinit();

    try out.appendSlice("{\"output\":");
    try writeJSONString(out.writer(), content);
    try out.appendSlice("}");

    return OwnedResult{
        .json = try out.toOwnedSlice(),
    };
}

fn writeResultResponse(writer: *std.Io.Writer, id_val: ?JsonValue, result: OwnedResult) !void {
    try writer.writeAll("{\"jsonrpc\":\"2.0\",\"id\":");
    try writeID(writer, id_val);
    try writer.writeAll(",\"result\":");
    try writer.writeAll(result.json);
    try writer.writeAll("}\n");
}

fn writeErrorResponse(
    allocator: std.mem.Allocator,
    writer: *std.Io.Writer,
    id_val: ?JsonValue,
    code: i64,
    message: []const u8,
    err: anyerror,
) !void {
    _ = allocator;
    _ = err;
    try writer.writeAll("{\"jsonrpc\":\"2.0\",\"id\":");
    try writeID(writer, id_val);
    try writer.writeAll(",\"error\":{\"code\":");
    try writer.print("{d}", .{code});
    try writer.writeAll(",\"message\":");
    try writeJSONString(writer, message);
    try writer.writeAll("}}\n");
}

fn writeID(writer: *std.Io.Writer, id_val: ?JsonValue) !void {
    if (id_val == null) {
        try writer.writeAll("null");
        return;
    }
    const id = id_val.?;
    switch (id) {
        .integer => |v| try writer.print("{d}", .{v}),
        .float => |v| try writer.print("{d}", .{v}),
        .string => |s| try writeJSONString(writer, s),
        else => try writer.writeAll("null"),
    }
}

// Read a line from the new std.Io.Reader, returns null on EOF.
fn readLineAlloc(
    allocator: std.mem.Allocator,
    reader: *std.Io.Reader,
    max_len: usize,
) !?[]u8 {
    var buf = std.ArrayList(u8).init(allocator);
    errdefer buf.deinit();

    while (true) {
        const byte = reader.takeByte() catch |err| switch (err) {
            error.EndOfStream => {
                if (buf.items.len == 0) {
                    buf.deinit();
                    return null;
                }
                return buf.toOwnedSlice();
            },
            else => return err,
        };

        if (byte == '\n') break;
        if (buf.items.len >= max_len) return error.LineTooLong;
        try buf.append(byte);
    }

    return buf.toOwnedSlice();
}

fn writeJSONString(writer: anytype, s: []const u8) !void {
    try writer.writeByte('"');
    for (s) |c| {
        switch (c) {
            '"' => try writer.writeAll("\\\""),
            '\\' => try writer.writeAll("\\\\"),
            '\n' => try writer.writeAll("\\n"),
            '\r' => try writer.writeAll("\\r"),
            '\t' => try writer.writeAll("\\t"),
            0x08 => try writer.writeAll("\\b"),
            0x0C => try writer.writeAll("\\f"),
            else => {
                if (c < 0x20) {
                    try writer.print("\\u{X:0>4}", .{@as(u32, c)});
                } else {
                    try writer.writeByte(c);
                }
            },
        }
    }
    try writer.writeByte('"');
}

fn requireObject(val: ?JsonValue) !std.json.ObjectMap {
    if (val == null or val.? != .object) return error.InvalidParams;
    return val.?.object;
}

fn requireString(val: ?JsonValue) ![]const u8 {
    if (val == null or val.? != .string) return error.InvalidParams;
    return val.?.string;
}

fn optionalString(val: ?JsonValue) ?[]const u8 {
    if (val == null) return null;
    if (val.? != .string) return null;
    return val.?.string;
}

fn optionalInt(val: ?JsonValue) ?i64 {
    if (val == null) return null;
    switch (val.?) {
        .integer => |v| return v,
        .float => |v| return @intFromFloat(v),
        else => return null,
    }
}

fn optionalBool(val: ?JsonValue) ?bool {
    if (val == null) return null;
    if (val.? != .bool) return null;
    return val.?.bool;
}
