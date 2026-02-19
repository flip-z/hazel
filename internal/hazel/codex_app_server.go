package hazel

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type codexEvent struct {
	Seq       int       `json:"seq"`
	Type      string    `json:"type"`
	Text      string    `json:"text,omitempty"`
	ThreadID  string    `json:"thread_id,omitempty"`
	TurnID    string    `json:"turn_id,omitempty"`
	ItemID    string    `json:"item_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type codexApproval struct {
	RequestID string `json:"request_id"`
	Method    string `json:"method"`
	Reason    string `json:"reason,omitempty"`
	Command   string `json:"command,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
}

type codexSession struct {
	ID       string
	Key      string
	Root     string
	RepoRoot string
	TaskID   string
	Approval string

	cmd   *exec.Cmd
	stdin io.WriteCloser
	logf  *os.File

	mu             sync.Mutex
	threadID       string
	events         []codexEvent
	nextSeq        int
	pendingRPC     map[string]chan rpcReply
	pendingApprove map[string]codexApproval
	done           bool
	exitCode       *int

	nextID atomic.Int64
}

type rpcReply struct {
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type codexHub struct {
	mu       sync.Mutex
	sessions map[string]*codexSession
	byID     map[string]*codexSession
}

var appHub = &codexHub{
	sessions: map[string]*codexSession{},
	byID:     map[string]*codexSession{},
}

type CodexSessionStartResult struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	Done      bool   `json:"done"`
}

type CodexPollResult struct {
	SessionID string          `json:"session_id"`
	Cursor    int             `json:"cursor"`
	Events    []codexEvent    `json:"events"`
	Approvals []codexApproval `json:"approvals,omitempty"`
	Done      bool            `json:"done"`
	ExitCode  *int            `json:"exit_code,omitempty"`
}

type CodexTurnResult struct {
	TurnID string `json:"turn_id,omitempty"`
}

func codexSessionKey(root string, taskID string) string {
	t := strings.TrimSpace(taskID)
	if t == "" {
		t = "_default"
	}
	return root + "::" + t
}

func codexCommand(cfg Config) string {
	cmd := strings.TrimSpace(cfg.AgentChatCommand)
	if cmd != "" && strings.Contains(cmd, "app-server") {
		return cmd
	}
	cmd = strings.TrimSpace(cfg.AgentCommand)
	if cmd != "" && strings.Contains(cmd, "app-server") {
		return cmd
	}
	return "codex app-server"
}

func codexApprovalPolicy(cfg Config) string {
	v := strings.ToLower(strings.TrimSpace(cfg.CodexApprovalPolicy))
	switch v {
	case "never", "on-request":
		return v
	default:
		return "on-request"
	}
}

func startOrGetCodexSession(root string, taskID string, restart bool) (*CodexSessionStartResult, error) {
	cfg, err := loadConfigOrDefault(root)
	if err != nil {
		return nil, err
	}
	repoRoot := resolveRepoRoot(root)
	key := codexSessionKey(root, taskID)

	appHub.mu.Lock()
	existing := appHub.sessions[key]
	if existing != nil && !restart {
		sid := existing.ID
		tid := existing.currentThreadID()
		done := existing.isDone()
		appHub.mu.Unlock()
		return &CodexSessionStartResult{SessionID: sid, TaskID: taskID, ThreadID: tid, Done: done}, nil
	}
	if existing != nil {
		delete(appHub.sessions, key)
		delete(appHub.byID, existing.ID)
		go existing.stop()
	}
	appHub.mu.Unlock()

	s, err := newCodexSession(root, repoRoot, taskID, codexCommand(cfg), codexApprovalPolicy(cfg))
	if err != nil {
		return nil, err
	}

	appHub.mu.Lock()
	appHub.sessions[key] = s
	appHub.byID[s.ID] = s
	appHub.mu.Unlock()

	if err := s.bootstrap(restart); err != nil {
		_ = s.stop()
		appHub.mu.Lock()
		delete(appHub.sessions, key)
		delete(appHub.byID, s.ID)
		appHub.mu.Unlock()
		return nil, err
	}
	return &CodexSessionStartResult{SessionID: s.ID, TaskID: taskID, ThreadID: s.currentThreadID(), Done: false}, nil
}

