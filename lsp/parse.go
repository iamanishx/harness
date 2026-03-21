package lsp

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Diagnostic struct {
	File    string
	Line    int
	Col     int
	Level   string
	Message string
}

// file:line:col: message  (gopls, eslint unix, ruff, flake8, pyright, tsc)
var reFileLineCol = regexp.MustCompile(`^([^:]+):(\d+):(\d+):\s*(?:(error|warning|note|hint|info)[:\s]+)?(.+)$`)

// file:line: message  (go build, some cargo short)
var reFileLine = regexp.MustCompile(`^([^:]+):(\d+):\s*(.+)$`)

// cargo short: file:line:col  error[E...]: message
var reCargo = regexp.MustCompile(`^([^:]+):(\d+):(\d+)\s+(error|warning)(?:\[[\w\d]+\])?:\s*(.+)$`)

// zig: file:line:col: error: message
var reZig = regexp.MustCompile(`^([^:]+):(\d+):(\d+):\s*(error|note|warning):\s*(.+)$`)

func parseOutput(bin, output, cwd string) []Diagnostic {
	var diags []Diagnostic
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var d *Diagnostic
		switch bin {
		case "cargo":
			d = matchCargo(line, cwd)
		case "zig":
			d = matchZig(line, cwd)
		default:
			d = matchGeneric(line, cwd)
		}
		if d != nil {
			diags = append(diags, *d)
		}
	}
	return diags
}

func matchCargo(line, cwd string) *Diagnostic {
	if m := reCargo.FindStringSubmatch(line); m != nil {
		return &Diagnostic{
			File:    absPath(m[1], cwd),
			Line:    atoi(m[2]),
			Col:     atoi(m[3]),
			Level:   m[4],
			Message: m[5],
		}
	}
	return matchGeneric(line, cwd)
}

func matchZig(line, cwd string) *Diagnostic {
	if m := reZig.FindStringSubmatch(line); m != nil {
		return &Diagnostic{
			File:    absPath(m[1], cwd),
			Line:    atoi(m[2]),
			Col:     atoi(m[3]),
			Level:   m[4],
			Message: m[5],
		}
	}
	return nil
}

func matchGeneric(line, cwd string) *Diagnostic {
	if m := reFileLineCol.FindStringSubmatch(line); m != nil {
		level := strings.ToLower(m[4])
		if level == "" {
			level = inferLevel(line)
		}
		return &Diagnostic{
			File:    absPath(m[1], cwd),
			Line:    atoi(m[2]),
			Col:     atoi(m[3]),
			Level:   level,
			Message: m[5],
		}
	}
	if m := reFileLine.FindStringSubmatch(line); m != nil {
		return &Diagnostic{
			File:    absPath(m[1], cwd),
			Line:    atoi(m[2]),
			Level:   inferLevel(line),
			Message: m[3],
		}
	}
	return nil
}

func inferLevel(line string) string {
	l := strings.ToLower(line)
	if strings.Contains(l, "error") {
		return "error"
	}
	if strings.Contains(l, "warning") {
		return "warning"
	}
	return "info"
}

func absPath(p, cwd string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(cwd, p)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
