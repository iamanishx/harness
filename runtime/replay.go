package runtime

import (
	"encoding/json"

	"goai-test/storage"
	"goai-test/types"
)

type ReplayEntry struct {
	Message *types.Message
	Part    *types.Part
}

func Replay(db *storage.DB, sessionID string) ([]ReplayEntry, error) {
	parts, err := db.GetPartsBySession(sessionID)
	if err != nil {
		return nil, err
	}

	msgs, err := db.GetMessagesBySession(sessionID)
	if err != nil {
		return nil, err
	}

	msgMap := make(map[string]types.Message, len(msgs))
	for _, row := range msgs {
		var meta struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(row.Data, &meta)
		msgMap[row.ID] = types.Message{
			ID:          row.ID,
			SessionID:   row.SessionID,
			Role:        types.Role(meta.Role),
			TimeCreated: row.TimeCreated,
			Meta:        row.Data,
		}
	}

	entries := make([]ReplayEntry, 0, len(parts))
	seenMsg := make(map[string]bool)

	for _, row := range parts {
		if !seenMsg[row.MessageID] {
			seenMsg[row.MessageID] = true
			if m, ok := msgMap[row.MessageID]; ok {
				mc := m
				entries = append(entries, ReplayEntry{Message: &mc})
			}
		}

		pt := partTypeFromEnvelope(row.Data)
		p := types.Part{
			ID:          row.ID,
			MessageID:   row.MessageID,
			SessionID:   row.SessionID,
			Type:        pt,
			TimeCreated: row.TimeCreated,
			Data:        row.Data,
		}
		pc := p
		entries = append(entries, ReplayEntry{Part: &pc})
	}

	return entries, nil
}

func partTypeFromEnvelope(raw json.RawMessage) types.PartType {
	var env struct {
		Type types.PartType `json:"type"`
	}
	_ = json.Unmarshal(raw, &env)
	return env.Type
}