func currentCodexSessionID(root string, taskID string) string {
	key := codexSessionKey(root, taskID)
	appHub.mu.Lock()
	defer appHub.mu.Unlock()
	if s := appHub.sessions[key]; s != nil {
		return s.ID
	}
	return ""
}

func pollCodexSession(sessionID string, cursor int) (*CodexPollResult, error) {
	s, err := getCodexSessionByID(sessionID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cursor < 0 || cursor > len(s.events) {
		cursor = 0
	}
	events := make([]codexEvent, len(s.events[cursor:]))
	copy(events, s.events[cursor:])
	approvals := make([]codexApproval, 0, len(s.pendingApprove))
	for _, a := range s.pendingApprove {
		approvals = append(approvals, a)
	}
	return &CodexPollResult{
		SessionID: s.ID,
		Cursor:    len(s.events),
		Events:    events,
		Approvals: approvals,
		Done:      s.done,
		ExitCode:  s.exitCode,
	}, nil
}

func sendCodexUserMessage(sessionID string, prompt string) (*CodexTurnResult, error) {
	s, err := getCodexSessionByID(sessionID)
	if err != nil {
		return nil, err
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	threadID := s.currentThreadID()
	if threadID == "" {
		return nil, errors.New("session has no thread id")
	}

	s.appendEvent(codexEvent{Type: "user_message", Text: prompt, ThreadID: threadID})
	promptPayload := prompt
	if ctx := strings.TrimSpace(buildCodexTaskContext(s.Root, s.TaskID)); ctx != "" {
		promptPayload = "[Hazel task context]\n" + clipped(ctx, 2600) + "\n[/Hazel task context]\n\nUser message:\n" + prompt
	}
	resp, err := s.sendRequest("turn/start", map[string]any{
		"threadId": threadID,
		"input": []map[string]any{{
			"type":          "text",
			"text":          promptPayload,
			"text_elements": []any{},
		}},
	})
	if err != nil {
		s.appendEvent(codexEvent{Type: "error", Text: err.Error(), ThreadID: threadID})
		return nil, err
	}
	var out struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(resp.Result, &out)
	return &CodexTurnResult{TurnID: out.Turn.ID}, nil
}

func respondCodexApproval(sessionID string, requestID string, decision string) error {
	s, err := getCodexSessionByID(sessionID)
	if err != nil {
		return err
	}
	reqID := strings.TrimSpace(requestID)
	if reqID == "" {
		return errors.New("request_id is required")
	}

	s.mu.Lock()
	approval, ok := s.pendingApprove[reqID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("approval request not found: %s", reqID)
	}

	decision = strings.TrimSpace(decision)
	if decision == "" {
		decision = "decline"
	}
	allowed := map[string]bool{"accept": true, "acceptForSession": true, "decline": true, "cancel": true}
	if !allowed[decision] {
		return fmt.Errorf("unsupported decision %q", decision)
	}

	result := map[string]any{"decision": decision}
	if err := s.sendResponse(reqID, result, nil); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.pendingApprove, reqID)
	s.mu.Unlock()
	s.appendEvent(codexEvent{Type: "approval_resolved", Text: approval.Method + " => " + decision})
	return nil
}

func stopCodexSession(sessionID string) error {
	s, err := getCodexSessionByID(sessionID)
	if err != nil {
		return err
	}
	appHub.mu.Lock()
	delete(appHub.sessions, s.Key)
	delete(appHub.byID, s.ID)
	appHub.mu.Unlock()
	return s.stop()
}

func getCodexSessionByID(sessionID string) (*codexSession, error) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return nil, errors.New("session_id is required")
	}
	appHub.mu.Lock()
	defer appHub.mu.Unlock()
	s := appHub.byID[id]
	if s == nil {
		return nil, fmt.Errorf("unknown session %s", id)
	}
	return s, nil
}

