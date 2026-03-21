package shell

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/iamanishx/go-ai/provider"
)

const timeout = 30 * time.Second

func Tool(cwd string) provider.Tool {
	return provider.Tool{
		Name:        "run_command",
		Description: "Run a shell command in the project working directory. Use for build commands, tests, package installs, git operations, and anything that requires a terminal. Avoid interactive commands.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The command to run",
				},
			},
			"required": []string{"command"},
		},
		Execute: func(input map[string]any) (string, error) {
			raw, ok := input["command"]
			if !ok {
				return "", fmt.Errorf("missing required parameter: command")
			}
			cmd, ok := raw.(string)
			if !ok || strings.TrimSpace(cmd) == "" {
				return "", fmt.Errorf("command must be a non-empty string")
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			var c *exec.Cmd
			if runtime.GOOS == "windows" {
				c = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", cmd)
			} else {
				c = exec.CommandContext(ctx, "sh", "-c", cmd)
			}
			c.Dir = cwd

			var out bytes.Buffer
			c.Stdout = &out
			c.Stderr = &out

			err := c.Run()
			output := strings.TrimRight(out.String(), "\n")

			if ctx.Err() == context.DeadlineExceeded {
				return output, fmt.Errorf("command timed out after %s", timeout)
			}
			if err != nil {
				if output != "" {
					return output, nil
				}
				return "", err
			}
			if output == "" {
				return "ok", nil
			}
			return output, nil
		},
	}
}
