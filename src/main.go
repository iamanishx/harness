package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	acpserver "goai-test/acp"
	goairuntime "goai-test/runtime"
	"goai-test/storage"
)

func main() {
	dbPath := flag.String("db-path", defaultDBPath(), "path to SQLite database")
	region := flag.String("region", "us-east-1", "AWS region for Bedrock")
	profile := flag.String("profile", "clickpe", "AWS profile for Bedrock")
	modelID := flag.String("model", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "Bedrock model ID")
	flag.Parse()

	logDir := filepath.Join(filepath.Dir(defaultDBPath()), "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logFile, err := os.OpenFile(filepath.Join(logDir, "goai.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatalf("create db dir: %v", err)
	}

	db, err := storage.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	zigBinaryPath, err := resolveZigBinary()
	if err != nil {
		log.Fatalf("prepare zig binary: %v", err)
	}

	bus := goairuntime.NewEventBus()
	sessions := goairuntime.NewSessionManager(db, bus)
	messages := goairuntime.NewMessageStore(db, bus)

	agentLoop := goairuntime.NewAgentLoop(goairuntime.AgentLoopConfig{
		Region:        *region,
		Profile:       *profile,
		ModelID:       *modelID,
		ZigBinaryPath: zigBinaryPath,
		Store:         messages,
		Bus:           bus,
	})

	srv := acpserver.NewServer(sessions, messages, agentLoop, db)
	stdinProxy := acpserver.NewInterceptReader(os.Stdin, os.Stdout, db)
	stdoutProxy := acpserver.NewInterceptWriter(os.Stdout, stdinProxy.InitID)
	conn := acp.NewAgentSideConnection(srv, stdoutProxy, stdinProxy)
	srv.SetConnection(conn)

	<-conn.Done()
}

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".goai/harness.db"
	}
	return filepath.Join(home, ".goai", "harness.db")
}

func resolveZigBinary() (string, error) {
	self, err := os.Executable()
	if err != nil {
		self = "."
	}
	selfDir := filepath.Dir(self)

	candidates := []string{
		filepath.Join(selfDir, "tools/filesystem/zig-bindings/zig-out/bin/filesystem_mcp"),
		filepath.FromSlash("./tools/filesystem/zig-bindings/zig-out/bin/filesystem_mcp"),
	}
	if runtime.GOOS == "windows" {
		for i, c := range candidates {
			candidates[i] = c + ".exe"
		}
	}

	for _, c := range candidates {
		if fileExists(c) {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs, nil
			}
			return c, nil
		}
	}

	zigExec, err := resolveZigExecutable()
	if err != nil {
		return "", err
	}

	zigProjectDir := filepath.FromSlash("./tools/filesystem/zig-bindings")
	zigBinaryPath := candidates[1]

	log.Printf("zig binary not found; building...")
	cmd := exec.Command(zigExec, "build")
	cmd.Dir = zigProjectDir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	if !fileExists(zigBinaryPath) {
		return "", os.ErrNotExist
	}
	abs, err := filepath.Abs(zigBinaryPath)
	if err != nil {
		return zigBinaryPath, nil
	}
	return abs, nil
}

func resolveZigExecutable() (string, error) {
	if p := strings.TrimSpace(os.Getenv("ZIG_BIN")); p != "" {
		if isExecutableFile(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath("zig"); err == nil {
		return p, nil
	}
	for _, c := range []string{
		"/usr/local/bin/zig", "/usr/bin/zig",
		"/opt/homebrew/bin/zig", "/snap/bin/zig",
		filepath.FromSlash("/mnt/c/Program Files/zig/zig.exe"),
		filepath.FromSlash("/mnt/c/zig/zig.exe"),
	} {
		if isExecutableFile(c) {
			return c, nil
		}
	}
	for _, sh := range [][]string{{"bash", "-lc"}, {"zsh", "-lc"}, {"sh", "-lc"}} {
		out, err := exec.Command(sh[0], sh[1], "command -v zig").Output()
		if err == nil {
			if p := strings.TrimSpace(string(out)); isExecutableFile(p) {
				return p, nil
			}
		}
	}
	return "", os.ErrNotExist
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
