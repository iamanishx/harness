package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/iamanishx/go-ai/agent"
	"github.com/iamanishx/go-ai/provider"
	"github.com/iamanishx/go-ai/provider/bedrock"
	"goai-test/tools/filesystem"
	"goai-test/types"
)

type AgentLoop struct {
	provider      provider.ChatModel
	zigBinaryPath string
	store         *MessageStore
	bus           *EventBus
}

func (al *AgentLoop) Bus() *EventBus { return al.bus }

type AgentLoopConfig struct {
	Region        string
	Profile       string
	ModelID       string
	ZigBinaryPath string
	Store         *MessageStore
	Bus           *EventBus
}

func NewAgentLoop(cfg AgentLoopConfig) *AgentLoop {
	p := bedrock.Create(bedrock.BedrockProviderSettings{
		Region:  cfg.Region,
		Profile: cfg.Profile,
	})
	return &AgentLoop{
		provider:      p.Chat(cfg.ModelID),
		zigBinaryPath: cfg.ZigBinaryPath,
		store:         cfg.Store,
		bus:           cfg.Bus,
	}
}

func (al *AgentLoop) Run(ctx context.Context, sessionID, cwd, prompt string) error {
	fsClient, err := filesystem.NewClientInDir(al.zigBinaryPath, cwd)
	if err != nil {
		return fmt.Errorf("start filesystem client for %s: %w", cwd, err)
	}
	defer fsClient.Close()

	history, err := al.buildHistory(sessionID)
	if err != nil {
		return fmt.Errorf("build history: %w", err)
	}

	log.Printf("[agent] session=%s history_len=%d", sessionID, len(history))

	_, err = al.store.CreateUserMessage(sessionID, prompt)
	if err != nil {
		return fmt.Errorf("create user message: %w", err)
	}

	assistantMsg, err := al.store.CreateAssistantMessage(sessionID)
	if err != nil {
		return fmt.Errorf("create assistant message: %w", err)
	}

	// pendingCallIDs maps toolName -> LLM callID, set by OnToolCallStart just before Execute.
	var pendingMu sync.Mutex
	pendingCallIDs := make(map[string]string)

	toolFactory := filesystem.NewToolFactory(fsClient)
	tools := al.instrumentedTools(toolFactory.Tools(), sessionID, assistantMsg.ID, cwd, &pendingMu, pendingCallIDs)

	ag := agentpkg.CreateToolLoopAgent(agentpkg.ToolLoopAgentSettings{
		Model:        al.provider,
		Tools:        tools,
		ExecuteTools: true,
		MaxSteps:     20,
		OnToolCallStart: func(e agentpkg.OnToolCallStartEvent) {
			pendingMu.Lock()
			pendingCallIDs[e.ToolName] = e.ToolCallID
			pendingMu.Unlock()
			log.Printf("[agent] tool-start session=%s tool=%s call_id=%s", sessionID, e.ToolName, e.ToolCallID)
		},
	})

	s, err := ag.Stream(ctx, agentpkg.AgentCallOptions{
		Prompt:   prompt,
		Messages: history,
	})
	if err != nil {
		return fmt.Errorf("agent stream: %w", err)
	}
	defer s.Close()

	var textBuf strings.Builder

	for part := range s.Part() {
		switch part.Type {
		case "text-delta":
			textBuf.WriteString(part.Text)
			al.bus.Publish(Event{
				Type:      EventPartDelta,
				SessionID: sessionID,
				Data:      map[string]string{"text": part.Text, "message_id": assistantMsg.ID},
			})

		case "text-end":
			if textBuf.Len() > 0 {
				_, _ = al.store.AddTextPart(assistantMsg.ID, sessionID, textBuf.String())
				textBuf.Reset()
			}

		case "error":
			return fmt.Errorf("agent error: %w", part.Error)
		}
	}

	if textBuf.Len() > 0 {
		_, _ = al.store.AddTextPart(assistantMsg.ID, sessionID, textBuf.String())
	}

	return nil
}

