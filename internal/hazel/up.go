package hazel

import (
	"context"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

type UpOptions struct {
	PortOverride int
}

func Up(ctx context.Context, root string, opt UpOptions) (addr string, err error) {
	var cfg Config
	if err := readYAMLFile(configPath(root), &cfg); err != nil {
		return "", err
	}
	if cfg.Version == 0 {
		cfg = defaultConfig()
	}
	port := cfg.Port
	if opt.PortOverride != 0 {
		port = opt.PortOverride
	}
	if port == 0 {
		port = 8765
	}

	repoSlug := readRepoSlugFromGitConfig(root)
	title := projectTitle(root)
	if repoSlug != "" {
		title = repoSlug
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { uiBoard(w, r, root, cfg, title, repoSlug) })
	mux.HandleFunc("/task/", func(w http.ResponseWriter, r *http.Request) { uiTask(w, r, root, cfg, title, repoSlug) })
	mux.HandleFunc("/mutate/status", func(w http.ResponseWriter, r *http.Request) { uiMutateStatus(w, r, root) })
	mux.HandleFunc("/mutate/priority", func(w http.ResponseWriter, r *http.Request) { uiMutatePriority(w, r, root) })
	mux.HandleFunc("/mutate/new_task", func(w http.ResponseWriter, r *http.Request) { uiMutateNewTask(w, r, root) })
	mux.HandleFunc("/mutate/task_md", func(w http.ResponseWriter, r *http.Request) { uiMutateTaskMD(w, r, root) })
	mux.HandleFunc("/mutate/task_color", func(w http.ResponseWriter, r *http.Request) { uiMutateTaskColor(w, r, root) })
	mux.HandleFunc("/mutate/plan", func(w http.ResponseWriter, r *http.Request) { uiMutatePlan(w, r, root) })
	mux.HandleFunc("/mutate/interval", func(w http.ResponseWriter, r *http.Request) { uiMutateInterval(w, r, root) })
	mux.HandleFunc("/mutate/run", func(w http.ResponseWriter, r *http.Request) { uiMutateRun(w, r, root) })

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", err
	}
	addr = ln.Addr().String()

	// Refuse to start if an existing server is already running.
	if st, err := readServerState(root); err == nil && pidAlive(st.PID) {
		_ = ln.Close()
		return "", fmt.Errorf("server already running (pid %d) on %s", st.PID, st.Addr)
	}
	// Write state for `hazel down`.
	_ = clearServerState(root)
	if err := writeServerState(root, &ServerState{PID: os.Getpid(), Addr: addr, StartedAt: time.Now()}); err != nil {
		_ = ln.Close()
		return "", err
	}

	// Always run the scheduler loop; it is a no-op unless enabled in config.
	go schedulerLoop(ctx, root)

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
		_ = clearServerState(root)
	}()
	go func() { _ = server.Serve(ln) }()
	return addr, nil
}

func agentUI(cfg Config) (name string, tooltip string) {
	ac := strings.TrimSpace(cfg.AgentCommand)
	if ac == "" {
		return "none", ""
	}
	// Our init wizard writes `.hazel/agent.sh` for Codex.
	if strings.Contains(ac, ".hazel/agent.sh") {
		return "codex", ac
	}
	// Heuristic: if the command begins with `codex`, call it codex.
	if fields := strings.Fields(ac); len(fields) > 0 {
		if strings.EqualFold(fields[0], "codex") {
			return "codex", ac
		}
	}
	return "custom", ac
}

