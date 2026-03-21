package lsp

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const checkTimeout = 30 * time.Second

func Check(filePath, cwd string) ([]Diagnostic, error) {
	c := detect(filePath, cwd)
	if c == nil {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	args := c.args(filePath, cwd)
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = c.dir(cwd)

	out, _ := cmd.CombinedOutput()

	diags := parseOutput(c.bin, string(out), cwd)
	return diags, nil
}

func Format(diags []Diagnostic) string {
	if len(diags) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, d := range diags {
		if d.Line > 0 && d.Col > 0 {
			fmt.Fprintf(&sb, "%s:%d:%d: %s: %s\n", d.File, d.Line, d.Col, d.Level, d.Message)
		} else if d.Line > 0 {
			fmt.Fprintf(&sb, "%s:%d: %s: %s\n", d.File, d.Line, d.Level, d.Message)
		} else {
			fmt.Fprintf(&sb, "%s: %s: %s\n", d.File, d.Level, d.Message)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func HasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Level == "error" {
			return true
		}
	}
	return false
}
