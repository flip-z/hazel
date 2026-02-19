package hazel

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
)

func uiChat(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	projectRoot, projectKey, err := resolveProjectRootForView(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	title := "Hazel Codex"
	if nexus != nil {
		if p, ok := nexus.ProjectByKey(projectKey); ok {
			title = p.Name + " Codex"
		}
	}

	var taskIDs []string
	var b Board
	if err := readYAMLFile(boardPath(projectRoot), &b); err == nil {
		for _, t := range b.Tasks {
			taskIDs = append(taskIDs, t.ID)
		}
	}
	selectedTask := strings.TrimSpace(r.URL.Query().Get("task"))
	selectedSession := strings.TrimSpace(r.URL.Query().Get("session"))
	if selectedTask == "" && selectedSession != "" {
		sessionName := selectedSession
		if !strings.HasSuffix(sessionName, ".jsonl") {
			sessionName += ".jsonl"
		}
		if t := strings.TrimSpace(taskIDFromChatSessionName(sessionName)); t != "" {
			selectedTask = t
		}
	}
	existingSessionID := currentCodexSessionID(projectRoot, selectedTask)
	initialEvents := []codexEvent{}
	if existingSessionID == "" {
		if selectedSession != "" {
			sessionName := selectedSession
			if !strings.HasSuffix(sessionName, ".jsonl") {
				sessionName += ".jsonl"
			}
			if evs, err := loadChatSessionEvents(filepath.Join(chatSessionsDir(projectRoot), sessionName)); err == nil {
				initialEvents = evs
			}
		} else if ss, ok := latestChatSessionForTask(projectRoot, selectedTask); ok {
			if evs, err := loadChatSessionEvents(ss.Path); err == nil {
				initialEvents = evs
			}
		}
	}
	initialEventsJSON := "[]"
	if b, err := json.Marshal(initialEvents); err == nil {
		initialEventsJSON = string(b)
	}

	tpl := template.Must(template.New("chat").Parse(uiChatHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	embed := strings.TrimSpace(r.URL.Query().Get("embed")) == "1"
	autoRun := strings.TrimSpace(r.URL.Query().Get("autorun")) == "1"
	_ = tpl.Execute(w, map[string]any{
		"Title":             title,
		"SelectedProject":   projectKey,
		"TaskIDs":           taskIDs,
		"SelectedTask":      selectedTask,
		"SelectedSession":   selectedSession,
		"Embed":             embed,
		"AutoRun":           autoRun,
		"ExistingSessionID": existingSessionID,
		"InitialEventsJSON": template.JS(initialEventsJSON),
	})
}

func apiCodexSessionStart(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectRoot, _, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	taskID := strings.TrimSpace(r.FormValue("task_id"))
	restart := strings.TrimSpace(r.FormValue("restart")) == "1"
	res, err := startOrGetCodexSession(projectRoot, taskID, restart)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(res)
}

func apiCodexSessionPoll(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	_ = root
	_ = nexus
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	cursor := 0
	if s := strings.TrimSpace(r.URL.Query().Get("cursor")); s != "" {
		_, _ = fmt.Sscanf(s, "%d", &cursor)
	}
	res, err := pollCodexSession(sessionID, cursor)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(res)
}

func apiCodexTurn(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	_ = root
	_ = nexus
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	prompt := r.FormValue("prompt")
	res, err := sendCodexUserMessage(sessionID, prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(res)
}

func apiCodexApproval(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	_ = root
	_ = nexus
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	requestID := strings.TrimSpace(r.FormValue("request_id"))
	decision := strings.TrimSpace(r.FormValue("decision"))
	if err := respondCodexApproval(sessionID, requestID, decision); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func apiCodexSessionStop(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	_ = root
	_ = nexus
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	if err := stopCodexSession(sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func resolveProjectRootForView(nexus *Nexus, r *http.Request, fallback string) (projectRoot string, projectKey string, err error) {
	_ = fallback
	if nexus == nil {
		return "", "", fmt.Errorf("nexus mode required")
	}
	key := strings.TrimSpace(r.URL.Query().Get("project"))
	if key == "" {
		return "", "", fmt.Errorf("project is required")
	}
	p, ok := nexus.ProjectByKey(key)
	if !ok {
		return "", "", fmt.Errorf("unknown project %q", key)
	}
	return p.StorageRoot, key, nil
}

const uiChatHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}}</title>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&display=swap');
    :root {
      --bg:#102022;
      --panel:#193133;
      --line:#326267;
      --fg:#e7fbff;
      --accent:#13daec;
      --warn:#facc15;
      --danger:#ff6b6b;
    }
    * { box-sizing:border-box; }
    body { margin:0; font-family:"Space Grotesk",ui-sans-serif,system-ui; background:var(--bg); color:var(--fg); min-height:100dvh; display:flex; flex-direction:column; }
    body::before { content:""; position:fixed; inset:0; pointer-events:none; background:linear-gradient(rgba(19,218,236,.04) 50%, rgba(0,0,0,0) 50%); background-size:100% 4px; }
    header { border-bottom:1px solid var(--line); background:rgba(16,32,34,.94); }
    .wrap { padding:10px 14px; }
    a { color:var(--accent); text-decoration:none; font-size:11px; text-transform:uppercase; }
    main { width:100%; margin:0 auto; padding:10px; flex:1; display:flex; min-height:0; }
    .panel { width:100%; border:1px solid var(--line); background:rgba(25,49,51,.35); display:grid; grid-template-rows:auto 1fr auto; min-height:0; }
    .toolbar { display:flex; gap:8px; align-items:center; padding:8px; border-bottom:1px solid var(--line); flex-wrap:wrap; }
    .toolbar label { font-size:10px; color:#8dc7cf; text-transform:uppercase; }
    select,button,textarea { background:rgba(0,0,0,.25); border:1px solid var(--line); color:var(--fg); border-radius:4px; }
    select,button { font-size:11px; text-transform:uppercase; padding:7px 9px; }
    button:hover { border-color:var(--accent); color:var(--accent); }
    .meta { margin-left:auto; font-size:10px; color:#8dc7cf; text-transform:uppercase; }
    .body { display:grid; grid-template-columns:1fr 280px; min-height:0; }
    .stream { min-height:0; overflow:auto; padding:10px; font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace; font-size:12px; white-space:pre-wrap; }
    .line { margin-bottom:5px; }
    .line-user { color:#9de8f3; }
    .line-assistant { color:#f5ffff; }
    .line-tool { color:#b8f8a2; }
    .line-warn { color:var(--warn); }
    .line-err { color:var(--danger); }
    .approvals { border-left:1px solid var(--line); overflow:auto; padding:8px; min-height:0; }
    .approval { border:1px solid var(--line); background:rgba(0,0,0,.2); padding:8px; margin-bottom:8px; font-size:11px; }
    .approval h4 { margin:0 0 6px; font-size:11px; color:var(--warn); text-transform:uppercase; }
    .approval code { color:#c2f6ff; word-break:break-all; }
    .approval .actions { display:flex; gap:6px; margin-top:8px; }
    .approval .actions button { flex:1; }
    .composer { border-top:1px solid var(--line); padding:8px; display:grid; grid-template-columns:1fr auto; gap:8px; }
    textarea { width:100%; min-height:68px; resize:vertical; padding:8px; font-size:12px; }
    @media (max-width: 900px) {
      .body { grid-template-columns:1fr; }
      .approvals { border-left:none; border-top:1px solid var(--line); max-height:180px; }
    }
  </style>
</head>
<body>
  {{if not .Embed}}
  <header>
    <div class="wrap">
      <a href="{{if .SelectedProject}}/?project={{.SelectedProject}}{{else}}/{{end}}">Back to board</a>
      <h1 style="margin:8px 0 0; color:#13daec; font-size:15px; text-transform:uppercase; letter-spacing:.1em;">{{.Title}}</h1>
    </div>
  </header>
  {{end}}
  <main>
    <section class="panel">
      <div class="toolbar">
        {{if .SelectedProject}}<input type="hidden" id="hzProject" value="{{.SelectedProject}}" />{{end}}
        <label>Task</label>
        <select id="hzTask">
          <option value="">(none)</option>
          {{range .TaskIDs}}
            <option value="{{.}}" {{if eq $.SelectedTask .}}selected{{end}}>{{.}}</option>
          {{end}}
        </select>
        <span class="meta" id="hzMeta">Idle</span>
      </div>
      <div class="body">
        <div class="stream" id="hzStream"></div>
        <aside class="approvals" id="hzApprovals"></aside>
      </div>
      <form class="composer" id="hzComposer">
        <textarea id="hzPrompt" placeholder="Message Codex for this task..."></textarea>
        <button type="submit">Send</button>
      </form>
    </section>
  </main>

  <script>
    let sessionID = "{{.ExistingSessionID}}";
    let cursor = 0;
    let pollTimer = null;
    let streamState = { assistantKey: '', assistantEl: null };
    const assistantByItem = new Map();
    const toolByItem = new Map();
    const toolCommandByItem = new Map();
    const pendingToolCommands = [];
    const streamedAssistantItems = new Set();
    let autoRan = false;
    const initialEvents = {{.InitialEventsJSON}};

    function setMeta(s) {
      const el = document.getElementById('hzMeta');
      if (el) el.textContent = s;
    }

    function appendLine(type, text) {
      const stream = document.getElementById('hzStream');
      const div = document.createElement('div');
      let cls = 'line '; 
      if (type === 'user') cls += 'line-user';
      else if (type === 'assistant') cls += 'line-assistant';
      else if (type === 'tool') cls += 'line-tool';
      else if (type === 'error') cls += 'line-err';
      else cls += 'line-warn';
      div.className = cls;
      div.textContent = text;
      stream.appendChild(div);
      stream.scrollTop = stream.scrollHeight;
      return div;
    }

    function parseCommandFromApprovalText(s) {
      const txt = (s || '').trim();
      const idx = txt.indexOf(': ');
      if (idx === -1) return '';
      return txt.slice(idx + 2).trim();
    }

    function escapeHTML(s) {
      return (s || '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }

    function renderMarkdown(s) {
      const bt = String.fromCharCode(96);
      const fence = bt + bt + bt;
      const lines = String(s || '').split('\n');
      const out = [];
      let inCode = false;
      for (const line of lines) {
        if (line.trim().startsWith(fence)) {
          out.push(inCode ? '</code></pre>' : '<pre><code>');
          inCode = !inCode;
          continue;
        }
        if (inCode) {
          out.push(escapeHTML(line));
          continue;
        }
        let h = escapeHTML(line);
        let start = h.indexOf(bt);
        while (start !== -1) {
          const end = h.indexOf(bt, start + 1);
          if (end === -1) break;
          h = h.slice(0, start) + '<code>' + h.slice(start + 1, end) + '</code>' + h.slice(end + 1);
          start = h.indexOf(bt, start + 13);
        }
        h = h.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
        if (h.startsWith('&gt;')) {
          h = '<blockquote style="margin:4px 0;padding-left:8px;border-left:2px solid rgba(255,255,255,.2);">' + h + '</blockquote>';
        }
        out.push(h);
      }
      if (inCode) out.push('</code></pre>');
      return out.join('<br>');
    }

    function appendChunk(type, text, key) {
      if (!text) return;
      if (type === 'assistant') {
        const k = key || '__assistant__';
        if (!assistantByItem.has(k)) {
          assistantByItem.set(k, appendLine('assistant', ''));
        }
        const el = assistantByItem.get(k);
        el.textContent += text;
        streamedAssistantItems.add(k);
      } else if (type === 'tool') {
        const k = key || ('tool_' + Date.now());
        let block = toolByItem.get(k);
        if (!block) {
          const stream = document.getElementById('hzStream');
          const details = document.createElement('details');
          details.className = 'line';
          const summary = document.createElement('summary');
          summary.className = 'line-tool';
          let label = toolCommandByItem.get(k) || '';
          if (!label && pendingToolCommands.length) label = pendingToolCommands.shift();
          summary.textContent = label || 'Command';
          const pre = document.createElement('pre');
          pre.style.margin = '6px 0 8px';
          pre.style.whiteSpace = 'pre-wrap';
          pre.style.border = '1px solid rgba(255,255,255,.12)';
          pre.style.borderRadius = '4px';
          pre.style.padding = '8px';
          pre.style.background = 'rgba(0,0,0,.2)';
          details.appendChild(summary);
          details.appendChild(pre);
          stream.appendChild(details);
          block = { details, pre };
          toolByItem.set(k, block);
        }
        block.pre.textContent += text;
      }
      const stream = document.getElementById('hzStream');
      stream.scrollTop = stream.scrollHeight;
    }

    function renderApprovals(list) {
      const root = document.getElementById('hzApprovals');
      root.innerHTML = '';
      if (!list || !list.length) {
        const p = document.createElement('div');
        p.className = 'line line-warn';
        p.textContent = '[no pending approvals]';
        root.appendChild(p);
        return;
      }
      for (const a of list) {
        const box = document.createElement('div');
        box.className = 'approval';
        box.innerHTML =
          '<h4>Approval Required</h4>' +
          '<div><strong>Method:</strong> <code>' + (a.method || '') + '</code></div>' +
          (a.reason ? '<div><strong>Reason:</strong> ' + a.reason + '</div>' : '') +
          (a.command ? '<div><strong>Command:</strong> <code>' + a.command + '</code></div>' : '') +
          (a.cwd ? '<div><strong>CWD:</strong> <code>' + a.cwd + '</code></div>' : '') +
          '<div class=\"actions\">' +
          '<button type=\"button\" data-id=\"' + a.request_id + '\" data-decision=\"accept\">Accept</button>' +
          '<button type=\"button\" data-id=\"' + a.request_id + '\" data-decision=\"decline\">Decline</button>' +
          '</div>';
        root.appendChild(box);
      }
      root.querySelectorAll('button[data-id]').forEach((btn) => {
        btn.addEventListener('click', () => {
          submitApproval(btn.getAttribute('data-id'), btn.getAttribute('data-decision'));
        });
      });
    }

    function appendInlineApproval(ev) {
      const stream = document.getElementById('hzStream');
      const box = document.createElement('div');
      box.className = 'approval';
      const rid = ev.item_id || '';
      box.innerHTML =
        '<h4>Approval Required</h4>' +
        '<div>' + (ev.text || '') + '</div>' +
        '<div class="actions">' +
        '<button type="button" data-id="' + rid + '" data-decision="accept">Accept</button>' +
        '<button type="button" data-id="' + rid + '" data-decision="decline">Decline</button>' +
        '</div>';
      stream.appendChild(box);
      stream.scrollTop = stream.scrollHeight;
      box.querySelectorAll('button[data-id]').forEach((btn) => {
        btn.addEventListener('click', async () => {
          const id = btn.getAttribute('data-id');
          if (!id) return;
          await submitApproval(id, btn.getAttribute('data-decision') || 'decline');
        });
      });
    }

    function renderEvent(ev) {
      if (ev.type === 'assistant_delta') appendChunk('assistant', ev.text || '', ev.item_id || ev.turn_id || '');
      else if (ev.type === 'tool_command') {
        const k = ev.item_id || ev.turn_id || '';
        if (ev.text) pendingToolCommands.push(ev.text);
        if (k) {
          toolCommandByItem.set(k, ev.text || 'Command');
          const block = toolByItem.get(k);
          if (block && block.details) {
            const summary = block.details.querySelector('summary');
            if (summary) summary.textContent = ev.text || 'Command';
          }
        } else {
          appendLine('tool', ev.text || 'Command');
        }
      }
      else if (ev.type === 'assistant_message') {
        const k = ev.item_id || ev.turn_id || '';
        if (k && assistantByItem.has(k)) {
          assistantByItem.get(k).innerHTML = renderMarkdown(ev.text || '');
          streamedAssistantItems.delete(k);
        } else if (!k || !streamedAssistantItems.has(k)) {
          const el = appendLine('assistant', '');
          el.innerHTML = renderMarkdown(ev.text || '');
        }
      }
      else if (ev.type === 'user_message') appendLine('user', '> ' + (ev.text || ''));
      else if (ev.type === 'tool_output') appendChunk('tool', ev.text || '', ev.item_id || ev.turn_id || '');
      else if (ev.type === 'error') appendLine('error', '[error] ' + (ev.text || ''));
      else if (ev.type === 'session_done') appendLine('warn', '[session] ' + (ev.text || 'done'));
      else if (ev.type === 'approval_requested') {
        const cmd = parseCommandFromApprovalText(ev.text || '');
        if (cmd) pendingToolCommands.push(cmd);
        appendInlineApproval(ev);
      }
      else if (ev.type === 'approval_resolved' || ev.type === 'warning') appendLine('warn', '[' + ev.type + '] ' + (ev.text || ''));
    }

    async function startSession(restart, preserveStream) {
      const task = document.getElementById('hzTask').value;
      const projectEl = document.getElementById('hzProject');
      const project = projectEl ? projectEl.value : '';
      const body = new URLSearchParams({ task_id: task, restart: restart ? '1' : '0' });
      if (project) body.set('project', project);
      const res = await fetch('/api/codex/session/start', { method:'POST', body });
      if (!res.ok) throw new Error(await res.text());
      const js = await res.json();
      sessionID = js.session_id;
      cursor = 0;
      if (!preserveStream) {
        document.getElementById('hzStream').innerHTML = '';
        streamState = { assistantKey: '', assistantEl: null };
        assistantByItem.clear();
        toolByItem.clear();
        toolCommandByItem.clear();
        streamedAssistantItems.clear();
      }
      appendLine('warn', '[hazel] connected to codex app-server');
      setMeta('Active' + (task ? ' [' + task + ']' : ''));
      await pollOnce();
      ensurePolling();
      const url = new URL(window.location.href);
      if (task) url.searchParams.set('task', task); else url.searchParams.delete('task');
      history.replaceState({}, '', url.toString());
    }

    async function pollOnce() {
      if (!sessionID) return;
      const qp = new URLSearchParams({ session_id: sessionID, cursor: String(cursor) });
      const res = await fetch('/api/codex/session/poll?' + qp.toString());
      if (!res.ok) return;
      const js = await res.json();
      cursor = js.cursor || cursor;
      for (const ev of (js.events || [])) renderEvent(ev);
      renderApprovals(js.approvals || []);
      if (js.done) {
        setMeta('Done' + (js.exit_code !== undefined && js.exit_code !== null ? ' (exit ' + js.exit_code + ')' : ''));
      }
    }

    function ensurePolling() {
      if (pollTimer) return;
      pollTimer = setInterval(() => { pollOnce().catch(() => {}); }, 250);
    }

    async function submitApproval(requestID, decision) {
      let rid = (requestID || '').trim();
      if (!sessionID) {
        const task = document.getElementById('hzTask').value;
        if (!task) {
          appendLine('warn', '[approval] request is no longer pending');
          return;
        }
        await startSession(false, true);
      }
      if (!rid) {
        const qp = new URLSearchParams({ session_id: sessionID, cursor: String(cursor) });
        const pollRes = await fetch('/api/codex/session/poll?' + qp.toString());
        if (pollRes.ok) {
          const js = await pollRes.json();
          const list = js && js.approvals ? js.approvals : [];
          if (list.length === 1 && list[0] && list[0].request_id) {
            rid = String(list[0].request_id);
          } else if (list.length > 1 && list[0] && list[0].request_id) {
            rid = String(list[0].request_id);
            appendLine('warn', '[approval] multiple pending approvals; applied decision to the most recent request');
          }
        }
      }
      if (!rid) {
        appendLine('warn', '[approval] request is no longer pending');
        return;
      }
      const body = new URLSearchParams({ session_id: sessionID, request_id: rid, decision });
      const res = await fetch('/api/codex/approval', { method:'POST', body });
      if (!res.ok) {
        const msg = await res.text();
        if ((msg || '').toLowerCase().includes('approval request not found')) {
          appendLine('warn', '[approval] request is no longer pending');
        } else {
          appendLine('error', '[approval] ' + msg);
        }
      }
      await pollOnce();
    }

    document.getElementById('hzTask').addEventListener('change', (e) => {
      const task = e.target && e.target.value ? e.target.value : '';
      const u = new URL(window.location.href);
      if (task) u.searchParams.set('task', task); else u.searchParams.delete('task');
      u.searchParams.delete('autorun');
      u.searchParams.delete('session');
      window.location.href = u.toString();
    });

    document.getElementById('hzComposer').addEventListener('submit', async (e) => {
      e.preventDefault();
      const promptEl = document.getElementById('hzPrompt');
      const prompt = promptEl.value.trim();
      await sendPrompt(prompt);
      promptEl.value = '';
      promptEl.focus();
    });
    document.getElementById('hzPrompt').addEventListener('keydown', (e) => {
      if (e.key !== 'Enter') return;
      if (e.shiftKey) return;
      e.preventDefault();
      document.getElementById('hzComposer').requestSubmit();
    });

    async function sendPrompt(prompt) {
      if (!prompt || !prompt.trim()) return;
      if (!sessionID) {
        await startSession(false);
      }
      const body = new URLSearchParams({ session_id: sessionID, prompt: prompt.trim() });
      const res = await fetch('/api/codex/turn', { method:'POST', body });
      if (!res.ok) {
        appendLine('error', '[turn] ' + await res.text());
      }
    }

    async function runInChatSelectedTask() {
      const task = document.getElementById('hzTask').value;
      if (!task) return;
      const projectEl = document.getElementById('hzProject');
      const project = projectEl ? projectEl.value : '';
      if (project) {
        const statusBody = new URLSearchParams({ id: task, status: 'ACTIVE', project });
        const statusRes = await fetch('/mutate/status', {
          method: 'POST',
          body: statusBody,
          headers: { 'X-Hazel-Ajax': '1' }
        });
        if (!statusRes.ok) throw new Error(await statusRes.text());
      }
      const prompt = 'Run this task end-to-end now: ' + task + '. Start by restating scope and acceptance criteria from Hazel context, then implement and verify.';
      await startSession(false);
      await sendPrompt(prompt);
      appendLine('warn', '[hazel] auto-ran task ' + task + ' in chat');
    }

    if (!sessionID && initialEvents && initialEvents.length) {
      for (const ev of initialEvents) renderEvent(ev);
      setMeta('History');
    }
    if (sessionID) {
      setMeta('Reattached');
      ensurePolling();
    }
    if ({{if .AutoRun}}true{{else}}false{{end}} && !autoRan) {
      autoRan = true;
      runInChatSelectedTask().catch((e) => appendLine('error', '[autorun] ' + (e && e.message ? e.message : e)));
    }
  </script>
</body>
</html>`
