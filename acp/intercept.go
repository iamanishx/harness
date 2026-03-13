package acp

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"sync"
	"time"

	"goai-test/storage"
)

type jsonRPCMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type listSessionsResult struct {
	Sessions []sessionSummary `json:"sessions"`
}

type sessionSummary struct {
	SessionID string  `json:"sessionId"`
	CWD       string  `json:"cwd"`
	Title     *string `json:"title,omitempty"`
	UpdatedAt *string `json:"updatedAt,omitempty"`
}

type InterceptReader struct {
	inner  *bufio.Scanner
	db     *storage.DB
	writer io.Writer
	mu     sync.Mutex
	buf    []byte
	initID string
}

func NewInterceptReader(r io.Reader, w io.Writer, db *storage.DB) *InterceptReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	return &InterceptReader{
		inner:  scanner,
		db:     db,
		writer: w,
	}
}

func (ir *InterceptReader) Read(p []byte) (int, error) {
	ir.mu.Lock()
	defer ir.mu.Unlock()

	if len(ir.buf) > 0 {
		n := copy(p, ir.buf)
		ir.buf = ir.buf[n:]
		return n, nil
	}

	for {
		if !ir.inner.Scan() {
			if err := ir.inner.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}

		line := ir.inner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg jsonRPCMessage
		if json.Unmarshal(line, &msg) != nil {
			ir.buf = append(append([]byte{}, line...), '\n')
			n := copy(p, ir.buf)
			ir.buf = ir.buf[n:]
			return n, nil
		}

		if msg.Method == "unstable_listSessions" || msg.Method == "session/list" {
			log.Printf("[intercept] handling %s id=%s", msg.Method, string(*msg.ID))
			ir.handleListSessions(msg)
			continue
		}

		if msg.Method == "initialize" && msg.ID != nil {
			ir.initID = string(*msg.ID)
		}

		ir.buf = append(append([]byte{}, line...), '\n')
		n := copy(p, ir.buf)
		ir.buf = ir.buf[n:]
		return n, nil
	}
}

func (ir *InterceptReader) handleListSessions(msg jsonRPCMessage) {
	sessions, err := ir.db.ListSessions()
	if err != nil {
		log.Printf("[intercept] list sessions error: %v", err)
		sessions = nil
	}

	summaries := make([]sessionSummary, 0, len(sessions))
	for _, s := range sessions {
		title := s.Title
		updatedAt := time.UnixMilli(s.TimeUpdated).UTC().Format(time.RFC3339)
		summaries = append(summaries, sessionSummary{
			SessionID: s.ID,
			CWD:       s.CWD,
			Title:     &title,
			UpdatedAt: &updatedAt,
		})
	}

	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      *msg.ID,
		Result:  listSessionsResult{Sessions: summaries},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("[intercept] marshal list sessions response: %v", err)
		return
	}

	data = append(data, '\n')
	_, _ = ir.writer.Write(data)
	log.Printf("[intercept] sent %d sessions", len(summaries))
}

func (ir *InterceptReader) InitID() string {
	return ir.initID
}

type InterceptWriter struct {
	inner  io.Writer
	initID func() string
	mu     sync.Mutex
}

func NewInterceptWriter(w io.Writer, initIDFunc func() string) *InterceptWriter {
	return &InterceptWriter{
		inner:  w,
		initID: initIDFunc,
	}
}

func (iw *InterceptWriter) Write(p []byte) (int, error) {
	iw.mu.Lock()
	defer iw.mu.Unlock()

	initID := iw.initID()
	if initID == "" {
		return iw.inner.Write(p)
	}

	var raw map[string]json.RawMessage
	if json.Unmarshal(p, &raw) != nil {
		return iw.inner.Write(p)
	}

	idBytes, hasID := raw["id"]
	if !hasID {
		return iw.inner.Write(p)
	}

	var id json.RawMessage
	if json.Unmarshal(idBytes, &id) != nil {
		return iw.inner.Write(p)
	}

	if string(idBytes) != initID {
		return iw.inner.Write(p)
	}

	resultBytes, hasResult := raw["result"]
	if !hasResult {
		return iw.inner.Write(p)
	}

	var result map[string]json.RawMessage
	if json.Unmarshal(resultBytes, &result) != nil {
		return iw.inner.Write(p)
	}

	capBytes, hasCaps := result["agentCapabilities"]
	if !hasCaps {
		return iw.inner.Write(p)
	}

	var caps map[string]json.RawMessage
	if json.Unmarshal(capBytes, &caps) != nil {
		return iw.inner.Write(p)
	}

	caps["sessionCapabilities"] = json.RawMessage(`{"list":{},"resume":{}}`)
	newCaps, _ := json.Marshal(caps)
	result["agentCapabilities"] = newCaps
	newResult, _ := json.Marshal(result)
	raw["result"] = newResult
	out, _ := json.Marshal(raw)

	log.Printf("[intercept] injected sessionCapabilities into initialize response")

	out = append(out, '\n')
	n, err := iw.inner.Write(out)
	if err != nil {
		return n, err
	}
	return len(p), nil
}
