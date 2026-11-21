package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindInit      Kind = "init"
	KindUser      Kind = "user"
	KindText      Kind = "text"
	KindThinking  Kind = "thinking"
	KindToolStart Kind = "tool_start"
	KindToolEnd   Kind = "tool_end"
	KindResult    Kind = "result"
	KindError     Kind = "error"
	KindSystem    Kind = "system"
)

type Event struct {
	Kind      Kind      `json:"kind"`
	Text      string    `json:"text,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Cost      float64   `json:"cost,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
	Delta     bool      `json:"delta,omitempty"`
	Time      time.Time `json:"t"`
}

type Approval string

const (
	ApprovalSafe     Approval = "safe"
	ApprovalYolo     Approval = "yolo"
	ApprovalReadOnly Approval = "readonly"
)

type Spec struct {
	Prompt       string
	Dir          string
	Model        string
	ResumeID     string
	Approval     Approval
	SystemPrompt string
	ApproveMCPs  bool
	AddDirs      []string
}

type Driver interface {
	Name() string
	Binary() string
	Args(Spec) []string
	Parse(line []byte) []Event
}

var drivers = map[string]Driver{
	"claude": Claude{},
	"cursor": Cursor{},
}

func Names() []string { return []string{"claude", "cursor"} }

func Register(name string, d Driver) { drivers[name] = d }

func Get(name string) (Driver, error) {
	d, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q (supported: %s)", name, strings.Join(Names(), ", "))
	}
	return d, nil
}

func Next(name string) string {
	names := Names()
	for i, n := range names {
		if n == name {
			return names[(i+1)%len(names)]
		}
	}
	return names[0]
}

var ErrNotInstalled = errors.New("agent binary not found")

func Locate(d Driver) (string, error) {
	if p, err := exec.LookPath(d.Binary()); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{".local/bin", ".claude/local", "bin"} {
		p := filepath.Join(home, dir, d.Binary())
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotInstalled, d.Binary())
}

func Version(ctx context.Context, d Driver) (string, error) {
	bin, err := Locate(d)
	if err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", errors.New("empty version output")
	}
	return fields[0], nil
}

func Run(ctx context.Context, d Driver, spec Spec, emit func(Event)) error {
	bin, err := Locate(d)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, d.Args(spec)...)
	cmd.Dir = spec.Dir
	cmd.Env = os.Environ()
	configureProcess(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &tailBuffer{max: 8 << 10}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", d.Name(), err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		for _, ev := range d.Parse(line) {
			if ev.Time.IsZero() {
				ev.Time = time.Now()
			}
			emit(ev)
		}
	}
	scanErr := sc.Err()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("%s: %w", d.Name(), waitErr)
		}
		return fmt.Errorf("%s: %w: %s", d.Name(), waitErr, lastLines(msg, 6))
	}
	return scanErr
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func summarize(input map[string]any) string {
	for _, k := range []string{"command", "file_path", "path", "pattern", "globPattern", "query", "url", "description", "prompt", "subject"} {
		if v, ok := input[k].(string); ok && v != "" {
			return oneLine(v, 120)
		}
	}
	for _, v := range input {
		if s, ok := v.(string); ok && s != "" {
			return oneLine(s, 120)
		}
	}
	return ""
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
