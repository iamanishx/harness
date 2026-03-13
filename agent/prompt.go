package agent

import (
	_ "embed"
	"strings"
)

//go:embed prompt.txt
var systemPromptTemplate string

func systemPrompt(cwd string) string {
	return strings.ReplaceAll(systemPromptTemplate, "{{.CWD}}", cwd)
}