func schedulerLoop(ctx context.Context, root string) {
	for {
		// Re-read config each tick so UI changes take effect without restart.
		var cfg Config
		if err := readYAMLFile(configPath(root), &cfg); err != nil || cfg.Version == 0 {
			cfg = defaultConfig()
		}

		enabled := cfg.SchedulerEnabled && cfg.RunIntervalSeconds > 0
		wait := 2 * time.Second
		if enabled {
			wait = time.Duration(cfg.RunIntervalSeconds) * time.Second
			if wait < 5*time.Second {
				wait = 5 * time.Second
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if enabled {
				_, _ = RunTick(ctx, root, RunOptions{})
			}
		}
	}
}

func uiBoard(w http.ResponseWriter, r *http.Request, root string, cfg Config, title string, repoSlug string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Refresh config per request (allows UI changes to config.yaml to show without restart).
	latest, _ := loadConfigOrDefault(root)
	cfg = latest

	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	all := []Status{StatusBacklog, StatusReady, StatusActive, StatusReview, StatusDone}
	visible := parseVisibleColumns(r, cfg, all)
	visibleSet := map[Status]bool{}
	for _, s := range visible {
		visibleSet[s] = true
	}

	type UICard struct {
		Task          *BoardTask
		ColorHex      string
		PriorityLabel string
		RingHex       string
	}
	cols := map[Status][]UICard{}
	for _, t := range b.Tasks {
		if !visibleSet[t.Status] {
			continue
		}
		colorKey := defaultColorKeyForID(t.ID)
		if md, err := readTaskMD(root, t.ID); err == nil {
			if k, ok := getTaskColorFromMD(md); ok {
				colorKey = k
			}
		}
		lbl := ""
		if md, err := readTaskMD(root, t.ID); err == nil {
			if p, ok := getTaskPriorityFromMD(md); ok {
				lbl = p
			}
		}
		cols[t.Status] = append(cols[t.Status], UICard{
			Task:          t,
			ColorHex:      colorHexForKey(colorKey),
			PriorityLabel: lbl,
			RingHex:       ringHexForPriorityLabel(lbl),
		})
	}

	agentName, agentTip := agentUI(cfg)

	tpl := template.Must(template.New("board").Funcs(template.FuncMap{
		"intp": func(p *int) string { // legacy; kept for template compatibility if extended later
			if p == nil {
				return ""
			}
			return fmtInt(*p)
		},
	}).Parse(uiBoardHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tpl.Execute(w, map[string]any{
		"Columns":     cols,
		"Order":       visible,
		"AllStatuses": all,
		"VisibleSet":  visibleSet,
		"ColCount":    len(visible),
		"SchedulerOn": cfg.SchedulerEnabled,
		"IntervalSec": cfg.RunIntervalSeconds,
		"Title":       title,
		"RepoSlug":    repoSlug,
		"HazelURL":    hazelPoweredByURL(),
		"AgentName":   agentName,
		"AgentTip":    agentTip,
	})
}

func uiTask(w http.ResponseWriter, r *http.Request, root string, cfg Config, title string, repoSlug string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	latest, _ := loadConfigOrDefault(root)
	cfg = latest
	id := strings.TrimPrefix(r.URL.Path, "/task/")
	id = strings.Trim(id, "/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var task *BoardTask
	for _, t := range b.Tasks {
		if t.ID == id {
			task = t
			break
		}
	}
	if task == nil {
		http.NotFound(w, r)
		return
	}

	read := func(name string) string {
		p := taskFile(root, id, name)
		b, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		return string(b)
	}

	tpl := template.Must(template.New("task").Parse(uiTaskHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	taskMD := read("task.md")
	colorKey := defaultColorKeyForID(task.ID)
	if k, ok := getTaskColorFromMD(taskMD); ok {
		colorKey = k
	}
	renderMD := func(src string) template.HTML {
		md := goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			// Don't allow raw HTML passthrough in UI (default behavior).
			goldmark.WithRendererOptions(),
		)
		var sb strings.Builder
		if err := md.Convert([]byte(src), &sb); err != nil {
			return ""
		}
		return template.HTML(sb.String())
	}

	renderTask := taskMD
	if stripped, err := stripTaskConfigForRender(taskMD); err == nil {
		renderTask = stripped
	}

	implMD := read("impl.md")

	priority := ""
	if p, ok := getTaskPriorityFromMD(taskMD); ok {
		priority = p
	}
	agentName, agentTip := agentUI(cfg)
	_ = tpl.Execute(w, map[string]any{
		"Task":      task,
		"TaskMD":    taskMD,
		"TaskHTML":  renderMD(renderTask),
		"ImplHTML":  renderMD(implMD),
		"ImplMD":    implMD,
		"ColorKey":  colorKey,
		"Palette":   pastelPalette,
		"RingHex":   ringHexForPriorityLabel(priority),
		"Priority":  priority,
		"AllPrios":  []string{"", "HIGH", "MEDIUM", "LOW"},
		"Title":     title,
		"RepoSlug":  repoSlug,
		"HazelURL":  hazelPoweredByURL(),
		"AgentName": agentName,
		"AgentTip":  agentTip,
	})
}

func loadConfigOrDefault(root string) (Config, error) {
	var cfg Config
	if err := readYAMLFile(configPath(root), &cfg); err != nil {
		return defaultConfig(), err
	}
	if cfg.Version == 0 {
		cfg = defaultConfig()
	}
	return cfg, nil
}

func uiMutateStatus(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	status := Status(strings.TrimSpace(r.FormValue("status")))
	if id == "" || !status.Valid() {
		http.Error(w, "invalid id or status", http.StatusBadRequest)
		return
	}

	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	for _, t := range b.Tasks {
		if t.ID == id {
			t.Status = status
			t.UpdatedAt = now
			break
		}
	}
	if err := writeYAMLFile(boardPath(root), &b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Header.Get("X-Hazel-Ajax") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func uiMutatePriority(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	lbl := strings.TrimSpace(r.FormValue("priority"))
	if id == "" {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	lbl = strings.ToUpper(strings.TrimSpace(lbl))
	if lbl != "" && lbl != "HIGH" && lbl != "MEDIUM" && lbl != "LOW" {
		http.Error(w, "invalid priority", http.StatusBadRequest)
		return
	}

	md, err := readTaskMD(root, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := setTaskPriorityInMD(md, lbl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeTaskMD(root, id, updated); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = bumpBoardUpdatedAt(root, id)
	if r.Header.Get("X-Hazel-Ajax") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func uiMutateInterval(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	enabled := strings.TrimSpace(r.FormValue("enabled")) != ""
	raw := strings.TrimSpace(r.FormValue("interval"))

	cfg, _ := loadConfigOrDefault(root)
	cfg.SchedulerEnabled = enabled
	if enabled {
		sec, err := strconv.Atoi(raw)
		if err != nil || sec < 5 {
			http.Error(w, "interval must be an integer >= 5 seconds", http.StatusBadRequest)
			return
		}
		cfg.RunIntervalSeconds = sec
	}
	if err := writeYAMLFile(configPath(root), &cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Header.Get("X-Hazel-Ajax") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func uiMutateRun(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Preflight: if agent is unconfigured, fail fast rather than silently doing nothing.
	cfg, cfgErr := loadConfigOrDefault(root)
	if cfgErr == nil && cfg.Version == 0 {
		cfg = defaultConfig()
	}
	if strings.TrimSpace(cfg.AgentCommand) == "" {
		http.Error(w, "agent_command is not configured in .hazel/config.yaml", http.StatusBadRequest)
		return
	}

	// Run asynchronously so the UI doesn't hang for long agent runs.
	go func() {
		res, err := RunTick(context.Background(), root, RunOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "hazel: run tick error: %v\n", err)
			return
		}
		if res != nil && res.DispatchedTaskID != "" {
			fmt.Fprintf(os.Stderr, "hazel: dispatched %s\n", res.DispatchedTaskID)
		}
	}()

	if r.Header.Get("X-Hazel-Ajax") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func uiMutateNewTask(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	t, err := createNewTask(root, title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/task/"+t.ID, http.StatusSeeOther)
}

func uiMutateTaskMD(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	content := r.FormValue("content")
	if id == "" {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := ensureTaskScaffold(root, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeTaskMD(root, id, content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Update board updated_at for visibility in tooling.
	_ = bumpBoardUpdatedAt(root, id)
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func uiMutateTaskColor(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	color := strings.TrimSpace(r.FormValue("color"))
	if id == "" {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if !validColorKey(color) {
		http.Error(w, "invalid color", http.StatusBadRequest)
		return
	}
	md, err := readTaskMD(root, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := setTaskColorInMD(md, color)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeTaskMD(root, id, updated); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/task/"+id, http.StatusSeeOther)
}

func uiMutatePlan(w http.ResponseWriter, r *http.Request, root string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	// Run the same workflow as `hazel plan`, but from the UI.
	// Do it asynchronously so the request doesn't hang for long-running agents.
	go func() { _, _ = Plan(context.Background(), root, id) }()

	if r.Header.Get("X-Hazel-Ajax") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/task/"+id+"?plan=started", http.StatusSeeOther)
}

func bumpBoardUpdatedAt(root, id string) error {
	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		return err
	}
	now := time.Now()
	for _, t := range b.Tasks {
		if t.ID == id {
			t.UpdatedAt = now
			break
		}
	}
	return writeYAMLFile(boardPath(root), &b)
}

func validColorKey(key string) bool {
	for _, p := range pastelPalette {
		if p.Key == key {
			return true
		}
	}
	return false
}

func ringHexForPriorityLabel(lbl string) string {
	switch strings.ToUpper(lbl) {
	case "HIGH":
		return "#ff6b6b"
	case "MEDIUM":
		return "#ffcc66"
	case "LOW":
		return "#86f7c5"
	default:
		return "rgba(255,255,255,0)"
	}
}

func parseVisibleColumns(r *http.Request, cfg Config, all []Status) []Status {
	raw := r.URL.Query()["col"]
	set := map[Status]bool{}
	for _, v := range raw {
		s := Status(strings.TrimSpace(v))
		if s.Valid() {
			set[s] = true
		}
	}
	if len(set) == 0 {
		// Default: hide DONE if configured, otherwise show all.
		for _, s := range all {
			if cfg.UIHideDoneByDefault && s == StatusDone {
				continue
			}
			set[s] = true
		}
	}
	var out []Status
	for _, s := range all {
		if set[s] {
			out = append(out, s)
		}
	}
	return out
}

const uiBoardHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}}</title>
  <style>
    :root { --bg:#0b1020; --panel:#101a33; --card:#0f1830; --text:#e9eefc; --muted:#aab4d6; --accent:#86f7c5; --danger:#ff6b6b; }
    * { box-sizing:border-box; }
    body { margin:0; font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, Arial; background: radial-gradient(1200px 600px at 20% -10%, rgba(134,247,197,.18) 0%, rgba(20,33,74,.55) 35%, var(--bg) 70%); color:var(--text); }
    header { padding:14px 20px; border-bottom:1px solid rgba(255,255,255,.08); background: rgba(16,26,51,.55); backdrop-filter: blur(10px); position: sticky; top:0; z-index: 10; }
    header .row { display:flex; justify-content:space-between; align-items:center; gap:12px; }
    h1 { margin:0; font-size:15px; letter-spacing:.06em; text-transform:uppercase; color: rgba(233,238,252,.82); }
    .actions { display:flex; gap:10px; align-items:center; flex-wrap:wrap; font-size:12px; }
    .actions a { color:var(--accent); text-decoration:none; border:1px solid rgba(134,247,197,.35); padding:6px 10px; border-radius:999px; }
    .actions form { display:flex; gap:8px; align-items:center; margin:0; }
    .actions input[type="text"]{ width: 280px; max-width: 45vw; }
    details.cols { position: relative; }
    details.cols > summary { list-style:none; cursor:pointer; user-select:none; border:1px solid rgba(255,255,255,.14); padding:6px 10px; border-radius:999px; color: rgba(233,238,252,.9); background: rgba(255,255,255,.04); }
    details.cols[open] > summary { border-color: rgba(134,247,197,.35); box-shadow: 0 0 0 3px rgba(134,247,197,.08); }
    /* Override the header form flex rule so the checklist is vertical. */
    .actions form.colmenu { display:flex; flex-direction:column; align-items:stretch; gap:0; }
    .colmenu { position:absolute; right:0; top: 38px; z-index:50; width: 240px; max-width: calc(100vw - 48px); border-radius:12px; border:1px solid rgba(255,255,255,.12); background: rgba(16,26,51,.96); padding:10px 10px; box-shadow: 0 18px 50px rgba(0,0,0,.45); }
    .colmenu { max-height: min(60vh, 360px); overflow:auto; }
    .colmenu label { display:flex; justify-content:space-between; gap:10px; padding:6px 6px; border-radius:10px; }
    .colmenu label:hover { background: rgba(255,255,255,.04); }
    .colmenu input { transform: translateY(1px); }
    .colmenu button { width:100%; margin-top:8px; }
    .colmenu .row { display:flex; gap:8px; align-items:center; }
    .colmenu .row input[type="number"] { width: 96px; }
    .pilltop { display:inline-flex; gap:10px; align-items:center; border:1px solid rgba(255,255,255,.10); background: rgba(255,255,255,.04); padding:7px 10px; border-radius: 999px; color: rgba(233,238,252,.78); }
    .dot { width:8px; height:8px; border-radius:999px; background: rgba(134,247,197,.9); box-shadow: 0 0 0 4px rgba(134,247,197,.12); }
    main { padding:16px 16px 20px; }
    .board { display:grid; gap:12px; grid-template-columns: repeat(var(--cols), minmax(250px, 1fr)); overflow-x:auto; padding-bottom:12px; }
    .col { background: rgba(16,26,51,.78); border:1px solid rgba(255,255,255,.08); border-radius:14px; padding:10px; min-height: 70vh; }
    .col h2 { margin:4px 6px 10px; font-size:12px; letter-spacing:.12em; text-transform:uppercase; color: rgba(233,238,252,.62); display:flex; justify-content:space-between; }
    .card { padding:12px 12px; margin:8px 4px; border-radius:12px; border:1px solid rgba(255,255,255,.10); background: rgba(15,24,48,.92); }
    .id a { color: rgba(233,238,252,.85); text-decoration:none; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size:12px; }
    .title { margin-top:8px; font-size:13px; line-height:1.35; color: rgba(233,238,252,.92); }
    .meta { margin-top:10px; display:flex; gap:8px; align-items:center; flex-wrap:wrap; }
    .meta form { margin:0; display:flex; gap:6px; align-items:center; }
    select, input { background: rgba(255,255,255,.06); border:1px solid rgba(255,255,255,.12); color: var(--text); padding:7px 9px; border-radius:10px; font-size:12px; }
    button { background: rgba(134,247,197,.12); border:1px solid rgba(134,247,197,.35); color: var(--text); padding:6px 8px; border-radius:8px; font-size:12px; cursor:pointer; }
    button:hover { box-shadow: 0 0 0 3px rgba(134,247,197,.08); }
    .hint { margin-top:10px; color: rgba(233,238,252,.50); font-size:12px; }
    .card.dragging { opacity: .6; }
    .card { background: linear-gradient(180deg, rgba(0,0,0,.62), rgba(0,0,0,.62)), var(--cardbg, rgba(15,24,48,.92)); box-shadow: inset 0 0 0 3px var(--ring, rgba(255,255,255,0)); }
    .col.dropover { border-color: rgba(134,247,197,.45); box-shadow: 0 0 0 3px rgba(134,247,197,.10); }
    footer { padding: 12px 16px 18px; color: rgba(233,238,252,.50); font-size:12px; }
    footer a { color: rgba(134,247,197,.85); text-decoration:none; }
    footer a:hover { text-decoration: underline; }
  </style>
</head>
<body>
  <header>
    <div class="row">
      <div>
        <h1>{{.Title}}</h1>
      </div>
      <div class="actions">
        <form action="/mutate/new_task" method="post">
          <input type="text" name="title" placeholder="New backlog item title" required />
          <button type="submit">Create</button>
        </form>
        <form action="/mutate/run" method="post" onsubmit="return hazelRunTick(this)">
          <button type="submit">Run tick</button>
        </form>
        <span class="pilltop" {{if .AgentTip}}title="{{.AgentTip}}"{{end}}>Agent: {{.AgentName}}</span>
        {{if and .SchedulerOn (gt .IntervalSec 0)}}
          <span class="pilltop"><span class="dot"></span>Next tick <span id="hzNextTick">{{.IntervalSec}}</span>s</span>
        {{end}}
        <details class="cols">
          <summary>Schedule{{if not .SchedulerOn}} (off){{end}}</summary>
          <form class="colmenu" action="/mutate/interval" method="post">
            <label>
              <span>Enabled</span>
              <input type="checkbox" name="enabled" value="1" {{if .SchedulerOn}}checked{{end}} />
            </label>
            <div class="hint" style="margin: 2px 6px 8px;">Tick interval (seconds)</div>
            <div class="row" style="padding: 0 6px 6px;">
              <input type="number" name="interval" min="5" step="1" value="{{.IntervalSec}}" />
              <button type="submit">Save</button>
            </div>
            <div class="hint" style="margin: 0 6px 2px;">Applies without restarting.</div>
          </form>
        </details>
        <details class="cols">
          <summary>Choose columns</summary>
          <form class="colmenu" action="/" method="get">
            {{range .AllStatuses}}
              <label>
                <span>{{.}}</span>
                <input type="checkbox" name="col" value="{{.}}" {{if index $.VisibleSet .}}checked{{end}} />
              </label>
            {{end}}
            <button type="submit">Apply</button>
          </form>
        </details>
      </div>
    </div>
    <div class="hint">Board edits update board.yaml. task.md edits only happen when you explicitly save.</div>
  </header>
  <main>
    <div class="board" style="--cols: {{.ColCount}};">
      {{range .Order}}
        {{$status := .}}
        {{$tasks := index $.Columns $status}}
        <section class="col dropzone" data-status="{{$status}}">
          <h2><span>{{$status}}</span><span>{{len $tasks}}</span></h2>
          {{range $tasks}}
            <div class="card" draggable="true" data-id="{{.Task.ID}}" style="--cardbg: {{.ColorHex}}; --ring: {{.RingHex}};">
              <div class="id"><a href="/task/{{.Task.ID}}">{{.Task.ID}}</a></div>
              <div class="title">{{.Task.Title}}</div>
              <div class="meta">
                <form action="/mutate/status" method="post">
                  <input type="hidden" name="id" value="{{.Task.ID}}" />
                  <select name="status" onchange="hazelSubmit(this.form)">
                    <option {{if eq .Task.Status "BACKLOG"}}selected{{end}}>BACKLOG</option>
                    <option {{if eq .Task.Status "READY"}}selected{{end}}>READY</option>
                    <option {{if eq .Task.Status "ACTIVE"}}selected{{end}}>ACTIVE</option>
                    <option {{if eq .Task.Status "REVIEW"}}selected{{end}}>REVIEW</option>
                    <option {{if eq .Task.Status "DONE"}}selected{{end}}>DONE</option>
                  </select>
                </form>
                <form action="/mutate/priority" method="post">
                  <input type="hidden" name="id" value="{{.Task.ID}}" />
                  <select name="priority" onchange="hazelSubmit(this.form)">
                    <option value="" {{if eq .PriorityLabel ""}}selected{{end}}>Priority</option>
                    <option value="LOW" {{if eq .PriorityLabel "LOW"}}selected{{end}}>LOW</option>
                    <option value="MEDIUM" {{if eq .PriorityLabel "MEDIUM"}}selected{{end}}>MEDIUM</option>
                    <option value="HIGH" {{if eq .PriorityLabel "HIGH"}}selected{{end}}>HIGH</option>
                  </select>
                </form>
              </div>
            </div>
          {{end}}
        </section>
      {{end}}
    </div>
  </main>
  <footer>
    <span>Powered by <a href="{{.HazelURL}}">Hazel</a></span>
  </footer>
  <script>
    function hazelSubmit(form) {
      const fd = new FormData(form);
      fetch(form.action, { method: "POST", body: new URLSearchParams(fd), headers: { "X-Hazel-Ajax": "1" } })
        .then(() => location.reload());
    }

    function hazelRunTick(form) {
      const btn = form.querySelector("button[type='submit']");
      if (btn) { btn.disabled = true; btn.textContent = "Running..."; }
      const fd = new FormData(form);
      fetch(form.action, { method: "POST", body: new URLSearchParams(fd), headers: { "X-Hazel-Ajax": "1" } })
        .then(() => setTimeout(() => location.reload(), 1500))
        .catch(() => { if (btn) { btn.disabled = false; btn.textContent = "Run tick"; }});
      return false;
    }

    let dragID = "";
    document.querySelectorAll(".card[draggable='true']").forEach((el) => {
      el.addEventListener("dragstart", (e) => {
        dragID = el.getAttribute("data-id") || "";
        el.classList.add("dragging");
        if (e.dataTransfer) {
          e.dataTransfer.setData("text/plain", dragID);
          e.dataTransfer.effectAllowed = "move";
        }
      });
      el.addEventListener("dragend", () => {
        el.classList.remove("dragging");
        dragID = "";
      });
    });
    document.querySelectorAll(".dropzone").forEach((col) => {
      col.addEventListener("dragover", (e) => {
        e.preventDefault();
        col.classList.add("dropover");
      });
      col.addEventListener("dragleave", () => col.classList.remove("dropover"));
      col.addEventListener("drop", (e) => {
        e.preventDefault();
        col.classList.remove("dropover");
        const id = (e.dataTransfer && e.dataTransfer.getData("text/plain")) || dragID;
        const status = col.getAttribute("data-status") || "";
        if (!id || !status) return;
        const body = new URLSearchParams({ id, status });
        fetch("/mutate/status", { method: "POST", body, headers: { "X-Hazel-Ajax": "1" } })
          .then(() => location.reload());
      });
    });

    (function(){
      const el = document.getElementById("hzNextTick");
      if (!el) return;
      const interval = parseInt(el.textContent || "0", 10);
      if (!interval || interval <= 0) return;
      let remaining = interval;
      setInterval(() => {
        remaining--;
        if (remaining <= 0) remaining = interval;
        el.textContent = String(remaining);
      }, 1000);
    })();
  </script>
</body>
</html>`

const uiTaskHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}} - {{.Task.ID}}</title>
  <style>
    :root { --bg:#0b1020; --panel:#101a33; --text:#e9eefc; --muted:#aab4d6; --link:#86f7c5; }
    * { box-sizing:border-box; }
    body { margin:0; font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, Arial; background: radial-gradient(1200px 600px at 20% -10%, rgba(134,247,197,.18) 0%, rgba(20,33,74,.55) 35%, var(--bg) 70%); color:var(--text); }
    header { padding:14px 20px; border-bottom:1px solid rgba(255,255,255,.08); background: rgba(16,26,51,.55); backdrop-filter: blur(10px); position: sticky; top:0; z-index: 10; }
    header a { color: var(--link); text-decoration:none; font-size:12px; }
    h1 { margin:6px 0 0; font-size:18px; }
    .meta { margin-top:6px; color: rgba(233,238,252,.6); font-size:12px; }
    main { padding: 18px 18px 28px; max-width: 1180px; margin:0 auto; }
    .panel { background: rgba(16,26,51,.85); border:1px solid rgba(255,255,255,.08); border-radius:12px; padding:14px 16px; margin-bottom:14px; }
    .panel h2 { margin:0 0 10px; font-size:12px; letter-spacing:.12em; text-transform:uppercase; color: var(--muted); }
    textarea { width:100%; min-height: 220px; background: rgba(255,255,255,.04); border:1px solid rgba(255,255,255,.10); color: var(--text); border-radius:10px; padding:10px 12px; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size:12px; }
    .row { display:flex; gap:10px; align-items:center; flex-wrap:wrap; margin-top:10px; }
    .row form { margin:0; display:flex; gap:8px; align-items:center; }
    .pill { border:1px solid rgba(255,255,255,.12); padding:2px 8px; border-radius:999px; font-size:12px; background: rgba(255,255,255,.04); }
    .md { padding: 10px 12px; border-radius:10px; border:1px solid rgba(255,255,255,.10); background: rgba(255,255,255,.03); }
    .md :is(h1,h2,h3){ margin-top: 14px; }
    .md a { color: var(--link); }
    .md code { background: rgba(255,255,255,.08); padding: 1px 5px; border-radius:6px; }
    .md pre { background: rgba(255,255,255,.06); padding: 10px 12px; border-radius:10px; overflow:auto; }
    .topbar { display:flex; align-items:center; justify-content:space-between; gap:12px; }
    .backbtn { display:inline-flex; gap:10px; align-items:center; border:1px solid rgba(255,255,255,.12); background: rgba(255,255,255,.04); padding:8px 10px; border-radius:999px; }
    .backbtn:hover { border-color: rgba(134,247,197,.35); box-shadow: 0 0 0 3px rgba(134,247,197,.08); }
    .split { display:grid; grid-template-columns: 1fr; gap:12px; }
    @media (min-width: 980px) { .split { grid-template-columns: 1fr 1fr; } }
    .editbar { display:flex; justify-content:space-between; align-items:center; gap:10px; flex-wrap:wrap; margin: 10px 0 8px; }
    .ghost { background: rgba(255,255,255,.04); border:1px solid rgba(255,255,255,.12); color: var(--text); padding:8px 10px; border-radius:10px; cursor:pointer; }
    .ghost:hover { box-shadow: 0 0 0 3px rgba(134,247,197,.08); }
    .editor { display:none; margin-top:10px; }
    .editor.on { display:block; }
    footer { padding: 12px 16px 18px; color: rgba(233,238,252,.50); font-size:12px; }
    footer a { color: rgba(134,247,197,.85); text-decoration:none; }
    footer a:hover { text-decoration: underline; }
  </style>
</head>
<body>
  <header>
    <div class="topbar">
      <a class="backbtn" href="/">Back to board</a>
      <div class="meta"><span {{if .AgentTip}}title="{{.AgentTip}}"{{end}}>Agent: {{.AgentName}}</span> | Status: {{.Task.Status}} | Updated: {{.Task.UpdatedAt}}</div>
    </div>
    <h1>{{.Task.ID}}: {{.Task.Title}}</h1>
  </header>
  <main>
    <div class="split">
      <section class="panel">
        <h2>Task</h2>
	        <div class="editbar">
	          <span class="pill" style="box-shadow: inset 0 0 0 3px {{.RingHex}};">Priority: {{if .Priority}}{{.Priority}}{{else}}Unset{{end}}</span>
	          <div class="row">
	            <form action="/mutate/plan" method="post" onsubmit="return hazelPlan(this)">
	              <input type="hidden" name="id" value="{{.Task.ID}}" />
	              <button class="ghost" type="submit">Plan</button>
	            </form>
	            <button class="ghost" type="button" onclick="hazelToggleEdit()">Edit</button>
	          </div>
	        </div>
        <div class="md">{{.TaskHTML}}</div>
        <div id="hzEditor" class="editor">
          <form action="/mutate/task_md" method="post">
            <input type="hidden" name="id" value="{{.Task.ID}}" />
            <textarea name="content">{{.TaskMD}}</textarea>
            <div class="row">
              <button type="submit">Save task.md</button>
              <button class="ghost" type="button" onclick="hazelToggleEdit()">Cancel</button>
            </div>
          </form>
        </div>
      </section>
      <section class="panel">
        <h2>Impl</h2>
        <div class="md">{{.ImplHTML}}</div>
      </section>
    </div>
  </main>
  <footer>
    <span>Powered by <a href="{{.HazelURL}}">Hazel</a></span>
  </footer>
  <script>
    function hazelToggleEdit() {
      const el = document.getElementById("hzEditor");
      if (!el) return;
      el.classList.toggle("on");
      if (el.classList.contains("on")) {
        const ta = el.querySelector("textarea");
        if (ta) ta.focus();
      }
    }

    function hazelPlan(form) {
      const btn = form.querySelector("button[type='submit']");
      if (btn) { btn.disabled = true; btn.textContent = "Planning..."; }
      const fd = new FormData(form);
      fetch(form.action, { method: "POST", body: new URLSearchParams(fd), headers: { "X-Hazel-Ajax": "1" } })
        .then(() => setTimeout(() => location.reload(), 1500))
        .catch(() => { if (btn) { btn.disabled = false; btn.textContent = "Plan"; }});
      return false;
    }
  </script>
</body>
</html>`
