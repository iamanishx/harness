package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"goai-test/storage"
	"goai-test/types"
)

type cancelEntry struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type SessionManager struct {
	db      *storage.DB
	bus     *EventBus
	mu      sync.RWMutex
	active  map[string]types.Session
	cancels map[string]cancelEntry
}

func NewSessionManager(db *storage.DB, bus *EventBus) *SessionManager {
	return &SessionManager{
		db:      db,
		bus:     bus,
		active:  make(map[string]types.Session),
		cancels: make(map[string]cancelEntry),
	}
}

func (sm *SessionManager) AcquireContext(sessionID string) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	sm.mu.Lock()
	if e, ok := sm.cancels[sessionID]; ok {
		e.cancel()
	}
	sm.cancels[sessionID] = cancelEntry{ctx: ctx, cancel: cancel}
	sm.mu.Unlock()
	return ctx
}

func (sm *SessionManager) Abort(sessionID string) {
	sm.mu.Lock()
	e, ok := sm.cancels[sessionID]
	if ok {
		delete(sm.cancels, sessionID)
	}
	sm.mu.Unlock()
	if ok {
		e.cancel()
	}
}

func (sm *SessionManager) Create(cwd string) (types.Session, error) {
	now := time.Now().UnixMilli()
	s := types.Session{
		ID:          uuid.New().String(),
		CWD:         cwd,
		Title:       "",
		TimeCreated: now,
		TimeUpdated: now,
	}
	if err := sm.db.InsertSession(storage.Session{
		ID:          s.ID,
		CWD:         s.CWD,
		Title:       s.Title,
		TimeCreated: s.TimeCreated,
		TimeUpdated: s.TimeUpdated,
	}); err != nil {
		return types.Session{}, fmt.Errorf("persist session: %w", err)
	}
	sm.mu.Lock()
	sm.active[s.ID] = s
	sm.mu.Unlock()
	sm.bus.Publish(Event{Type: EventSessionCreated, SessionID: s.ID, Data: s})
	return s, nil
}

func (sm *SessionManager) Load(id string) (types.Session, error) {
	row, err := sm.db.GetSession(id)
	if err != nil {
		return types.Session{}, fmt.Errorf("load session: %w", err)
	}
	s := types.Session{
		ID:          row.ID,
		CWD:         row.CWD,
		Title:       row.Title,
		TimeCreated: row.TimeCreated,
		TimeUpdated: row.TimeUpdated,
	}
	sm.mu.Lock()
	sm.active[s.ID] = s
	sm.mu.Unlock()
	return s, nil
}

func (sm *SessionManager) Get(id string) (types.Session, bool) {
	sm.mu.RLock()
	s, ok := sm.active[id]
	sm.mu.RUnlock()
	return s, ok
}

func (sm *SessionManager) UpdateTitle(id, title string) error {
	row, err := sm.db.GetSession(id)
	if err != nil {
		return err
	}
	row.Title = title
	if err := sm.db.UpdateSession(row); err != nil {
		return err
	}
	sm.mu.Lock()
	if s, ok := sm.active[id]; ok {
		s.Title = title
		s.TimeUpdated = time.Now().UnixMilli()
		sm.active[id] = s
	}
	sm.mu.Unlock()
	sm.bus.Publish(Event{Type: EventSessionUpdated, SessionID: id})
	return nil
}

type messageData struct {
	Role string `json:"role"`
}

type MessageStore struct {
	db  *storage.DB
	bus *EventBus
}

func NewMessageStore(db *storage.DB, bus *EventBus) *MessageStore {
	return &MessageStore{db: db, bus: bus}
}

func (ms *MessageStore) CreateUserMessage(sessionID, text string) (types.Message, error) {
	return ms.createMessage(sessionID, types.RoleUser, text)
}

func (ms *MessageStore) CreateAssistantMessage(sessionID string) (types.Message, error) {
	return ms.createMessage(sessionID, types.RoleAssistant, "")
}

func (ms *MessageStore) createMessage(sessionID string, role types.Role, text string) (types.Message, error) {
	now := time.Now().UnixMilli()
	id := uuid.New().String()

	meta, _ := json.Marshal(messageData{Role: string(role)})
	m := types.Message{
		ID:          id,
		SessionID:   sessionID,
		Role:        role,
		TimeCreated: now,
	}

	var parts []types.Part
	if text != "" {
		p := types.Part{
			ID:          uuid.New().String(),
			MessageID:   id,
			SessionID:   sessionID,
			Type:        types.PartTypeText,
			TimeCreated: now,
			Data:        marshalPartData(types.PartTypeText, types.TextData{Text: text}),
		}
		parts = append(parts, p)
	}
	m.Parts = parts
	m.Meta = json.RawMessage(meta)

	if err := ms.db.InsertMessage(storage.Message{
		ID:          m.ID,
		SessionID:   m.SessionID,
		TimeCreated: m.TimeCreated,
		Data:        m.Meta,
	}); err != nil {
		return types.Message{}, fmt.Errorf("insert message: %w", err)
	}

	for _, p := range parts {
		if err := ms.db.InsertPart(storage.Part{
			ID:          p.ID,
			MessageID:   p.MessageID,
			SessionID:   p.SessionID,
			TimeCreated: p.TimeCreated,
			Data:        p.Data,
		}); err != nil {
			return types.Message{}, fmt.Errorf("insert part: %w", err)
		}
	}

	ms.bus.Publish(Event{Type: EventMessageCreated, SessionID: sessionID, Data: m})
	return m, nil
}

