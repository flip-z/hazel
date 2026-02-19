package hazel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type telemetryState struct {
	mu         sync.Mutex
	connected  bool
	usagePct   *int
	usageHint  string
	updatedAt  time.Time
	lastErr    string
	lastRoot   string
	lastReadAt time.Time
	samples    []usageSample
}

var codexTelemetry = &telemetryState{usageHint: "Usage metrics unavailable"}

type usageSample struct {
	At  time.Time
	Pct int
}

func codexTelemetrySnapshot() (*int, string) {
	codexTelemetry.mu.Lock()
	defer codexTelemetry.mu.Unlock()
	var pct *int
	if codexTelemetry.usagePct != nil {
		v := *codexTelemetry.usagePct
		pct = &v
	}
	hint := codexTelemetry.usageHint
	if hint == "" {
		hint = "Usage metrics unavailable"
	}
	if codexTelemetry.connected {
		if codexTelemetry.updatedAt.IsZero() {
			hint = "Connected to Codex telemetry"
		}
	} else if strings.TrimSpace(codexTelemetry.lastErr) != "" {
		hint = "Telemetry offline: " + codexTelemetry.lastErr
	}
	if proj := usageProjectionHint(codexTelemetry.samples); proj != "" {
		hint = strings.TrimSpace(hint + " | " + proj)
	}
	return pct, hint
}

func codexTelemetrySetConnected(root string, on bool) {
	codexTelemetry.mu.Lock()
	defer codexTelemetry.mu.Unlock()
	codexTelemetry.connected = on
	codexTelemetry.lastRoot = root
}

func codexTelemetrySetError(err error) {
	codexTelemetry.mu.Lock()
	defer codexTelemetry.mu.Unlock()
	if err != nil {
		codexTelemetry.lastErr = err.Error()
		codexTelemetry.usageHint = "Telemetry offline: " + err.Error()
	}
	codexTelemetry.connected = false
}

