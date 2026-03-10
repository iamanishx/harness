package filesystem

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/iamanishx/go-ai/provider"
)

// ToolFactory builds provider.Tool wrappers backed by a Zig MCP server.
type ToolFactory struct {
	client *Client
}

// NewToolFactory creates a new wrapper factory from an initialized client.
func NewToolFactory(client *Client) *ToolFactory {
	return &ToolFactory{client: client}
}

// Tools returns all filesystem tools ready to register with ToolLoopAgentSettings.
func (f *ToolFactory) Tools() []provider.Tool {
	return []provider.Tool{
		f.ReadFileTool(),
		f.WriteFileTool(),
		f.EditFileTool(),
		f.GrepTool(),
		f.GlobTool(),
		f.ListDirTool(),
	}
}

// ReadFileTool reads a file, optionally with offset/limit pagination.
func (f *ToolFactory) ReadFileTool() provider.Tool {
	return provider.Tool{
		Name:        "read_file",
		Description: "Read a file with optional offset and limit. Useful for large files.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to file relative to project root",
				},
				"offset": map[string]interface{}{
					"type":        "integer",
					"description": "1-indexed starting line (default 1)",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of lines to return (default 2000)",
				},
			},
			"required": []string{"path"},
		},
		Execute: func(input map[string]interface{}) (string, error) {
			if f.client == nil {
				return "", fmt.Errorf("filesystem client is not initialized")
			}

			path, err := getString(input, "path")
			if err != nil {
				return "", err
			}

			params := map[string]interface{}{
				"path": path,
			}

			if offset, ok, err := getOptionalInt(input, "offset"); err != nil {
				return "", err
			} else if ok {
				params["offset"] = offset
			}

			if limit, ok, err := getOptionalInt(input, "limit"); err != nil {
				return "", err
			} else if ok {
				params["limit"] = limit
			}

			var result ToolResult
			if err := f.client.Call("read_file", params, &result); err != nil {
				return "", err
			}
			return stringifyResult(result)
		},
	}
}

// WriteFileTool writes content to a file (create or overwrite).
func (f *ToolFactory) WriteFileTool() provider.Tool {
	return provider.Tool{
		Name:        "write_file",
		Description: "Create or overwrite a file with full content.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to file relative to project root",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Full content to write",
				},
			},
			"required": []string{"path", "content"},
		},
		Execute: func(input map[string]interface{}) (string, error) {
			if f.client == nil {
				return "", fmt.Errorf("filesystem client is not initialized")
			}

			path, err := getString(input, "path")
			if err != nil {
				return "", err
			}
			content, err := getString(input, "content")
			if err != nil {
				return "", err
			}

			params := map[string]interface{}{
				"path":    path,
				"content": content,
			}

			var result ToolResult
			if err := f.client.Call("write_file", params, &result); err != nil {
				return "", err
			}
			return stringifyResult(result)
		},
	}
}

// EditFileTool performs oldString->newString replacement in an existing file.
func (f *ToolFactory) EditFileTool() provider.Tool {
	return provider.Tool{
		Name:        "edit_file",
		Description: "Edit file content by replacing old_string with new_string. Can replace all occurrences.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to file relative to project root",
				},
				"old_string": map[string]interface{}{
					"type":        "string",
					"description": "Exact text to find",
				},
				"new_string": map[string]interface{}{
					"type":        "string",
					"description": "Replacement text",
				},
				"replace_all": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether to replace all occurrences (default false)",
				},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
		Execute: func(input map[string]interface{}) (string, error) {
			if f.client == nil {
				return "", fmt.Errorf("filesystem client is not initialized")
			}

			path, err := getString(input, "path")
			if err != nil {
				return "", err
			}
			oldString, err := getString(input, "old_string")
			if err != nil {
				return "", err
			}
			newString, err := getString(input, "new_string")
			if err != nil {
				return "", err
			}

			params := map[string]interface{}{
				"path":       path,
				"old_string": oldString,
				"new_string": newString,
			}

			if replaceAll, ok, err := getOptionalBool(input, "replace_all"); err != nil {
				return "", err
			} else if ok {
				params["replace_all"] = replaceAll
			}

			var result ToolResult
			if err := f.client.Call("edit_file", params, &result); err != nil {
				return "", err
			}
			return stringifyResult(result)
		},
	}
}

