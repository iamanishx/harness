package acp

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"goai-test/agent"
	"goai-test/runtime"
	"goai-test/storage"
	"goai-test/types"
)

type Server struct {
	conn         *acp.AgentSideConnection
	sessions     *runtime.SessionManager
	messages     *runtime.MessageStore
	agent        *agent.Loop
	db           *storage.DB
	canWriteFile bool
}

func NewServer(
	sessions *runtime.SessionManager,
	messages *runtime.MessageStore,
	ag *agent.Loop,
	db *storage.DB,
) *Server {
	return &Server{
		sessions: sessions,
		messages: messages,
		agent:    ag,
		db:       db,
	}
}

func (s *Server) SetConnection(conn *acp.AgentSideConnection) {
	s.conn = conn
}

func (s *Server) Done() <-chan struct{} {
	return s.conn.Done()
}

func (s *Server) Authenticate(_ context.Context, _ acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (s *Server) Initialize(_ context.Context, req acp.InitializeRequest) (acp.InitializeResponse, error) {
	s.canWriteFile = req.ClientCapabilities.Fs.WriteTextFile
	log.Printf("[acp] initialize canWriteFile=%v", s.canWriteFile)
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: acp.PromptCapabilities{
				EmbeddedContext: true,
			},
		},
	}, nil
}

func (s *Server) Cancel(_ context.Context, p acp.CancelNotification) error {
	s.sessions.Abort(string(p.SessionId))
	return nil
}

func (s *Server) SetSessionMode(_ context.Context, _ acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func (s *Server) NewSession(_ context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	cwd := p.Cwd
	if cwd == "" {
		cwd = "."
	}
	sess, err := s.sessions.Create(cwd)
	if err != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("create session: %w", err)
	}
	log.Printf("[acp] new session=%s cwd=%s", sess.ID, cwd)
	return acp.NewSessionResponse{
		SessionId: acp.SessionId(sess.ID),
	}, nil
}

func (s *Server) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	sessionID := string(p.SessionId)

	sess, ok := s.sessions.Get(sessionID)
	if !ok {
		loaded, err := s.sessions.Load(sessionID)
		if err != nil {
			return acp.PromptResponse{}, fmt.Errorf("session not found: %s", sessionID)
		}
		sess = loaded
	}

	text := promptText(p.Prompt)
	log.Printf("[acp] prompt session=%s text=%q", sessionID, truncate(text, 80))

	if sess.Title == "" {
		title := text
		if len(title) > 60 {
			title = title[:60] + "..."
		}
		_ = s.sessions.UpdateTitle(sessionID, title)
	}

	unsub := s.agent.Bus().Subscribe("*", func(e runtime.Event) {
		if e.SessionID != sessionID {
			return
		}
		switch e.Type {
		case runtime.EventPartDelta:
			if d, ok := e.Data.(map[string]string); ok {
				_ = s.conn.SessionUpdate(ctx, acp.SessionNotification{
					SessionId: p.SessionId,
					Update:    acp.UpdateAgentMessageText(d["text"]),
				})
			}

		case runtime.EventPartCreated:
			if part, ok := e.Data.(types.Part); ok && part.Type == types.PartTypeTool {
				var tp types.ToolPart
				if _, err := runtime.UnmarshalPartData(part.Data, &tp); err == nil {
					_ = s.conn.SessionUpdate(ctx, acp.SessionNotification{
						SessionId: p.SessionId,
						Update: acp.StartToolCall(
							acp.ToolCallId(tp.CallID),
							tp.Tool,
							acp.WithStartKind(toolKind(tp.Tool)),
							acp.WithStartStatus(acp.ToolCallStatusPending),
							acp.WithStartRawInput(tp.Input),
						),
					})
				}
			}

		case runtime.EventPartUpdated:
			if d, ok := e.Data.(map[string]any); ok {
				if tp, ok2 := d["tool"].(types.ToolPart); ok2 {
					opts := []acp.ToolCallUpdateOpt{
						acp.WithUpdateStatus(toolCallStatus(tp.State)),
						acp.WithUpdateRawOutput(tp.Output),
					}
					if tp.State == types.ToolStateCompleted && tp.FilePath != "" && tp.NewContent != "" {
						log.Printf("[diff] sending diff to Zed session=%s tool=%s path=%s old_bytes=%d new_bytes=%d",
							sessionID, tp.Tool, tp.FilePath, len(tp.OldContent), len(tp.NewContent))
						opts = append(opts, acp.WithUpdateContent([]acp.ToolCallContent{
							acp.ToolContent(acp.ContentBlock{Text: &acp.ContentBlockText{Text: tp.Output}}),
							acp.ToolDiffContent(tp.FilePath, tp.NewContent, tp.OldContent),
						}))
						if s.canWriteFile {
							log.Printf("[diff] calling WriteTextFile session=%s path=%s", sessionID, tp.FilePath)
							_, writeErr := s.conn.WriteTextFile(ctx, acp.WriteTextFileRequest{
								SessionId: p.SessionId,
								Path:      tp.FilePath,
								Content:   tp.NewContent,
							})
							if writeErr != nil {
								log.Printf("[diff] WriteTextFile error: %v", writeErr)
							}
						} else {
							log.Printf("[diff] skipping WriteTextFile (client capability not set)")
						}
					}
					_ = s.conn.SessionUpdate(ctx, acp.SessionNotification{
						SessionId: p.SessionId,
						Update:    acp.UpdateToolCall(acp.ToolCallId(tp.CallID), opts...),
					})
				}
			}
		}
	})
	defer unsub()

	rctx := s.sessions.AcquireContext(sessionID)
	runCtx, cancel := context.WithCancel(rctx)
	defer cancel()

	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()

	err := s.agent.Run(runCtx, sessionID, sess.CWD, text)
	if err != nil && runCtx.Err() == nil {
		return acp.PromptResponse{}, err
	}

	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (s *Server) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	sessionID := string(p.SessionId)

	sess, err := s.sessions.Load(sessionID)
	if err != nil {
		log.Printf("[acp] load session not found: %s", sessionID)
		return acp.LoadSessionResponse{}, nil
	}

	entries, err := runtime.Replay(s.db, sessionID)
	if err != nil {
		return acp.LoadSessionResponse{}, nil
	}

	sid := acp.SessionId(sess.ID)
	currentRole := types.RoleAssistant

	for _, entry := range entries {
		if entry.Message != nil {
			currentRole = entry.Message.Role
			continue
		}
		if entry.Part == nil {
			continue
		}
		part := entry.Part
		switch part.Type {
		case types.PartTypeText:
			var td types.TextData
			if _, err := runtime.UnmarshalPartData(part.Data, &td); err == nil && td.Text != "" {
				var update acp.SessionUpdate
				if currentRole == types.RoleUser {
					update = acp.UpdateUserMessageText(td.Text)
				} else {
					update = acp.UpdateAgentMessageText(td.Text)
				}
				_ = s.conn.SessionUpdate(ctx, acp.SessionNotification{
					SessionId: sid,
					Update:    update,
				})
			}
		case types.PartTypeTool:
			var tp types.ToolPart
			if _, err := runtime.UnmarshalPartData(part.Data, &tp); err == nil {
				startOpts := []acp.ToolCallStartOpt{
					acp.WithStartKind(toolKind(tp.Tool)),
					acp.WithStartStatus(toolCallStatus(tp.State)),
					acp.WithStartRawInput(tp.Input),
					acp.WithStartRawOutput(tp.Output),
				}
				if tp.State == types.ToolStateCompleted && tp.FilePath != "" && tp.NewContent != "" {
					log.Printf("[diff] replaying diff session=%s tool=%s path=%s", sessionID, tp.Tool, tp.FilePath)
					startOpts = append(startOpts, acp.WithStartContent([]acp.ToolCallContent{
						acp.ToolContent(acp.ContentBlock{Text: &acp.ContentBlockText{Text: tp.Output}}),
						acp.ToolDiffContent(tp.FilePath, tp.NewContent, tp.OldContent),
					}))
				}
				_ = s.conn.SessionUpdate(ctx, acp.SessionNotification{
					SessionId: sid,
					Update:    acp.StartToolCall(acp.ToolCallId(tp.CallID), tp.Tool, startOpts...),
				})
			}
		}
	}

	return acp.LoadSessionResponse{}, nil
}