func newCodexSession(root string, repoRoot string, taskID string, cmdLine string, approvalPolicy string) (*codexSession, error) {
	cmd := exec.Command("sh", "-c", cmdLine)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HAZEL_MODE=chat",
		"HAZEL_ROOT="+repoRoot,
		"HAZEL_STATE_ROOT="+root,
		"HAZEL_REPO_ROOT="+repoRoot,
		"HAZEL_TASK_ID="+strings.TrimSpace(taskID),
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
	logPath := filepath.Join(hazelDir(root), "chat", "sessions", time.Now().Format("20060102")+"_"+
		strings.ReplaceAll(strings.TrimSpace(taskID), "/", "-")+"_"+strconv.FormatInt(time.Now().UnixNano(), 36)+".jsonl")
	_ = ensureDir(filepath.Dir(logPath))
	logf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)

	s := &codexSession{
		ID:             strconv.FormatInt(time.Now().UnixNano(), 36),
		Key:            codexSessionKey(root, taskID),
		Root:           root,
		RepoRoot:       repoRoot,
		TaskID:         taskID,
		Approval:       approvalPolicy,
		cmd:            cmd,
		stdin:          stdin,
		logf:           logf,
		pendingRPC:     map[string]chan rpcReply{},
		pendingApprove: map[string]codexApproval{},
		events:         make([]codexEvent, 0, 256),
		nextSeq:        1,
	}
	go s.readLoop(stdout)
	go s.stderrLoop(stderr)
	go s.waitLoop()
	return s, nil
}

func (s *codexSession) bootstrap(restart bool) error {
	devInstructions := strings.TrimSpace(buildCodexTaskContext(s.Root, s.TaskID))
	_, err := s.sendRequest("initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "hazel",
			"title":   "Hazel Nexus",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	})
	if err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}
	if err := s.sendNotification("initialized", nil); err != nil {
		return fmt.Errorf("initialized notification failed: %w", err)
	}

	threadID := ""
	if !restart {
		threadID = readCodexThreadID(s.Root, s.TaskID)
		if threadID != "" {
			if resp, err := s.sendRequest("thread/resume", map[string]any{"threadId": threadID}); err == nil {
				if tid := parseThreadIDFromResponse(resp.Result); tid != "" {
					threadID = tid
					s.setThreadID(threadID)
					s.appendEvent(codexEvent{Type: "thread_resumed", ThreadID: threadID, Text: "resumed existing thread"})
				}
			} else {
				s.appendEvent(codexEvent{Type: "warning", Text: "failed to resume thread; starting new thread"})
				threadID = ""
			}
		}
	}

	if threadID == "" {
		policy := strings.TrimSpace(s.Approval)
		if policy == "" {
			policy = "on-request"
		}
		resp, err := s.sendRequest("thread/start", map[string]any{
			"cwd":                   s.RepoRoot,
			"approvalPolicy":        policy,
			"experimentalRawEvents": false,
			"developerInstructions": devInstructions,
		})
		if err != nil {
			return fmt.Errorf("thread/start failed: %w", err)
		}
		threadID = parseThreadIDFromResponse(resp.Result)
		if threadID == "" {
			return errors.New("thread/start returned empty thread id")
		}
		s.setThreadID(threadID)
		s.appendEvent(codexEvent{Type: "thread_started", ThreadID: threadID, Text: "started new thread"})
	}
	if err := writeCodexThreadID(s.Root, s.TaskID, threadID); err != nil {
		s.appendEvent(codexEvent{Type: "warning", Text: "failed to persist thread id: " + err.Error()})
	}
	return nil
}

func parseThreadIDFromResponse(raw json.RawMessage) string {
	var obj struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return strings.TrimSpace(obj.Thread.ID)
}

func (s *codexSession) currentThreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

func (s *codexSession) setThreadID(id string) {
	s.mu.Lock()
	s.threadID = strings.TrimSpace(id)
	s.mu.Unlock()
}