func (al *AgentLoop) buildHistory(sessionID string) ([]provider.Message, error) {
	msgs, err := al.store.GetMessages(sessionID)
	if err != nil {
		return nil, err
	}

	var history []provider.Message

	for _, m := range msgs {
		role := string(m.Role)

		var textParts []string
		var toolCalls []provider.ToolCall

		for _, p := range m.Parts {
			switch p.Type {
			case types.PartTypeText:
				var td types.TextData
				if _, err := UnmarshalPartData(p.Data, &td); err == nil && td.Text != "" {
					textParts = append(textParts, td.Text)
				}
			case types.PartTypeTool:
				var tp types.ToolPart
				if _, err := UnmarshalPartData(p.Data, &tp); err == nil {
					toolCalls = append(toolCalls, provider.ToolCall{
						ID:     tp.CallID,
						Name:   tp.Tool,
						Input:  tp.Input,
						Output: tp.Output,
					})
				}
			}
		}

		if role == "user" {
			content := strings.Join(textParts, "\n")
			if content == "" && len(toolCalls) == 0 {
				continue
			}
			msg := provider.Message{Role: "user", Content: content}
			if len(toolCalls) > 0 {
				msg.ToolResults = toolCalls
			}
			history = append(history, msg)
			continue
		}

		if len(toolCalls) > 0 {
			assistantMsg := provider.Message{
				Role:      "assistant",
				Content:   strings.Join(textParts, "\n"),
				ToolCalls: toolCalls,
			}
			history = append(history, assistantMsg)

			var results []provider.ToolCall
			for _, tc := range toolCalls {
				results = append(results, provider.ToolCall{
					ID:     tc.ID,
					Name:   tc.Name,
					Output: tc.Output,
				})
			}
			history = append(history, provider.Message{
				Role:        "user",
				ToolResults: results,
			})
		} else if len(textParts) > 0 {
			history = append(history, provider.Message{
				Role:    "assistant",
				Content: strings.Join(textParts, "\n"),
			})
		}
	}

	return history, nil
}

func (al *AgentLoop) instrumentedTools(tools []provider.Tool, sessionID, messageID, cwd string, pendingMu *sync.Mutex, pendingCallIDs map[string]string) []provider.Tool {
	out := make([]provider.Tool, len(tools))
	for i, t := range tools {
		t := t
		originalExecute := t.Execute
		t.Execute = func(input map[string]any) (string, error) {
			pendingMu.Lock()
			callID := pendingCallIDs[t.Name]
			delete(pendingCallIDs, t.Name)
			pendingMu.Unlock()

			var oldContent, filePath string
			if isWriteOp(t.Name) {
				filePath = resolveToolPath(t.Name, input, cwd)
				if filePath != "" {
					if data, err := os.ReadFile(filePath); err == nil {
						oldContent = string(data)
						log.Printf("[diff] captured old content tool=%s path=%s bytes=%d", t.Name, filePath, len(oldContent))
					} else {
						log.Printf("[diff] old file not found (new file) tool=%s path=%s", t.Name, filePath)
					}
				}
			}

			toolPart := types.ToolPart{
				CallID:    callID,
				Tool:      t.Name,
				State:     types.ToolStatePending,
				Input:     input,
				TimeStart: time.Now().UnixMilli(),
				FilePath:  filePath,
			}
			p, _ := al.store.AddToolPart(messageID, sessionID, toolPart)

			toolPart.State = types.ToolStateRunning
			_ = al.store.UpdateToolPart(p.ID, sessionID, toolPart)

			result, err := originalExecute(input)

			toolPart.TimeEnd = time.Now().UnixMilli()
			if err != nil {
				toolPart.State = types.ToolStateError
				toolPart.Error = err.Error()
				log.Printf("[agent] tool error session=%s tool=%s call_id=%s: %v", sessionID, t.Name, callID, err)
			} else {
				toolPart.State = types.ToolStateCompleted
				toolPart.Output = result
				if filePath != "" {
					toolPart.OldContent = oldContent
					if data, readErr := os.ReadFile(filePath); readErr == nil {
						toolPart.NewContent = string(data)
						log.Printf("[diff] captured new content tool=%s path=%s old_bytes=%d new_bytes=%d", t.Name, filePath, len(oldContent), len(toolPart.NewContent))
					} else {
						log.Printf("[diff] failed to read new content tool=%s path=%s: %v", t.Name, filePath, readErr)
					}
				}
			}
			_ = al.store.UpdateToolPart(p.ID, sessionID, toolPart)

			if err == nil && isWriteOp(t.Name) && filePath != "" {
				al.bus.Publish(Event{
					Type:      EventFileChanged,
					SessionID: sessionID,
					Data:      map[string]string{"path": filePath, "op": t.Name, "cwd": cwd},
				})
			}

			return result, err
		}
		out[i] = t
	}
	return out
}

func resolveToolPath(toolName string, input map[string]any, cwd string) string {
	key := "path"
	if toolName == "write_file" || toolName == "edit_file" {
		key = "path"
	}
	raw, ok := input[key]
	if !ok {
		return ""
	}
	p, ok := raw.(string)
	if !ok || p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(cwd, p)
}
