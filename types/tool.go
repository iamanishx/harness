package types

type ToolState string

const (
	ToolStatePending   ToolState = "pending"
	ToolStateRunning   ToolState = "running"
	ToolStateCompleted ToolState = "completed"
	ToolStateError     ToolState = "error"
)

type ToolPart struct {
	CallID    string            `json:"call_id"`
	Tool      string            `json:"tool"`
	State     ToolState         `json:"state"`
	Input     map[string]any    `json:"input,omitempty"`
	Output    string            `json:"output,omitempty"`
	Error     string            `json:"error,omitempty"`
	TimeStart int64             `json:"time_start,omitempty"`
	TimeEnd   int64             `json:"time_end,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}