func (s *codexSession) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		s.handleRPCLine([]byte(line))
	}
	if err := scanner.Err(); err != nil {
		s.appendEvent(codexEvent{Type: "error", Text: "app-server read error: " + err.Error()})
	}
}

func (s *codexSession) stderrLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 16*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		s.appendEvent(codexEvent{Type: "stderr", Text: line})
	}
}

func (s *codexSession) waitLoop() {
	err := s.cmd.Wait()
	code := 0
	if err != nil {
		if ee := (*exec.ExitError)(nil); errorAs(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	s.mu.Lock()
	s.done = true
	s.exitCode = &code
	for _, ch := range s.pendingRPC {
		select {
		case ch <- rpcReply{Error: &rpcError{Code: -1, Message: "session terminated"}}:
		default:
		}
	}
	s.pendingRPC = map[string]chan rpcReply{}
	s.mu.Unlock()
	s.appendEvent(codexEvent{Type: "session_done", Text: fmt.Sprintf("codex app-server exited (%d)", code)})
}

func (s *codexSession) handleRPCLine(line []byte) {
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		s.appendEvent(codexEvent{Type: "warning", Text: "invalid app-server json: " + err.Error()})
		return
	}
	if len(probe.ID) > 0 && (len(probe.Result) > 0 || probe.Error != nil) {
		key := rpcIDKey(probe.ID)
		s.mu.Lock()
		ch := s.pendingRPC[key]
		if ch != nil {
			delete(s.pendingRPC, key)
		}
		s.mu.Unlock()
		if ch != nil {
			ch <- rpcReply{Result: probe.Result, Error: probe.Error}
		}
		return
	}
	if probe.Method == "" {
		return
	}
	if len(probe.ID) > 0 {
		s.handleServerRequest(probe.Method, probe.ID, probe.Params)
		return
	}
	s.handleNotification(probe.Method, probe.Params)
}

func (s *codexSession) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "thread/started":
		var p struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := json.Unmarshal(params, &p); err == nil && strings.TrimSpace(p.Thread.ID) != "" {
			s.setThreadID(p.Thread.ID)
			_ = writeCodexThreadID(s.Root, s.TaskID, p.Thread.ID)
			s.appendEvent(codexEvent{Type: "thread_started", Text: "thread started", ThreadID: strings.TrimSpace(p.Thread.ID)})
		}
	case "item/agentMessage/delta":
		var p struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			s.appendEvent(codexEvent{Type: "assistant_delta", Text: p.Delta, ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.ItemID})
		}
	case "item/commandExecution/outputDelta":
		var p struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			s.appendEvent(codexEvent{Type: "tool_output", Text: p.Delta, ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.ItemID})
		}
	case "item/commandExecution/started":
		var p struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Command  string `json:"command"`
		}
		if err := json.Unmarshal(params, &p); err == nil && strings.TrimSpace(p.Command) != "" {
			s.appendEvent(codexEvent{Type: "tool_command", Text: strings.TrimSpace(p.Command), ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.ItemID})
		}
	case "item/completed":
		var p struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Item     struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			if p.Item.Type == "agentMessage" && strings.TrimSpace(p.Item.Text) != "" {
				s.appendEvent(codexEvent{Type: "assistant_message", Text: p.Item.Text, ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.Item.ID})
			}
		}
	case "turn/completed":
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turn"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			s.appendEvent(codexEvent{Type: "turn_completed", Text: p.Turn.Status, ThreadID: p.ThreadID, TurnID: p.Turn.ID})
		}
	case "turn/started":
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			s.appendEvent(codexEvent{Type: "turn_started", ThreadID: p.ThreadID, TurnID: p.Turn.ID})
		}
	case "error":
		var p struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Error    struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			s.appendEvent(codexEvent{Type: "error", Text: p.Error.Message, ThreadID: p.ThreadID, TurnID: p.TurnID})
		}
	default:
		// Ignore noisy protocol notifications in UI stream by default.
		return
	}
}