func (ms *MessageStore) AddTextPart(messageID, sessionID, text string) (types.Part, error) {
	p := types.Part{
		ID:          uuid.New().String(),
		MessageID:   messageID,
		SessionID:   sessionID,
		Type:        types.PartTypeText,
		TimeCreated: time.Now().UnixMilli(),
		Data:        marshalPartData(types.PartTypeText, types.TextData{Text: text}),
	}
	if err := ms.db.InsertPart(storage.Part{
		ID:          p.ID,
		MessageID:   p.MessageID,
		SessionID:   p.SessionID,
		TimeCreated: p.TimeCreated,
		Data:        p.Data,
	}); err != nil {
		return types.Part{}, fmt.Errorf("insert text part: %w", err)
	}
	ms.bus.Publish(Event{Type: EventPartCreated, SessionID: sessionID, Data: p})
	return p, nil
}

func (ms *MessageStore) AddToolPart(messageID, sessionID string, tool types.ToolPart) (types.Part, error) {
	p := types.Part{
		ID:          uuid.New().String(),
		MessageID:   messageID,
		SessionID:   sessionID,
		Type:        types.PartTypeTool,
		TimeCreated: time.Now().UnixMilli(),
		Data:        marshalPartData(types.PartTypeTool, tool),
	}
	if err := ms.db.InsertPart(storage.Part{
		ID:          p.ID,
		MessageID:   p.MessageID,
		SessionID:   p.SessionID,
		TimeCreated: p.TimeCreated,
		Data:        p.Data,
	}); err != nil {
		return types.Part{}, fmt.Errorf("insert tool part: %w", err)
	}
	ms.bus.Publish(Event{Type: EventPartCreated, SessionID: sessionID, Data: p})
	return p, nil
}

func (ms *MessageStore) UpdateToolPart(partID string, tool types.ToolPart) error {
	if err := ms.db.UpdatePart(storage.Part{
		ID:   partID,
		Data: marshalPartData(types.PartTypeTool, tool),
	}); err != nil {
		return fmt.Errorf("update tool part: %w", err)
	}
	ms.bus.Publish(Event{Type: EventPartUpdated, Data: map[string]any{"part_id": partID, "tool": tool}})
	return nil
}

type partEnvelope struct {
	Type    types.PartType  `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func marshalPartData(pt types.PartType, v any) json.RawMessage {
	payload, _ := json.Marshal(v)
	env, _ := json.Marshal(partEnvelope{Type: pt, Payload: payload})
	return env
}

func UnmarshalPartData(raw json.RawMessage, v any) (types.PartType, error) {
	var env partEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", err
	}
	if err := json.Unmarshal(env.Payload, v); err != nil {
		return "", err
	}
	return env.Type, nil
}

func (ms *MessageStore) GetMessages(sessionID string) ([]types.Message, error) {
	rows, err := ms.db.GetMessagesBySession(sessionID)
	if err != nil {
		return nil, err
	}
	msgs := make([]types.Message, 0, len(rows))
	for _, row := range rows {
		var meta messageData
		_ = json.Unmarshal(row.Data, &meta)
		m := types.Message{
			ID:          row.ID,
			SessionID:   row.SessionID,
			Role:        types.Role(meta.Role),
			TimeCreated: row.TimeCreated,
			Meta:        row.Data,
		}
		parts, err := ms.GetParts(row.ID)
		if err != nil {
			return nil, err
		}
		m.Parts = parts
		msgs = append(msgs, m)
	}
	return msgs, nil
}

func (ms *MessageStore) GetParts(messageID string) ([]types.Part, error) {
	rows, err := ms.db.GetPartsByMessage(messageID)
	if err != nil {
		return nil, err
	}
	parts := make([]types.Part, 0, len(rows))
	for _, row := range rows {
		var env struct {
			Type types.PartType `json:"type"`
		}
		_ = json.Unmarshal(row.Data, &env)
		parts = append(parts, types.Part{
			ID:          row.ID,
			MessageID:   row.MessageID,
			SessionID:   row.SessionID,
			Type:        env.Type,
			TimeCreated: row.TimeCreated,
			Data:        row.Data,
		})
	}
	return parts, nil
}