func promptText(blocks []acp.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			sb.WriteString(b.Text.Text)

		case b.ResourceLink != nil:
			uri := b.ResourceLink.Uri
			name := b.ResourceLink.Name
			if content, path, err := readFileFromURI(uri); err == nil {
				sb.WriteString("\n[file: ")
				sb.WriteString(path)
				sb.WriteString("]\n")
				sb.WriteString(content)
				sb.WriteString("\n")
			} else {
				log.Printf("[acp] resource_link read error name=%s: %v", name, err)
				sb.WriteString("\n[file: ")
				sb.WriteString(uri)
				sb.WriteString("]\n")
			}

		case b.Resource != nil:
			res := b.Resource.Resource
			if res.TextResourceContents != nil {
				path := uriToPath(res.TextResourceContents.Uri)
				sb.WriteString("\n[file: ")
				sb.WriteString(path)
				sb.WriteString("]\n")
				sb.WriteString(res.TextResourceContents.Text)
				sb.WriteString("\n")
			} else if res.BlobResourceContents != nil {
				sb.WriteString("\n[file: ")
				sb.WriteString(res.BlobResourceContents.Uri)
				sb.WriteString("]\n")
			}
		}
	}
	return sb.String()
}

func readFileFromURI(rawURI string) (content, path string, err error) {
	path = uriToPath(rawURI)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", path, err
	}
	return string(data), path, nil
}

func uriToPath(rawURI string) string {
	u, err := url.Parse(rawURI)
	if err != nil {
		return rawURI
	}
	switch u.Scheme {
	case "file":
		return filepath.FromSlash(u.Path)
	case "zed":
		if p := u.Query().Get("path"); p != "" {
			return p
		}
		return u.Path
	default:
		return rawURI
	}
}

func toolCallStatus(state types.ToolState) acp.ToolCallStatus {
	switch state {
	case types.ToolStatePending:
		return acp.ToolCallStatusPending
	case types.ToolStateRunning:
		return acp.ToolCallStatusInProgress
	case types.ToolStateCompleted:
		return acp.ToolCallStatusCompleted
	case types.ToolStateError:
		return acp.ToolCallStatusFailed
	default:
		return acp.ToolCallStatusPending
	}
}

func toolKind(name string) acp.ToolKind {
	switch name {
	case "read_file":
		return acp.ToolKindRead
	case "write_file", "edit_file":
		return acp.ToolKindEdit
	case "grep", "glob", "list_dir":
		return acp.ToolKindSearch
	case "run_command":
		return acp.ToolKindExecute
	default:
		return acp.ToolKindOther
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