func (s *codexSession) handleServerRequest(method string, idRaw json.RawMessage, params json.RawMessage) {
	requestID := rpcIDKey(idRaw)
	switch method {
	case "item/commandExecution/requestApproval":
		var p struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Reason   string `json:"reason"`
			Command  string `json:"command"`
			Cwd      string `json:"cwd"`
		}
		_ = json.Unmarshal(params, &p)
		a := codexApproval{RequestID: requestID, Method: method, Reason: strings.TrimSpace(p.Reason), Command: strings.TrimSpace(p.Command), Cwd: strings.TrimSpace(p.Cwd)}
		s.mu.Lock()
		s.pendingApprove[requestID] = a
		s.mu.Unlock()
		s.appendEvent(codexEvent{Type: "approval_requested", Text: method + ": " + strings.TrimSpace(p.Command), ItemID: requestID})
		if strings.TrimSpace(p.Command) != "" {
			s.appendEvent(codexEvent{Type: "tool_command", Text: strings.TrimSpace(p.Command), ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.ItemID})
		}
	case "item/fileChange/requestApproval":
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		a := codexApproval{RequestID: requestID, Method: method, Reason: strings.TrimSpace(p.Reason)}
		s.mu.Lock()
		s.pendingApprove[requestID] = a
		s.mu.Unlock()
		s.appendEvent(codexEvent{Type: "approval_requested", Text: method})
	case "item/tool/requestUserInput":
		_ = s.sendResponse(requestID, map[string]any{"answers": map[string]any{}}, nil)
		s.appendEvent(codexEvent{Type: "notice", Text: method + " auto-answered"})
	default:
		_ = s.sendResponse(requestID, nil, &rpcError{Code: -32601, Message: "unsupported server request in hazel"})
		s.appendEvent(codexEvent{Type: "warning", Text: "unsupported server request: " + method})
	}
}

func (s *codexSession) sendRequest(method string, params any) (rpcReply, error) {
	id := s.nextID.Add(1)
	idKey := strconv.FormatInt(id, 10)
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return rpcReply{}, err
	}
	ch := make(chan rpcReply, 1)
	s.mu.Lock()
	s.pendingRPC[idKey] = ch
	s.mu.Unlock()
	if _, err := s.stdin.Write(append(b, '\n')); err != nil {
		s.mu.Lock()
		delete(s.pendingRPC, idKey)
		s.mu.Unlock()
		return rpcReply{}, err
	}

	select {
	case res := <-ch:
		if res.Error != nil {
			return rpcReply{}, fmt.Errorf("%s (%d)", res.Error.Message, res.Error.Code)
		}
		return res, nil
	case <-time.After(90 * time.Second):
		s.mu.Lock()
		delete(s.pendingRPC, idKey)
		s.mu.Unlock()
		return rpcReply{}, errors.New("request timeout")
	}
}