func codexTelemetrySetRateLimits(raw json.RawMessage) {
	type window struct {
		UsedPercent        float64 `json:"usedPercent"`
		WindowDurationMins *int    `json:"windowDurationMins"`
		ResetsAt           *int64  `json:"resetsAt"`
	}
	type snap struct {
		Primary   *window `json:"primary"`
		Secondary *window `json:"secondary"`
	}
	var payload struct {
		RateLimits *snap `json:"rateLimits"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.RateLimits == nil {
		return
	}
	pick := func(a, b *window) *window {
		if a == nil {
			return b
		}
		if b == nil {
			return a
		}
		if b.UsedPercent > a.UsedPercent {
			return b
		}
		return a
	}
	w := pick(payload.RateLimits.Primary, payload.RateLimits.Secondary)
	if w == nil {
		return
	}
	pct := int(w.UsedPercent + 0.5)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	hint := fmt.Sprintf("Used %d%%", pct)
	if w.ResetsAt != nil && *w.ResetsAt > 0 {
		rt := time.Unix(*w.ResetsAt, 0).Local()
		hint += ", resets " + rt.Format(time.RFC1123)
	}
	if w.WindowDurationMins != nil && *w.WindowDurationMins > 0 {
		hint += fmt.Sprintf(" (%s window)", formatWindowDuration(*w.WindowDurationMins))
	}

	codexTelemetry.mu.Lock()
	defer codexTelemetry.mu.Unlock()
	codexTelemetry.connected = true
	codexTelemetry.usagePct = &pct
	codexTelemetry.usageHint = hint
	codexTelemetry.updatedAt = time.Now()
	codexTelemetry.lastReadAt = time.Now()
	codexTelemetry.lastErr = ""
	now := time.Now()
	codexTelemetry.samples = append(codexTelemetry.samples, usageSample{At: now, Pct: pct})
	cutoff := now.Add(-3 * time.Hour)
	kept := codexTelemetry.samples[:0]
	for _, s := range codexTelemetry.samples {
		if s.At.After(cutoff) {
			kept = append(kept, s)
		}
	}
	codexTelemetry.samples = kept
}

func formatWindowDuration(mins int) string {
	if mins <= 0 {
		return "unknown"
	}
	days := mins / (24 * 60)
	rem := mins % (24 * 60)
	hours := rem / 60
	minutes := rem % 60

	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if len(parts) == 0 {
		return "0m"
	}
	return strings.Join(parts, " ")
}

func usageProjectionHint(samples []usageSample) string {
	if len(samples) < 2 {
		return ""
	}
	now := time.Now()
	windowStart := now.Add(-2 * time.Hour)
	firstIdx := -1
	for i, s := range samples {
		if s.At.After(windowStart) || s.At.Equal(windowStart) {
			firstIdx = i
			break
		}
	}
	if firstIdx == -1 {
		firstIdx = 0
	}
	first := samples[firstIdx]
	last := samples[len(samples)-1]
	dt := last.At.Sub(first.At).Hours()
	if dt <= 0.05 {
		return ""
	}
	burnPerHour := float64(last.Pct-first.Pct) / dt
	if burnPerHour <= 0 {
		return fmt.Sprintf("2h burn %.1f%%/h (stable/down)", burnPerHour)
	}
	if last.Pct >= 100 {
		return fmt.Sprintf("2h burn %.1f%%/h (at limit)", burnPerHour)
	}
	hoursToMax := float64(100-last.Pct) / burnPerHour
	if hoursToMax < 0 {
		hoursToMax = 0
	}
	totalMin := int(math.Round(hoursToMax * 60))
	h := totalMin / 60
	m := totalMin % 60
	if h > 0 {
		return fmt.Sprintf("2h burn %.1f%%/h, projected 100%% in %dh %dm", burnPerHour, h, m)
	}
	return fmt.Sprintf("2h burn %.1f%%/h, projected 100%% in %dm", burnPerHour, m)
}

type telemetryRPCClient struct {
	stdin      io.WriteCloser
	cmd        *exec.Cmd
	mu         sync.Mutex
	pending    map[string]chan rpcReply
	nextID     int64
	done       chan struct{}
	closedOnce sync.Once
}

func startTelemetryClient(root string) (*telemetryRPCClient, error) {
	cfg, err := loadConfigOrDefault(root)
	if err != nil {
		return nil, err
	}
	repoRoot := resolveRepoRoot(root)
	cmd := exec.Command("sh", "-c", codexCommand(cfg))
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HAZEL_MODE=telemetry",
		"HAZEL_ROOT="+repoRoot,
		"HAZEL_STATE_ROOT="+root,
		"HAZEL_REPO_ROOT="+repoRoot,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &telemetryRPCClient{stdin: stdin, cmd: cmd, pending: map[string]chan rpcReply{}, done: make(chan struct{})}
	go c.readLoop(stdout)
	go c.stderrLoop(stderr)
	go func() {
		_ = cmd.Wait()
		c.close()
	}()
	return c, nil
}

func (c *telemetryRPCClient) close() {
	c.closedOnce.Do(func() {
		c.mu.Lock()
		for _, ch := range c.pending {
			select {
			case ch <- rpcReply{Error: &rpcError{Code: -1, Message: "telemetry session terminated"}}:
			default:
			}
		}
		c.pending = map[string]chan rpcReply{}
		c.mu.Unlock()
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		close(c.done)
	})
}

func (c *telemetryRPCClient) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if len(msg.ID) > 0 && (len(msg.Result) > 0 || msg.Error != nil) {
			key := rpcIDKey(msg.ID)
			c.mu.Lock()
			ch := c.pending[key]
			if ch != nil {
				delete(c.pending, key)
			}
			c.mu.Unlock()
			if ch != nil {
				ch <- rpcReply{Result: msg.Result, Error: msg.Error}
			}
			continue
		}
		if msg.Method == "account/rateLimits/updated" {
			codexTelemetrySetRateLimits(msg.Params)
		}
	}
	c.close()
}

func (c *telemetryRPCClient) stderrLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 16*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		codexTelemetrySetError(errors.New(line))
	}
}

func (c *telemetryRPCClient) sendNotification(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *telemetryRPCClient) sendRequest(method string, params any, timeout time.Duration) (rpcReply, error) {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	idKey := strconv.FormatInt(id, 10)
	ch := make(chan rpcReply, 1)
	c.pending[idKey] = ch
	c.mu.Unlock()

	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return rpcReply{}, err
	}
	if _, err := c.stdin.Write(append(b, '\n')); err != nil {
		return rpcReply{}, err
	}

	select {
	case res := <-ch:
		if res.Error != nil {
			return rpcReply{}, fmt.Errorf("%s (%d)", res.Error.Message, res.Error.Code)
		}
		return res, nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return rpcReply{}, fmt.Errorf("request timeout: %s", method)
	case <-c.done:
		return rpcReply{}, fmt.Errorf("telemetry session closed")
	}
}

func runCodexTelemetryLoop(ctx context.Context, root string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		client, err := startTelemetryClient(root)
		if err != nil {
			codexTelemetrySetError(err)
			t := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			continue
		}
		codexTelemetrySetConnected(root, true)

		if _, err := client.sendRequest("initialize", map[string]any{
			"clientInfo":   map[string]any{"name": "hazel-telemetry", "title": "Hazel Telemetry", "version": "0.1.0"},
			"capabilities": map[string]any{"experimentalApi": true},
		}, 20*time.Second); err != nil {
			codexTelemetrySetError(err)
			client.close()
			continue
		}
		_ = client.sendNotification("initialized", nil)

		fetch := func() {
			res, err := client.sendRequest("account/rateLimits/read", map[string]any{}, 20*time.Second)
			if err != nil {
				codexTelemetrySetError(err)
				return
			}
			codexTelemetrySetRateLimits(res.Result)
		}
		fetch()

		ticker := time.NewTicker(60 * time.Second)
		running := true
		for running {
			select {
			case <-ctx.Done():
				ticker.Stop()
				client.close()
				return
			case <-ticker.C:
				fetch()
			case <-client.done:
				running = false
			}
		}
		ticker.Stop()
		codexTelemetrySetConnected(root, false)
		t := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}
