package types

import "encoding/json"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type PartType string

const (
	PartTypeText      PartType = "text"
	PartTypeReasoning PartType = "reasoning"
	PartTypeTool      PartType = "tool"
	PartTypePatch     PartType = "patch"
)

type Message struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"session_id"`
	Role        Role            `json:"role"`
	TimeCreated int64           `json:"time_created"`
	Parts       []Part          `json:"parts,omitempty"`
	Meta        json.RawMessage `json:"meta,omitempty"`
}

type Part struct {
	ID          string          `json:"id"`
	MessageID   string          `json:"message_id"`
	SessionID   string          `json:"session_id"`
	Type        PartType        `json:"type"`
	TimeCreated int64           `json:"time_created"`
	Data        json.RawMessage `json:"data"`
}

type TextData struct {
	Text string `json:"text"`
}

type PatchData struct {
	Path  string `json:"path"`
	Patch string `json:"patch"`
}