func (s *codexSession) sendNotification(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func (s *codexSession) sendResponse(requestID string, result any, rpcErr *rpcError) error {
	msg := map[string]any{"jsonrpc": "2.0", "id": rpcIDValue(requestID)}
	if rpcErr != nil {
		msg["error"] = rpcErr
	} else {
		msg["result"] = result
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func rpcIDKey(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return strconv.FormatInt(n, 10)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(raw))
}

func rpcIDValue(key string) any {
	if n, err := strconv.ParseInt(strings.TrimSpace(key), 10, 64); err == nil {
		return n
	}
	return key
}

func (s *codexSession) appendEvent(e codexEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Seq = s.nextSeq
	e.CreatedAt = time.Now()
	s.nextSeq++
	s.events = append(s.events, e)
	if len(s.events) > 6000 {
		s.events = append([]codexEvent(nil), s.events[len(s.events)-4000:]...)
	}
	if s.logf != nil {
		if b, err := json.Marshal(e); err == nil {
			_, _ = s.logf.Write(append(b, '\n'))
		}
	}
}

func (s *codexSession) isDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *codexSession) stop() error {
	s.mu.Lock()
	alreadyDone := s.done
	cmd := s.cmd
	stdin := s.stdin
	s.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if s.logf != nil {
		_ = s.logf.Close()
	}
	if !alreadyDone && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}

func buildCodexTaskContext(root string, taskID string) string {
	taskID = strings.TrimSpace(taskID)
	repoRoot := resolveRepoRoot(root)
	var taskTitle, taskStatus, taskMD string
	if taskID != "" {
		var b Board
		if err := readYAMLFile(boardPath(root), &b); err == nil {
			for _, t := range b.Tasks {
				if t.ID == taskID {
					taskTitle = strings.TrimSpace(t.Title)
					taskStatus = string(t.Status)
					break
				}
			}
		}
		if mdb, err := os.ReadFile(taskFile(root, taskID, "task.md")); err == nil {
			taskMD = string(mdb)
		}
	}
	wikiReadme, _ := os.ReadFile(filepath.Join(root, "wiki", "README.md"))
	wikiFeatures, _ := os.ReadFile(filepath.Join(root, "wiki", "FEATURES_AND_USAGE.md"))
	wikiChangelog, _ := os.ReadFile(filepath.Join(root, "wiki", "CHANGELOG.md"))
	agentsMD, _ := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))

	var sb strings.Builder
	sb.WriteString("Hazel Nexus task context. Prioritize this context while responding.\n\n")
	sb.WriteString("State root: " + root + "\n")
	sb.WriteString("Repo root: " + repoRoot + "\n")
	if taskID != "" {
		sb.WriteString("Task ID: " + taskID + "\n")
	}
	if taskTitle != "" {
		sb.WriteString("Task Title: " + taskTitle + "\n")
	}
	if taskStatus != "" {
		sb.WriteString("Task Status: " + taskStatus + "\n")
	}
	if strings.TrimSpace(taskMD) != "" {
		sb.WriteString("\nTask markdown:\n```markdown\n" + clipped(taskMD, 4000) + "\n```\n")
	}
	if strings.TrimSpace(string(agentsMD)) != "" {
		sb.WriteString("\nAGENTS.md:\n```markdown\n" + clipped(string(agentsMD), 2500) + "\n```\n")
	}
	if strings.TrimSpace(string(wikiReadme)) != "" {
		sb.WriteString("\nWiki README:\n```markdown\n" + clipped(string(wikiReadme), 1800) + "\n```\n")
	}
	if strings.TrimSpace(string(wikiFeatures)) != "" {
		sb.WriteString("\nWiki FEATURES_AND_USAGE:\n```markdown\n" + clipped(string(wikiFeatures), 1800) + "\n```\n")
	}
	if strings.TrimSpace(string(wikiChangelog)) != "" {
		sb.WriteString("\nWiki CHANGELOG:\n```markdown\n" + clipped(string(wikiChangelog), 1200) + "\n```\n")
	}
	return sb.String()
}

type codexThreadIndex struct {
	Tasks map[string]string `json:"tasks"`
}

func codexThreadIndexPath(root string) string {
	return filepath.Join(hazelDir(root), "chat", "threads.json")
}

func codexThreadTaskKey(taskID string) string {
	t := strings.TrimSpace(taskID)
	if t == "" {
		return "_default"
	}
	return t
}

func readCodexThreadID(root string, taskID string) string {
	b, err := os.ReadFile(codexThreadIndexPath(root))
	if err != nil {
		return ""
	}
	var idx codexThreadIndex
	if err := json.Unmarshal(b, &idx); err != nil {
		return ""
	}
	if idx.Tasks == nil {
		return ""
	}
	return strings.TrimSpace(idx.Tasks[codexThreadTaskKey(taskID)])
}

func writeCodexThreadID(root string, taskID string, threadID string) error {
	p := codexThreadIndexPath(root)
	idx := codexThreadIndex{Tasks: map[string]string{}}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &idx)
		if idx.Tasks == nil {
			idx.Tasks = map[string]string{}
		}
	}
	idx.Tasks[codexThreadTaskKey(taskID)] = strings.TrimSpace(threadID)
	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return writeFileAtomic(p, out, 0o644)
}
