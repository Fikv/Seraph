package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2025-06-18"

var ErrAuth = errors.New("server requires authentication")

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func rpc(id int, method string, params any) []byte {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if id > 0 {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	b, _ := json.Marshal(m)
	return b
}

func initParams() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "seraph", "version": "0.1.0"},
	}
}

func toolNames(raw json.RawMessage) []string {
	var r struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(raw, &r)
	out := make([]string, 0, len(r.Tools))
	for _, t := range r.Tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

func Probe(ctx context.Context, s Server) ([]string, error) {
	switch s.Transport {
	case TransportStdio:
		return probeStdio(ctx, s)
	case TransportHTTP:
		return probeHTTP(ctx, s)
	}
	return nil, fmt.Errorf("testing %s servers is not supported yet", s.Transport)
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buf.Len() < 16<<10 {
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func probeStdio(ctx context.Context, s Server) ([]string, error) {
	cmd := exec.CommandContext(ctx, s.Command, s.Args...)
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", s.Command, err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	lines := make(chan rpcResponse, 8)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
		for sc.Scan() {
			var r rpcResponse
			if json.Unmarshal(sc.Bytes(), &r) == nil && len(r.ID) > 0 {
				lines <- r
			}
		}
	}()
	wait := func(id string) (json.RawMessage, error) {
		for {
			select {
			case <-ctx.Done():
				return nil, withStderr(ctx.Err(), stderr.String())
			case r, ok := <-lines:
				if !ok {
					return nil, withStderr(errors.New("server exited"), stderr.String())
				}
				if string(r.ID) != id {
					continue
				}
				if r.Error != nil {
					return nil, fmt.Errorf("%s (code %d)", r.Error.Message, r.Error.Code)
				}
				return r.Result, nil
			}
		}
	}

	send := func(b []byte) error {
		_, err := stdin.Write(append(b, '\n'))
		return err
	}
	if err := send(rpc(1, "initialize", initParams())); err != nil {
		return nil, withStderr(err, stderr.String())
	}
	if _, err := wait("1"); err != nil {
		return nil, err
	}
	_ = send(rpc(0, "notifications/initialized", nil))
	if err := send(rpc(2, "tools/list", map[string]any{})); err != nil {
		return nil, err
	}
	res, err := wait("2")
	if err != nil {
		return nil, err
	}
	return toolNames(res), nil
}

func withStderr(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	lines := strings.Split(stderr, "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return fmt.Errorf("%w: %s", err, strings.Join(lines, " | "))
}

func probeHTTP(ctx context.Context, s Server) ([]string, error) {
	session := ""
	post := func(body []byte) (json.RawMessage, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
		for k, v := range s.Headers {
			req.Header.Set(k, v)
		}
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
			session = id
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, ErrAuth
		case resp.StatusCode == http.StatusAccepted:
			return nil, nil
		case resp.StatusCode >= 300:
			return nil, fmt.Errorf("http %s", resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			data = lastSSEData(data)
		}
		var r rpcResponse
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("unexpected response: %w", err)
		}
		if r.Error != nil {
			return nil, fmt.Errorf("%s (code %d)", r.Error.Message, r.Error.Code)
		}
		return r.Result, nil
	}
	if _, err := post(rpc(1, "initialize", initParams())); err != nil {
		return nil, err
	}
	_, _ = post(rpc(0, "notifications/initialized", nil))
	res, err := post(rpc(2, "tools/list", map[string]any{}))
	if err != nil {
		return nil, err
	}
	return toolNames(res), nil
}

func lastSSEData(b []byte) []byte {
	var last []byte
	for _, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if bytes.HasPrefix(line, []byte("data:")) {
			last = bytes.TrimSpace(line[5:])
		}
	}
	return last
}