// GrepTool searches file contents using a regex pattern.
func (f *ToolFactory) GrepTool() provider.Tool {
	return provider.Tool{
		Name:        "grep",
		Description: "Search file contents with a regex pattern.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "Regex pattern to search for",
				},
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Directory path to search in (default: project root)",
				},
				"include": map[string]interface{}{
					"type":        "string",
					"description": "Optional glob filter, e.g. **/*.go",
				},
				"case_sensitive": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether matching is case-sensitive",
				},
			},
			"required": []string{"pattern"},
		},
		Execute: func(input map[string]interface{}) (string, error) {
			if f.client == nil {
				return "", fmt.Errorf("filesystem client is not initialized")
			}

			pattern, err := getString(input, "pattern")
			if err != nil {
				return "", err
			}

			params := map[string]interface{}{
				"pattern": pattern,
			}

			if path, ok, err := getOptionalString(input, "path"); err != nil {
				return "", err
			} else if ok {
				params["path"] = path
			}

			if include, ok, err := getOptionalString(input, "include"); err != nil {
				return "", err
			} else if ok {
				params["include"] = include
			}

			if cs, ok, err := getOptionalBool(input, "case_sensitive"); err != nil {
				return "", err
			} else if ok {
				params["case_sensitive"] = cs
			}

			var result ToolResult
			if err := f.client.Call("grep", params, &result); err != nil {
				return "", err
			}
			return stringifyResult(result)
		},
	}
}

// GlobTool finds files by glob pattern.
func (f *ToolFactory) GlobTool() provider.Tool {
	return provider.Tool{
		Name:        "glob",
		Description: "Find files and directories by glob pattern.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "Glob pattern, e.g. **/*.go",
				},
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Directory path to search in (default: project root)",
				},
			},
			"required": []string{"pattern"},
		},
		Execute: func(input map[string]interface{}) (string, error) {
			if f.client == nil {
				return "", fmt.Errorf("filesystem client is not initialized")
			}

			pattern, err := getString(input, "pattern")
			if err != nil {
				return "", err
			}

			params := map[string]interface{}{
				"pattern": pattern,
			}

			if path, ok, err := getOptionalString(input, "path"); err != nil {
				return "", err
			} else if ok {
				params["path"] = path
			}

			var result ToolResult
			if err := f.client.Call("glob", params, &result); err != nil {
				return "", err
			}
			return stringifyResult(result)
		},
	}
}

// ListDirTool lists entries in a directory.
func (f *ToolFactory) ListDirTool() provider.Tool {
	return provider.Tool{
		Name:        "list_dir",
		Description: "List files and directories in a path.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Directory path relative to project root",
				},
			},
			"required": []string{"path"},
		},
		Execute: func(input map[string]interface{}) (string, error) {
			if f.client == nil {
				return "", fmt.Errorf("filesystem client is not initialized")
			}

			path, err := getString(input, "path")
			if err != nil {
				return "", err
			}

			params := map[string]interface{}{
				"path": path,
			}

			var result ToolResult
			if err := f.client.Call("list_dir", params, &result); err != nil {
				return "", err
			}
			return stringifyResult(result)
		},
	}
}

func stringifyResult(result ToolResult) (string, error) {
	if result.Output != "" {
		return result.Output, nil
	}

	blob, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool result: %w", err)
	}
	return string(blob), nil
}

func getString(input map[string]interface{}, key string) (string, error) {
	raw, ok := input[key]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", key)
	}
	v, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string", key)
	}
	return v, nil
}

func getOptionalString(input map[string]interface{}, key string) (string, bool, error) {
	raw, ok := input[key]
	if !ok {
		return "", false, nil
	}
	v, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("parameter %s must be a string", key)
	}
	return v, true, nil
}

func getOptionalInt(input map[string]interface{}, key string) (int, bool, error) {
	raw, ok := input[key]
	if !ok {
		return 0, false, nil
	}

	switch n := raw.(type) {
	case int:
		return n, true, nil
	case int32:
		return int(n), true, nil
	case int64:
		return int(n), true, nil
	case float32:
		return int(n), true, nil
	case float64:
		return int(n), true, nil
	case json.Number:
		i, err := strconv.Atoi(n.String())
		if err != nil {
			return 0, false, fmt.Errorf("parameter %s must be an integer", key)
		}
		return i, true, nil
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, false, fmt.Errorf("parameter %s must be an integer", key)
		}
		i, err := strconv.Atoi(s)
		if err != nil {
			return 0, false, fmt.Errorf("parameter %s must be an integer", key)
		}
		return i, true, nil
	default:
		return 0, false, fmt.Errorf("parameter %s must be an integer", key)
	}
}

func getOptionalBool(input map[string]interface{}, key string) (bool, bool, error) {
	raw, ok := input[key]
	if !ok {
		return false, false, nil
	}
	v, ok := raw.(bool)
	if !ok {
		return false, false, fmt.Errorf("parameter %s must be a boolean", key)
	}
	return v, true, nil
}
