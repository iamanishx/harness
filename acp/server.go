package acp

import (
	"context"
	"fmt"
	"log"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"goai-test/runtime"
	"goai-test/storage"
	"goai-test/types"
)

type Server struct {
	conn     *acp.AgentSideConnection
	sessions *runtime.SessionManager
	messages *runtime.MessageStore
	agent    *runtime.AgentLoop
	db       *storage.DB
}

func NewServer(
	sessions *runtime.SessionManager,
	messages *runtime.MessageStore,
	agent *runtime.AgentLoop,
	db *storage.DB,
) *Server {
	return &Server{
		sessions: sessions,
		messages: messages,
		agent:    agent,
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

func (s *Server) Initialize(_ context.Context, _ acp.InitializeRequest) (acp.InitializeResponse, error) {
	log.Printf("[acp] Initialize called")
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: true,
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
	log.Printf("[acp] NewSession called cwd=%s", cwd)
	sess, err := s.sessions.Create(cwd)
	if err != nil {
		return acp.NewSessionResponse{}, fmt.Errorf("create session: %w", err)
	}
	log.Printf("[acp] NewSession created session=%s", sess.ID)
	return acp.NewSessionResponse{
		SessionId: acp.SessionId(sess.ID),
	}, nil
}

func (s *Server) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	sessionID := string(p.SessionId)
	log.Printf("[acp] Prompt called session=%s", sessionID)

	sess, ok := s.sessions.Get(sessionID)
	if !ok {
		loaded, err := s.sessions.Load(sessionID)
		if err != nil {
			return acp.PromptResponse{}, fmt.Errorf("session not found: %s", sessionID)
		}
		sess = loaded
	}

	text := promptText(p.Prompt)
	log.Printf("[acp] Prompt text=%q", text)

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
							acp.WithStartStatus(acp.ToolCallStatusPending),
						),
					})
				}
			}

		case runtime.EventPartUpdated:
			if d, ok := e.Data.(map[string]any); ok {
				if tp, ok2 := d["tool"].(types.ToolPart); ok2 {
					_ = s.conn.SessionUpdate(ctx, acp.SessionNotification{
						SessionId: p.SessionId,
						Update: acp.UpdateToolCall(
							acp.ToolCallId(tp.CallID),
							acp.WithUpdateStatus(toolCallStatus(tp.State)),
							acp.WithUpdateRawOutput(tp.Output),
						),
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
	log.Printf("[acp] LoadSession called session=%s", sessionID)

	sess, err := s.sessions.Load(sessionID)
	if err != nil {
		log.Printf("[acp] LoadSession session not found: %v", err)
		return acp.LoadSessionResponse{}, nil
	}
	log.Printf("[acp] LoadSession found session=%s title=%q cwd=%s", sess.ID, sess.Title, sess.CWD)

	entries, err := runtime.Replay(s.db, sessionID)
	if err != nil {
		log.Printf("[acp] LoadSession replay error: %v", err)
		return acp.LoadSessionResponse{}, nil
	}
	log.Printf("[acp] LoadSession replay entries=%d", len(entries))

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
				_ = s.conn.SessionUpdate(ctx, acp.SessionNotification{
					SessionId: sid,
					Update: acp.StartToolCall(
						acp.ToolCallId(tp.CallID),
						tp.Tool,
						acp.WithStartStatus(toolCallStatus(tp.State)),
						acp.WithStartRawOutput(tp.Output),
					),
				})
			}
		}
	}

	return acp.LoadSessionResponse{}, nil
}

func promptText(blocks []acp.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Text != nil {
			sb.WriteString(b.Text.Text)
		}
	}
	return sb.String()
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
