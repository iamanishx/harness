package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	agentpkg "github.com/iamanishx/go-ai/agent"
	"github.com/iamanishx/go-ai/provider/bedrock"

	"goai-test/tools/filesystem"
)

func main() {
	zigProjectDir := filepath.FromSlash("./tools/filesystem/zig-bindings")
	zigBinaryPath := filepath.FromSlash("./tools/filesystem/zig-bindings/zig-out/bin/filesystem_mcp")
	if runtime.GOOS == "windows" {
		zigBinaryPath += ".exe"
	}

	zigExec, err := resolveZigExecutable()
	if err != nil {
		log.Fatalf("failed to resolve Zig executable: %v", err)
	}

	if err := ensureZigBinary(zigExec, zigProjectDir, zigBinaryPath); err != nil {
		log.Fatalf("failed to prepare filesystem MCP binary: %v", err)
	}

	fsClient, err := filesystem.NewClient(zigBinaryPath)
	if err != nil {
		log.Fatalf("failed to start filesystem MCP client: %v", err)
	}
	defer func() {
		if cerr := fsClient.Close(); cerr != nil {
			log.Printf("filesystem MCP client close error: %v", cerr)
		}
	}()

	toolFactory := filesystem.NewToolFactory(fsClient)

	provider := bedrock.Create(bedrock.BedrockProviderSettings{
		Region:  "us-east-1",
		Profile: "clickpe",
	})

	ag := agentpkg.CreateToolLoopAgent(agentpkg.ToolLoopAgentSettings{
		Model:        provider.Chat("us.anthropic.claude-sonnet-4-5-20250929-v1:0"),
		Tools:        toolFactory.Tools(),
		ExecuteTools: true,
		MaxSteps:     20,
	})

	ctx := context.Background()
	s, err := ag.Stream(ctx, agentpkg.AgentCallOptions{
		Prompt: "change the 20-24 races to 50-60 race in @f1.txt",
	})
	if err != nil {
		log.Fatalf("agent failed: %v", err)
	}
	defer s.Close()

	for part := range s.Part() {
		switch part.Type {
		case "text-delta":
			fmt.Print(part.Text)
		case "tool-call":
			fmt.Printf("\n[tool-call: %s]\n", part.ToolName)
		case "tool-input-delta":
			fmt.Print(part.ToolInputDelta)
		case "tool-result":
			fmt.Printf("\n[tool-result: %s]\n", part.ToolName)
		case "finish":
			fmt.Printf("\n[finish: %s]\n", part.FinishReason)
		case "error":
			fmt.Printf("\n[error: %v]\n", part.Error)
		}
	}
}

func ensureZigBinary(zigExec, zigProjectDir, zigBinaryPath string) error {
	if fileExists(zigBinaryPath) {
		return nil
	}

	log.Printf("zig MCP binary not found at %s; attempting to build with `%s build`...", zigBinaryPath, zigExec)

	cmd := exec.Command(zigExec, "build")
	cmd.Dir = zigProjectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zig build failed in %s: %w", zigProjectDir, err)
	}

	if !fileExists(zigBinaryPath) {
		return fmt.Errorf("zig build completed but binary still not found at %s", zigBinaryPath)
	}

	log.Printf("zig MCP binary built successfully: %s", zigBinaryPath)
	return nil
}

func resolveZigExecutable() (string, error) {
	// 1) Explicit env override.
	if p := strings.TrimSpace(os.Getenv("ZIG_BIN")); p != "" {
		if isExecutableFile(p) {
			return p, nil
		}
		return "", fmt.Errorf("ZIG_BIN is set but not executable: %s", p)
	}

	// 2) PATH lookup.
	if p, err := exec.LookPath("zig"); err == nil {
		return p, nil
	}

	// 3) Common absolute install locations.
	candidates := []string{
		"/usr/local/bin/zig",
		"/usr/bin/zig",
		"/opt/homebrew/bin/zig",
		"/snap/bin/zig",
		filepath.FromSlash("/mnt/c/Program Files/zig/zig.exe"),
		filepath.FromSlash("/mnt/c/zig/zig.exe"),
	}
	for _, c := range candidates {
		if isExecutableFile(c) {
			return c, nil
		}
	}

	// 4) Shell login lookup fallback (helps when PATH differs for non-interactive exec).
	if p := resolveViaShell(); p != "" && isExecutableFile(p) {
		return p, nil
	}

	return "", fmt.Errorf("zig executable not found; set ZIG_BIN or add zig to PATH")
}

func resolveViaShell() string {
	shellCandidates := []struct {
		prog string
		args []string
	}{
		{"bash", []string{"-lc", "command -v zig"}},
		{"zsh", []string{"-lc", "command -v zig"}},
		{"sh", []string{"-lc", "command -v zig"}},
	}

	for _, sh := range shellCandidates {
		out, err := exec.Command(sh.prog, sh.args...).Output()
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(out))
		if p != "" {
			return p
		}
	}
	return ""
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
