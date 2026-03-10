package filesystem

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a JSON-RPC 2.0 client over stdio for a long-lived Zig subprocess.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	enc *json.Encoder
	dec *json.Decoder

	mu      sync.Mutex // protects pending map + encoder writes
	pending map[int64]chan rpcResponse

	nextID int64

	doneCh   chan struct{}
	waitErr  error
	waitOnce sync.Once
	closed   atomic.Bool
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ToolResult struct {
	Output   string                 `json:"output,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// NewClient starts the Zig server process and prepares streaming JSON-RPC I/O.
func NewClient(zigBinaryPath string, args ...string) (*Client, error) {
	cmd := exec.Command(zigBinaryPath, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start zig server: %w", err)
	}

	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		enc:     json.NewEncoder(stdin),
		dec:     json.NewDecoder(bufio.NewReader(stdout)),
		pending: make(map[int64]chan rpcResponse),
		doneCh:  make(chan struct{}),
	}

	go c.readLoop()
	go c.stderrDrainLoop()
	go c.waitLoop()

	return c, nil
}

// Call invokes one JSON-RPC method and optionally unmarshals result into out.
func (c *Client) Call(method string, params interface{}, out interface{}) error {
	if c.closed.Load() {
		return errors.New("client is closed")
	}

	id := atomic.AddInt64(&c.nextID, 1)
	respCh := make(chan rpcResponse, 1)

	c.mu.Lock()
	c.pending[id] = respCh
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	encErr := c.enc.Encode(req)
	c.mu.Unlock()

	if encErr != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("encode request: %w", encErr)
	}

	select {
	case resp, ok := <-respCh:
		if !ok {
			return errors.New("rpc response channel closed unexpectedly")
		}
		if resp.Error != nil {
			return fmt.Errorf("rpc error (%d): %s", resp.Error.Code, resp.Error.Message)
		}
		if out == nil || len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode result: %w", err)
		}
		return nil

	case <-c.doneCh:
		return fmt.Errorf("zig process exited: %w", c.getWaitErr())

	case <-time.After(45 * time.Second):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return errors.New("rpc timeout")
	}
}

func (c *Client) readLoop() {
	for {
		var resp rpcResponse
		if err := c.dec.Decode(&resp); err != nil {
			c.failAllPending(-32000, fmt.Sprintf("decode response failed: %v", err))
			return
		}

		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()

		if ok {
			ch <- resp
			close(ch)
		}
	}
}

func (c *Client) failAllPending(code int, message string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, ch := range c.pending {
		ch <- rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &rpcError{
				Code:    code,
				Message: message,
			},
		}
		close(ch)
	}
	c.pending = make(map[int64]chan rpcResponse)
}

func (c *Client) stderrDrainLoop() {
	_, _ = io.Copy(io.Discard, c.stderr)
}

func (c *Client) waitLoop() {
	err := c.cmd.Wait()
	c.waitOnce.Do(func() {
		c.waitErr = err
		close(c.doneCh)
	})
	// If process exits first, unblock pending calls as well.
	c.failAllPending(-32001, fmt.Sprintf("zig process exited: %v", err))
}

func (c *Client) getWaitErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.waitErr == nil {
		return errors.New("process exited")
	}
	return c.waitErr
}

// Close gracefully stops client and subprocess.
func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil
	}

	_ = c.stdin.Close()

	select {
	case <-c.doneCh:
		return c.getWaitErr()
	case <-time.After(2 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-c.doneCh
		return c.getWaitErr()
	}
}

// Convenience wrappers for filesystem calls.

type ReadFileParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type WriteFileParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ListDirParams struct {
	Path string `json:"path"`
}

func (c *Client) ReadFile(path string, offset, limit int) (ToolResult, error) {
	var result ToolResult
	err := c.Call("read_file", ReadFileParams{
		Path:   path,
		Offset: offset,
		Limit:  limit,
	}, &result)
	return result, err
}

func (c *Client) WriteFile(path, content string) (ToolResult, error) {
	var result ToolResult
	err := c.Call("write_file", WriteFileParams{
		Path:    path,
		Content: content,
	}, &result)
	return result, err
}

func (c *Client) ListDir(path string) (ToolResult, error) {
	var result ToolResult
	err := c.Call("list_dir", ListDirParams{
		Path: path,
	}, &result)
	return result, err
}
