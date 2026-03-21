package lsp

import (
	"os/exec"
	"path/filepath"
)

type checker struct {
	bin  string
	args func(file, cwd string) []string
	dir  func(cwd string) string
}

var extCheckers = map[string][]checker{
	".go": {
		{
			bin:  "gopls",
			args: func(file, cwd string) []string { return []string{"check", file} },
			dir:  func(cwd string) string { return cwd },
		},
		{
			bin:  "go",
			args: func(file, cwd string) []string { return []string{"build", "./..."} },
			dir:  func(cwd string) string { return cwd },
		},
	},
	".ts": {
		{
			bin:  "deno",
			args: func(file, cwd string) []string { return []string{"check", file} },
			dir:  func(cwd string) string { return cwd },
		},
		{
			bin:  "tsc",
			args: func(file, cwd string) []string { return []string{"--noEmit", "--pretty", "false"} },
			dir:  func(cwd string) string { return cwd },
		},
		{
			bin:  "npx",
			args: func(file, cwd string) []string { return []string{"tsc", "--noEmit", "--pretty", "false"} },
			dir:  func(cwd string) string { return cwd },
		},
	},
	".tsx": {
		{
			bin:  "deno",
			args: func(file, cwd string) []string { return []string{"check", file} },
			dir:  func(cwd string) string { return cwd },
		},
		{
			bin:  "tsc",
			args: func(file, cwd string) []string { return []string{"--noEmit", "--pretty", "false"} },
			dir:  func(cwd string) string { return cwd },
		},
	},
	".js": {
		{
			bin:  "deno",
			args: func(file, cwd string) []string { return []string{"check", file} },
			dir:  func(cwd string) string { return cwd },
		},
		{
			bin:  "eslint",
			args: func(file, cwd string) []string { return []string{"--format", "unix", file} },
			dir:  func(cwd string) string { return cwd },
		},
	},
	".jsx": {
		{
			bin:  "deno",
			args: func(file, cwd string) []string { return []string{"check", file} },
			dir:  func(cwd string) string { return cwd },
		},
		{
			bin:  "eslint",
			args: func(file, cwd string) []string { return []string{"--format", "unix", file} },
			dir:  func(cwd string) string { return cwd },
		},
	},
	".py": {
		{
			bin:  "pyright",
			args: func(file, cwd string) []string { return []string{file} },
			dir:  func(cwd string) string { return cwd },
		},
		{
			bin:  "ruff",
			args: func(file, cwd string) []string { return []string{"check", file} },
			dir:  func(cwd string) string { return cwd },
		},
		{
			bin:  "flake8",
			args: func(file, cwd string) []string { return []string{file} },
			dir:  func(cwd string) string { return cwd },
		},
	},
	".rs": {
		{
			bin:  "cargo",
			args: func(file, cwd string) []string { return []string{"check", "--message-format", "short"} },
			dir:  func(cwd string) string { return cwd },
		},
	},
	".zig": {
		{
			bin:  "zig",
			args: func(file, cwd string) []string { return []string{"build-exe", file, "--dry-run"} },
			dir:  func(cwd string) string { return cwd },
		},
		{
			bin:  "zig",
			args: func(file, cwd string) []string { return []string{"build", "check"} },
			dir:  func(cwd string) string { return cwd },
		},
	},
}

func detect(filePath, cwd string) *checker {
	ext := filepath.Ext(filePath)
	checkers, ok := extCheckers[ext]
	if !ok {
		return nil
	}
	for i := range checkers {
		c := &checkers[i]
		if _, err := exec.LookPath(c.bin); err == nil {
			return c
		}
	}
	return nil
}
