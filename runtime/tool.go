package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"goai-test/tools/filesystem"
	"goai-test/types"
)

type ToolRunner struct {
	client *filesystem.Client
	store  *MessageStore
	bus    *EventBus
	perms  *PermissionService
	mu     sync.Mutex
	hashes map[string]string
}

func NewToolRunner(client *filesystem.Client, store *MessageStore, bus *EventBus, perms *PermissionService) *ToolRunner {
	return &ToolRunner{
		client: client,
		store:  store,
		bus:    bus,
		perms:  perms,
		hashes: make(map[string]string),
	}
}

func (tr *ToolRunner) Execute(ctx context.Context, sessionID, messageID, cwd, toolName string, input map[string]any) (types.Part, error) {
	callID := uuid.New().String()
	tool := types.ToolPart{
		CallID:    callID,
		Tool:      toolName,
		State:     types.ToolStatePending,
		Input:     input,
		TimeStart: time.Now().UnixMilli(),
	}

	part, err := tr.store.AddToolPart(messageID, sessionID, tool)
	if err != nil {
		return types.Part{}, fmt.Errorf("create tool part: %w", err)
	}

	tool.State = types.ToolStateRunning
	_ = tr.store.UpdateToolPart(part.ID, tool)

	output, execErr := tr.dispatch(ctx, cwd, toolName, input)

	tool.TimeEnd = time.Now().UnixMilli()
	if execErr != nil {
		tool.State = types.ToolStateError
		tool.Error = execErr.Error()
	} else {
		tool.State = types.ToolStateCompleted
		tool.Output = output
	}

	_ = tr.store.UpdateToolPart(part.ID, tool)

	if isWriteOp(toolName) {
		if path, ok := inputPath(input); ok {
			tr.bus.Publish(Event{
				Type:      EventFileChanged,
				SessionID: sessionID,
				Data:      map[string]string{"path": path, "op": toolName},
			})
		}
	}

	return part, execErr
}

func (tr *ToolRunner) dispatch(ctx context.Context, cwd, toolName string, input map[string]any) (string, error) {
	if path, ok := inputPath(input); ok {
		if err := tr.checkScope(cwd, path); err != nil {
			return "", err
		}
	}

	if isWriteOp(toolName) {
		if path, ok := inputPath(input); ok {
			if err := tr.checkStale(path); err != nil {
				return "", err
			}
		}
	}

	var result filesystem.ToolResult
	if err := tr.client.Call(toolName, input, &result); err != nil {
		return "", err
	}

	if toolName == "read_file" {
		if path, ok := inputPath(input); ok {
			tr.recordHash(path, result.Output)
		}
	}

	if result.Output != "" {
		return result.Output, nil
	}
	return "{}", nil
}

func (tr *ToolRunner) checkScope(cwd, path string) error {
	if filepath.IsAbs(path) {
		clean := filepath.Clean(path)
		cwdClean := filepath.Clean(cwd)
		if !strings.HasPrefix(clean, cwdClean+string(filepath.Separator)) && clean != cwdClean {
			return fmt.Errorf("path %q is outside working directory %q", path, cwd)
		}
	}
	return nil
}

func (tr *ToolRunner) checkStale(path string) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	_, exists := tr.hashes[path]
	if !exists {
		return nil
	}
	return nil
}

func (tr *ToolRunner) recordHash(path, content string) {
	h := sha256.Sum256([]byte(content))
	tr.mu.Lock()
	tr.hashes[path] = fmt.Sprintf("%x", h)
	tr.mu.Unlock()
}

func isWriteOp(toolName string) bool {
	return toolName == "write_file" || toolName == "edit_file"
}

func inputPath(input map[string]any) (string, bool) {
	v, ok := input["path"]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
