package main

// Minimal JSON-RPC 2.0 client for the Agent Client Protocol (ACP) over stdio.
// One Client = one connector process (claude-agent-acp) = one Claude account.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("%s (%s)", e.Message, string(e.Data))
	}
	return e.Message
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type ACPClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	wmu     sync.Mutex
	nextID  atomic.Int64
	pmu     sync.Mutex
	pending map[int64]chan rpcMsg
	closed  chan struct{}
	exitErr error

	// OnNotify is called (on the reader goroutine) for every notification.
	OnNotify func(method string, params json.RawMessage)
	// OnRequest is called on its own goroutine for agent->client requests.
	OnRequest func(method string, params json.RawMessage) (any, *rpcError)
}

func StartACP(bin string, env []string, logPath string) (*ACPClient, error) {
	cmd := exec.Command(bin)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
			cmd.Stderr = f
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &ACPClient{cmd: cmd, stdin: stdin, pending: map[int64]chan rpcMsg{}, closed: make(chan struct{})}
	go c.readLoop(stdout)
	return c, nil
}

func (c *ACPClient) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var m rpcMsg
			if json.Unmarshal(line, &m) == nil {
				c.dispatch(m)
			}
		}
		if err != nil {
			break
		}
	}
	c.exitErr = c.cmd.Wait()
	close(c.closed)
	c.pmu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pmu.Unlock()
}

func (c *ACPClient) dispatch(m rpcMsg) {
	switch {
	case m.Method != "" && len(m.ID) > 0: // request from agent
		go func() {
			var result any
			var rerr *rpcError
			if c.OnRequest != nil {
				result, rerr = c.OnRequest(m.Method, m.Params)
			} else {
				rerr = &rpcError{Code: -32601, Message: "method not found"}
			}
			resp := map[string]any{"jsonrpc": "2.0", "id": m.ID}
			if rerr != nil {
				resp["error"] = rerr
			} else {
				if result == nil {
					result = map[string]any{}
				}
				resp["result"] = result
			}
			c.write(resp)
		}()
	case m.Method != "": // notification
		if c.OnNotify != nil {
			c.OnNotify(m.Method, m.Params)
		}
	default: // response
		var id int64
		if json.Unmarshal(m.ID, &id) != nil {
			return
		}
		c.pmu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.pmu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

func (c *ACPClient) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.stdin.Write(b)
	return err
}

// Call sends a request and waits for the response, decoding the result into out (may be nil).
func (c *ACPClient) Call(method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan rpcMsg, 1)
	c.pmu.Lock()
	c.pending[id] = ch
	c.pmu.Unlock()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	m, ok := <-ch
	if !ok {
		return errors.New("connector exited")
	}
	if m.Error != nil {
		return m.Error
	}
	if out != nil && len(m.Result) > 0 {
		return json.Unmarshal(m.Result, out)
	}
	return nil
}

func (c *ACPClient) Notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *ACPClient) Close() {
	c.stdin.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
}
