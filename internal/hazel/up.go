package hazel

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	loadNexus := func() (*Nexus, error) {
		nx, err := LoadNexus(root)
		if err != nil {
			return nil, err
		}
		if nx == nil {
			return nil, fmt.Errorf("nexus mode required: projects_root_dir must be configured")
		}
		return nx, nil
	}
	if _, err := loadNexus(); err != nil {
		return "", err
	}
	title = "Hazel Nexus"
	repoSlug = ""

	withNexus := func(fn func(http.ResponseWriter, *http.Request, *Nexus)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			nx, err := loadNexus()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fn(w, r, nx)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiBoard(w, r, root, cfg, title, repoSlug, nx) }))
	mux.HandleFunc("/task/", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiTask(w, r, root, cfg, title, repoSlug, nx) }))
	mux.HandleFunc("/panel/board", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiNexusBoardPanel(w, r, root, cfg, nx) }))
	mux.HandleFunc("/chat", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiChat(w, r, root, nx) }))
	mux.HandleFunc("/wiki", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiWiki(w, r, root, nx) }))
	mux.HandleFunc("/mutate/status", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateStatus(w, r, root, nx) }))
	mux.HandleFunc("/mutate/priority", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutatePriority(w, r, root, nx) }))
	mux.HandleFunc("/mutate/new_task", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateNewTask(w, r, root, nx) }))
	mux.HandleFunc("/mutate/task_md", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateTaskMD(w, r, root, nx) }))
	mux.HandleFunc("/mutate/task_color", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateTaskColor(w, r, root, nx) }))
	mux.HandleFunc("/mutate/plan", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutatePlan(w, r, root, nx) }))
	mux.HandleFunc("/mutate/plan_decision", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutatePlanDecision(w, r, root, nx) }))
	mux.HandleFunc("/mutate/interval", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateInterval(w, r, root, nx) }))
	mux.HandleFunc("/mutate/run", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateRun(w, r, root, nx) }))
	mux.HandleFunc("/mutate/git/start", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateGitStart(w, r, root, nx) }))
	mux.HandleFunc("/mutate/git/commit", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateGitCommit(w, r, root, nx) }))
	mux.HandleFunc("/mutate/git/pr", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateGitPR(w, r, root, nx) }))
	mux.HandleFunc("/mutate/git/merge", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateGitMerge(w, r, root, nx) }))
	mux.HandleFunc("/mutate/config", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiMutateConfig(w, r, root, nx) }))
	mux.HandleFunc("/history", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiRuns(w, r, root, title, repoSlug, nx) }))
	mux.HandleFunc("/history/", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiRunView(w, r, root, title, repoSlug, nx) }))
	mux.HandleFunc("/runs", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiRuns(w, r, root, title, repoSlug, nx) }))
	mux.HandleFunc("/runs/", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { uiRunView(w, r, root, title, repoSlug, nx) }))
	mux.HandleFunc("/api/run_state", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { apiRunState(w, r, root, nx) }))
	mux.HandleFunc("/api/run_tail", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { apiRunTail(w, r, root, nx) }))
	mux.HandleFunc("/api/codex/session/start", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { apiCodexSessionStart(w, r, root, nx) }))
	mux.HandleFunc("/api/codex/session/poll", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { apiCodexSessionPoll(w, r, root, nx) }))
	mux.HandleFunc("/api/codex/session/stop", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { apiCodexSessionStop(w, r, root, nx) }))
	mux.HandleFunc("/api/codex/turn", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { apiCodexTurn(w, r, root, nx) }))
	mux.HandleFunc("/api/codex/approval", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { apiCodexApproval(w, r, root, nx) }))
	mux.HandleFunc("/api/nexus/health", withNexus(func(w http.ResponseWriter, r *http.Request, nx *Nexus) { apiNexusHealth(w, r, root, nx) }))

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
	go runCodexTelemetryLoop(ctx, root)

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
				nx, err := LoadNexus(root)
				if err != nil || nx == nil || len(nx.Projects) == 0 {
					continue
				}
				for _, p := range nx.Projects {
					go func(projectRoot string) {
						_, _ = RunTick(ctx, projectRoot, RunOptions{})
					}(p.StorageRoot)
				}
			}
		}
	}
}

func uiBoard(w http.ResponseWriter, r *http.Request, root string, cfg Config, title string, repoSlug string, nexus *Nexus) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if nexus != nil {
		uiBoardNexus(w, r, root, cfg, nexus)
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
	runningTaskID := ""
	runningMode := ""
	running := false
	if st, err := readRunState(root); err == nil && st != nil && st.Running {
		running = true
		runningTaskID = st.TaskID
		runningMode = st.Mode
	}

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
		"Running":     running,
		"RunningTask": runningTaskID,
		"RunningMode": runningMode,
	})
}

func uiTask(w http.ResponseWriter, r *http.Request, root string, cfg Config, title string, repoSlug string, nexus *Nexus) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if nexus != nil {
		uiTaskNexus(w, r, cfg, nexus)
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
	planMD := read(planProposalFile)
	gitMeta, _ := getTaskGitFromMD(taskMD)

	priority := ""
	if p, ok := getTaskPriorityFromMD(taskMD); ok {
		priority = p
	}
	agentName, agentTip := agentUI(cfg)
	chatLabel := "Run in Chat"
	chatAutoRun := true
	chatSession := ""
	if ss, ok := latestChatSessionForTask(root, task.ID); ok {
		chatLabel = "Open in Chat"
		chatAutoRun = false
		chatSession = strings.TrimSuffix(ss.Name, ".jsonl")
	}
	chatHref := "/chat?task=" + url.QueryEscape(task.ID)
	if chatAutoRun {
		chatHref += "&autorun=1"
	}
	if chatSession != "" {
		chatHref += "&session=" + url.QueryEscape(chatSession)
	}
	if err := tpl.Execute(w, map[string]any{
		"Task":        task,
		"TaskMD":      taskMD,
		"TaskHTML":    renderMD(renderTask),
		"ImplHTML":    renderMD(implMD),
		"ImplMD":      implMD,
		"PlanHTML":    renderMD(planMD),
		"PlanMD":      planMD,
		"HasPlan":     strings.TrimSpace(planMD) != "",
		"ColorKey":    colorKey,
		"Palette":     pastelPalette,
		"RingHex":     ringHexForPriorityLabel(priority),
		"Priority":    priority,
		"AllPrios":    []string{"", "HIGH", "MEDIUM", "LOW"},
		"Title":       title,
		"RepoSlug":    repoSlug,
		"HazelURL":    hazelPoweredByURL(),
		"AgentName":   agentName,
		"AgentTip":    agentTip,
		"ChatHref":    chatHref,
		"ChatLabel":   chatLabel,
		"ChatAutoRun": chatAutoRun,
		"ChatSession": chatSession,
		"Git":         gitMeta,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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

func resolveProjectRoot(nexus *Nexus, r *http.Request, fallback string) (projectRoot string, projectKey string, err error) {
	_ = fallback
	if nexus == nil {
		return "", "", fmt.Errorf("nexus mode required")
	}
	key := strings.TrimSpace(r.FormValue("project"))
	if key == "" {
		key = strings.TrimSpace(r.URL.Query().Get("project"))
	}
	if key == "" {
		return "", "", fmt.Errorf("project is required in nexus mode")
	}
	p, ok := nexus.ProjectByKey(key)
	if !ok {
		return "", "", fmt.Errorf("unknown project %q", key)
	}
	return p.StorageRoot, key, nil
}

func uiMutateStatus(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
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
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var b Board
	if err := readYAMLFile(boardPath(projectRoot), &b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	for _, t := range b.Tasks {
		if t.ID == id {
			if status == StatusReview || status == StatusDone {
				md, _ := readTaskMD(projectRoot, id)
				git, _ := getTaskGitFromMD(md)
				if status == StatusReview && strings.TrimSpace(git.PRURL) == "" {
					http.Error(w, "cannot move to REVIEW without PR URL; use Open PR in task Git Flow", http.StatusBadRequest)
					return
				}
				if status == StatusDone && strings.TrimSpace(git.MergeSHA) == "" {
					http.Error(w, "cannot move to DONE without merge SHA; use Mark Merged in task Git Flow", http.StatusBadRequest)
					return
				}
			}
			t.Status = status
			t.UpdatedAt = now
			break
		}
	}
	if err := writeYAMLFile(boardPath(projectRoot), &b); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Header.Get("X-Hazel-Ajax") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	target := "/"
	if projectKey != "" {
		target = "/?project=" + projectKey
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func uiMutatePriority(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
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
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	md, err := readTaskMD(projectRoot, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := setTaskPriorityInMD(md, lbl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeTaskMD(projectRoot, id, updated); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = bumpBoardUpdatedAt(projectRoot, id)
	if r.Header.Get("X-Hazel-Ajax") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	target := "/"
	if projectKey != "" {
		target = "/?project=" + projectKey
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func uiMutateInterval(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
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

func uiMutateRun(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Preflight: if agent is unconfigured, fail fast rather than silently doing nothing.
	cfg, cfgErr := loadConfigOrDefault(projectRoot)
	if cfgErr == nil && cfg.Version == 0 {
		cfg = defaultConfig()
	}
	if strings.TrimSpace(cfg.AgentCommand) == "" {
		http.Error(w, "agent_command is not configured in .hazel/config.yaml", http.StatusBadRequest)
		return
	}

	// Run asynchronously so the UI doesn't hang for long agent runs.
	go func() {
		res, err := RunTick(context.Background(), projectRoot, RunOptions{})
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
	target := "/"
	if projectKey != "" {
		target = "/?project=" + projectKey
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func uiMutateNewTask(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
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
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t, err := createNewTask(projectRoot, title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target := "/task/" + t.ID
	if projectKey != "" {
		target = "/task/" + projectKey + "/" + t.ID
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func uiMutateTaskMD(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
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
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := ensureTaskScaffold(projectRoot, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeTaskMD(projectRoot, id, content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Update board updated_at for visibility in tooling.
	_ = bumpBoardUpdatedAt(projectRoot, id)
	target := "/task/" + id
	if projectKey != "" {
		target = "/task/" + projectKey + "/" + id
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func uiMutateTaskColor(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
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
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	md, err := readTaskMD(projectRoot, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := setTaskColorInMD(md, color)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := writeTaskMD(projectRoot, id, updated); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target := "/task/" + id
	if projectKey != "" {
		target = "/task/" + projectKey + "/" + id
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func uiMutatePlan(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
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
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = clearPlanProposal(projectRoot, id)
	// Run the same workflow as `hazel plan`, but from the UI.
	// Do it asynchronously so the request doesn't hang for long-running agents.
	go func() { _, _ = Plan(context.Background(), projectRoot, id) }()

	if r.Header.Get("X-Hazel-Ajax") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	target := "/task/" + id + "?plan=started"
	if projectKey != "" {
		target = "/task/" + projectKey + "/" + id + "?plan=started"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func uiMutatePlanDecision(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
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
	decision := strings.ToLower(strings.TrimSpace(r.FormValue("decision")))
	if decision != "accept" && decision != "decline" {
		http.Error(w, "invalid decision", http.StatusBadRequest)
		return
	}
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if decision == "decline" {
		if err := clearPlanProposal(projectRoot, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		taskMD, err := readTaskMD(projectRoot, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		planMD, err := readPlanProposal(projectRoot, id)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "plan proposal not found", http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		merged, err := mergeTaskMDWithPlanProposal(taskMD, planMD)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := writeTaskMD(projectRoot, id, merged); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := clearPlanProposal(projectRoot, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	target := "/task/" + id
	if projectKey != "" {
		target = "/task/" + projectKey + "/" + id
	}
	target += "?plan=" + url.QueryEscape(decision+"ed")
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func findTaskInBoard(projectRoot, taskID string) (*BoardTask, error) {
	var b Board
	if err := readYAMLFile(boardPath(projectRoot), &b); err != nil {
		return nil, err
	}
	for _, t := range b.Tasks {
		if t.ID == taskID {
			return t, nil
		}
	}
	return nil, fmt.Errorf("task not found: %s", taskID)
}

func uiMutateGitStart(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	taskID := strings.TrimSpace(r.FormValue("id"))
	task, err := findTaskInBoard(projectRoot, taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	project, ok := nexus.ProjectByKey(projectKey)
	if !ok {
		http.Error(w, "unknown project", http.StatusBadRequest)
		return
	}
	cfg, _ := loadConfigOrDefault(root)
	if _, err := startTaskBranch(project, task, cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	task.Status = StatusActive
	_ = bumpBoardTaskStatus(project.StorageRoot, task.ID, StatusActive)
	http.Redirect(w, r, "/task/"+projectKey+"/"+task.ID, http.StatusSeeOther)
}

func uiMutateGitCommit(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	taskID := strings.TrimSpace(r.FormValue("id"))
	task, err := findTaskInBoard(projectRoot, taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	project, ok := nexus.ProjectByKey(projectKey)
	if !ok {
		http.Error(w, "unknown project", http.StatusBadRequest)
		return
	}
	msg := strings.TrimSpace(r.FormValue("message"))
	if msg == "" {
		msg = task.ID + ": " + task.Title
	}
	if _, err := commitTaskChanges(project, task, msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/task/"+projectKey+"/"+task.ID, http.StatusSeeOther)
}

func uiMutateGitPR(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	taskID := strings.TrimSpace(r.FormValue("id"))
	task, err := findTaskInBoard(projectRoot, taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	project, ok := nexus.ProjectByKey(projectKey)
	if !ok {
		http.Error(w, "unknown project", http.StatusBadRequest)
		return
	}
	cfg, _ := loadConfigOrDefault(root)
	meta, _ := captureTaskGitMeta(project, task, cfg)
	if _, err := openTaskPR(project, task, cfg, meta); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = bumpBoardTaskStatus(project.StorageRoot, task.ID, StatusReview)
	http.Redirect(w, r, "/task/"+projectKey+"/"+task.ID, http.StatusSeeOther)
}

func uiMutateGitMerge(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	taskID := strings.TrimSpace(r.FormValue("id"))
	task, err := findTaskInBoard(projectRoot, taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	project, ok := nexus.ProjectByKey(projectKey)
	if !ok {
		http.Error(w, "unknown project", http.StatusBadRequest)
		return
	}
	mergeSHA := strings.TrimSpace(r.FormValue("merge_sha"))
	if mergeSHA == "" {
		mergeSHA, _ = runCmd(project.RepoPath, nil, "git", "rev-parse", "HEAD")
	}
	if err := markTaskMerged(project, task, mergeSHA); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = bumpBoardTaskStatus(project.StorageRoot, task.ID, StatusDone)
	http.Redirect(w, r, "/task/"+projectKey+"/"+task.ID, http.StatusSeeOther)
}

func uiMutateConfig(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	_ = nexus
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, _ := loadConfigOrDefault(root)
	if strings.TrimSpace(r.FormValue("clear_github_token")) != "" {
		cfg.GitHubToken = ""
	}
	token := strings.TrimSpace(r.FormValue("github_token"))
	if token != "" {
		cfg.GitHubToken = token
	}
	cfg.GitBaseBranch = strings.TrimSpace(r.FormValue("git_base_branch"))
	if cfg.GitBaseBranch == "" {
		cfg.GitBaseBranch = "main"
	}
	if err := writeYAMLFile(configPath(root), &cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target := "/"
	if p := strings.TrimSpace(r.FormValue("project")); p != "" {
		target = "/?project=" + url.QueryEscape(p)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func bumpBoardTaskStatus(projectRoot, taskID string, status Status) error {
	var b Board
	if err := readYAMLFile(boardPath(projectRoot), &b); err != nil {
		return err
	}
	now := time.Now()
	changed := false
	for _, t := range b.Tasks {
		if t.ID == taskID {
			t.Status = status
			t.UpdatedAt = now
			changed = true
			break
		}
	}
	if !changed {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return writeYAMLFile(boardPath(projectRoot), &b)
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

func apiRunState(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	projectRoot, _, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st, err := readRunState(projectRoot)
	if err != nil {
		// Treat missing/invalid state as "not running".
		st = &RunState{Running: false}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(st)
}

func apiRunTail(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	projectRoot, _, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	lines := 80
	if raw := strings.TrimSpace(r.URL.Query().Get("lines")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			lines = n
		}
	}
	if lines > 400 {
		lines = 400
	}

	st, _ := readRunState(projectRoot)
	logPath := ""
	if st != nil && st.LogPath != "" {
		logPath = st.LogPath
	}
	if logPath == "" {
		// Fall back to the newest run log.
		logPath = newestRunLog(projectRoot)
	}
	if logPath == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	b, err := os.ReadFile(logPath)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(tailLines(string(b), lines)))
}

func newestRunLog(root string) string {
	dir := runsDir(root)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var logs []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".log") {
			logs = append(logs, filepath.Join(dir, name))
		}
	}
	if len(logs) == 0 {
		return ""
	}
	sort.Strings(logs)
	return logs[len(logs)-1]
}

func tailLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n") + "\n"
	}
	return strings.Join(lines[len(lines)-n:], "\n") + "\n"
}

func uiRuns(w http.ResponseWriter, r *http.Request, root string, title string, repoSlug string, nexus *Nexus) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/runs" && r.URL.Path != "/history" {
		http.NotFound(w, r)
		return
	}

	type row struct {
		TaskID      string
		TaskIDRaw   string
		Sessions    int
		LastAt      string
		LastSummary string
		LatestName  string
		ChatHref    string
	}

	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if nexus != nil && projectKey != "" {
		if p, ok := nexus.ProjectByKey(projectKey); ok {
			title = p.Name
			repoSlug = p.RepoSlug
		}
	}
	summaries, err := listChatSessionSummaries(projectRoot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type agg struct {
		count      int
		lastAt     time.Time
		lastText   string
		latestName string
	}
	grouped := map[string]*agg{}
	for _, s := range summaries {
		taskID := strings.TrimSpace(s.TaskID)
		if taskID == "" {
			taskID = "(none)"
		}
		g := grouped[taskID]
		if g == nil {
			g = &agg{}
			grouped[taskID] = g
		}
		g.count++
		if g.lastAt.IsZero() || s.ModifiedAt.After(g.lastAt) {
			g.lastAt = s.ModifiedAt
			g.lastText = s.LastAssistant
			g.latestName = s.Name
		}
	}
	var rows []row
	for taskID, g := range grouped {
		taskIDRaw := taskID
		if taskIDRaw == "(none)" {
			taskIDRaw = ""
		}
		chatHref := "/chat"
		if projectKey != "" {
			chatHref += "?project=" + url.QueryEscape(projectKey)
		} else {
			chatHref += "?"
		}
		if taskIDRaw != "" {
			chatHref += "&task=" + url.QueryEscape(taskIDRaw)
		}
		if g.latestName != "" {
			chatHref += "&session=" + url.QueryEscape(strings.TrimSuffix(g.latestName, ".jsonl"))
		}
		rows = append(rows, row{
			TaskID:      taskID,
			TaskIDRaw:   taskIDRaw,
			Sessions:    g.count,
			LastAt:      g.lastAt.Format("2006-01-02 15:04"),
			LastSummary: clipped(strings.TrimSpace(g.lastText), 180),
			LatestName:  strings.TrimSuffix(g.latestName, ".jsonl"),
			ChatHref:    chatHref,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].LastAt > rows[j].LastAt
	})
	embed := strings.TrimSpace(r.URL.Query().Get("embed")) == "1"

	tpl := template.Must(template.New("runs").Parse(uiRunsHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tpl.Execute(w, map[string]any{
		"Title":    title,
		"RepoSlug": repoSlug,
		"Rows":     rows,
		"Project":  projectKey,
		"Embed":    embed,
	})
}

func uiRunView(w http.ResponseWriter, r *http.Request, root string, title string, repoSlug string, nexus *Nexus) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	projectRoot, projectKey, err := resolveProjectRoot(nexus, r, root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if nexus != nil && projectKey != "" {
		if p, ok := nexus.ProjectByKey(projectKey); ok {
			title = p.Name
			repoSlug = p.RepoSlug
		}
	}
	name := strings.TrimPrefix(r.URL.Path, "/runs/")
	if strings.HasPrefix(r.URL.Path, "/history/") {
		name = strings.TrimPrefix(r.URL.Path, "/history/")
	}
	name = strings.Trim(name, "/")
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	sessionName := name
	if !strings.HasSuffix(sessionName, ".jsonl") {
		sessionName += ".jsonl"
	}
	sessionPath := filepath.Join(chatSessionsDir(projectRoot), sessionName)
	evs, err := loadChatSessionEvents(sessionPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	eventsJSON := "[]"
	if b, err := json.Marshal(evs); err == nil {
		eventsJSON = string(b)
	}
	taskID := taskIDFromChatSessionName(sessionName)
	embed := strings.TrimSpace(r.URL.Query().Get("embed")) == "1"

	tpl := template.Must(template.New("run").Parse(uiRunHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tpl.Execute(w, map[string]any{
		"Title":    title,
		"RepoSlug": repoSlug,
		"Name":     strings.TrimSuffix(sessionName, ".jsonl"),
		"TaskID":   taskID,
		"Events":   template.JS(eventsJSON),
		"Project":  projectKey,
		"Embed":    embed,
	})
}

const uiRunsHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}} - History</title>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&display=swap');
    :root { --bg:#102022; --panel:rgba(25,49,51,.35); --text:#e7fbff; --muted:#8dc7cf; --accent:#13daec; --line:#326267; }
    * { box-sizing:border-box; }
    body { margin:0; font-family:"Space Grotesk", ui-sans-serif, system-ui; background:var(--bg); color:var(--text); min-height:100dvh; display:flex; flex-direction:column; }
    body::before { content:""; position:fixed; inset:0; pointer-events:none; background:linear-gradient(rgba(19,218,236,.04) 50%, rgba(0,0,0,0) 50%); background-size:100% 4px; }
    header { padding:10px 16px; border-bottom:1px solid var(--line); background: rgba(16,32,34,.95); position: sticky; top:0; z-index:10; }
    header a { color: var(--accent); text-decoration:none; font-size:11px; text-transform:uppercase; }
    h1 { margin:8px 0 0; color:var(--accent); font-size:15px; text-transform:uppercase; letter-spacing:.1em; }
    main { padding:10px; flex:1; min-height:0; }
    .panel { border:1px solid var(--line); background:var(--panel); border-radius:4px; padding:10px; }
    .row { display:flex; justify-content:space-between; gap:8px; align-items:center; margin-bottom:8px; }
    .pill { border:1px solid var(--line); border-radius:4px; padding:3px 7px; font-size:10px; text-transform:uppercase; color:var(--text); background:rgba(0,0,0,.2); }
    table { width:100%; border-collapse: collapse; font-size:12px; }
    th, td { text-align:left; padding:8px 6px; border-bottom:1px solid rgba(255,255,255,.08); vertical-align: top; }
    th { color:var(--muted); font-size:10px; text-transform:uppercase; letter-spacing:.1em; }
    a.link { color:var(--accent); text-decoration:none; }
    a.link:hover { text-decoration:underline; }
  </style>
</head>
<body>
  {{if not .Embed}}
  <header>
    <a href="{{if .Project}}/?project={{.Project}}{{else}}/{{end}}">Back to board</a>
    <h1>History</h1>
  </header>
  {{end}}
  <main>
    <section class="panel">
      <div class="row">
        <div class="pill">{{.Title}}</div>
        <div class="pill">{{len .Rows}} tasks</div>
      </div>
      <div style="margin-top:12px;">
        <table>
          <thead>
            <tr>
              <th>Task</th>
              <th>Sessions</th>
              <th>Last</th>
              <th>Summary</th>
            </tr>
          </thead>
          <tbody>
            {{range .Rows}}
              <tr>
                <td>
                  <a class="link hzOpenChat" data-project="{{$.Project}}" data-task="{{.TaskIDRaw}}" data-session="{{.LatestName}}" href="{{.ChatHref}}">{{.TaskID}}</a>
                </td>
                <td>{{.Sessions}}</td>
                <td>{{.LastAt}}</td>
                <td>{{if .LastSummary}}{{.LastSummary}}{{else}}-{{end}}</td>
              </tr>
            {{end}}
          </tbody>
        </table>
      </div>
    </section>
  </main>
  <script>
    (function(){
      if (!{{if .Embed}}true{{else}}false{{end}}) return;
      const links = Array.from(document.querySelectorAll('.hzOpenChat'));
      for (const a of links) {
        a.addEventListener('click', (ev) => {
          try {
            if (window.parent && window.parent !== window) {
              ev.preventDefault();
              window.parent.postMessage({
                type: 'hazel-open-chat-session',
                project: a.getAttribute('data-project') || '',
                task: a.getAttribute('data-task') || '',
                session: a.getAttribute('data-session') || '',
                autorun: false
              }, '*');
              return false;
            }
          } catch (_) {}
          return true;
        });
      }
    })();
  </script>
</body>
</html>`

const uiRunHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}} - History {{.Name}}</title>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&display=swap');
    :root { --bg:#102022; --panel:rgba(25,49,51,.35); --text:#e7fbff; --muted:#8dc7cf; --accent:#13daec; --line:#326267; }
    * { box-sizing:border-box; }
    body { margin:0; font-family:"Space Grotesk", ui-sans-serif, system-ui; background:var(--bg); color:var(--text); min-height:100dvh; display:flex; flex-direction:column; }
    body::before { content:""; position:fixed; inset:0; pointer-events:none; background:linear-gradient(rgba(19,218,236,.04) 50%, rgba(0,0,0,0) 50%); background-size:100% 4px; }
    header { padding:10px 16px; border-bottom:1px solid var(--line); background: rgba(16,32,34,.95); position: sticky; top:0; z-index:10; }
    header a { color: var(--accent); text-decoration:none; font-size:11px; text-transform:uppercase; }
    h1 { margin:8px 0 0; color:var(--accent); font-size:15px; text-transform:uppercase; letter-spacing:.1em; }
    .meta { margin-top:6px; color: var(--muted); font-size:10px; text-transform:uppercase; letter-spacing:.08em; }
    main { padding:10px; flex:1; min-height:0; }
    .stream { white-space: pre-wrap; background: rgba(0,0,0,.22); border:1px solid var(--line); border-radius:4px; padding:10px; overflow:auto; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 12px; min-height: 200px; }
    .line { margin-bottom:6px; }
    .line-user { color:#9de8f3; }
    .line-assistant { color:#f5ffff; }
    .line-tool { color:#b8f8a2; }
    .line-warn { color:#facc15; }
    .line-err { color:#ff6b6b; }
    .pill { border:1px solid var(--line); border-radius:4px; padding:3px 7px; font-size:10px; text-transform:uppercase; color:var(--text); background:rgba(0,0,0,.2); }
  </style>
</head>
<body>
  {{if not .Embed}}
  <header>
    <a href="/history{{if .Project}}?project={{.Project}}{{end}}">Back to history</a>
    <h1>{{.Name}}</h1>
    <div class="meta"><span class="pill">{{if .TaskID}}{{.TaskID}}{{else}}(none){{end}}</span></div>
  </header>
  {{end}}
  <main>
    <div id="hzStream" class="stream"></div>
  </main>
  <script>
    (function(){
      const events = {{.Events}};
      const stream = document.getElementById('hzStream');
      const assistantByItem = new Map();
      const toolByItem = new Map();
      const toolCommandByItem = new Map();
      const pendingToolCommands = [];
      function appendLine(cls, text) {
        const div = document.createElement('div');
        div.className = 'line ' + cls;
        div.textContent = text;
        stream.appendChild(div);
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
      for (const ev of (events || [])) {
        if (ev.type === 'assistant_delta') {
          const k = ev.item_id || ev.turn_id || '__assistant__';
          if (!assistantByItem.has(k)) assistantByItem.set(k, appendLine('line-assistant', ''));
          assistantByItem.get(k).textContent += (ev.text || '');
        } else if (ev.type === 'tool_command') {
          const k = ev.item_id || ev.turn_id || '';
          if (ev.text) pendingToolCommands.push(ev.text);
          if (k) {
            toolCommandByItem.set(k, ev.text || 'Command');
            const block = toolByItem.get(k);
            if (block && block.summary) block.summary.textContent = ev.text || 'Command';
          } else {
            appendLine('line-tool', ev.text || 'Command');
          }
        } else if (ev.type === 'assistant_message') {
          const k = ev.item_id || ev.turn_id || '';
          if (k && assistantByItem.has(k)) assistantByItem.get(k).innerHTML = renderMarkdown(ev.text || '');
          else {
            const el = appendLine('line-assistant', '');
            el.innerHTML = renderMarkdown(ev.text || '');
          }
        } else if (ev.type === 'user_message') {
          appendLine('line-user', '> ' + (ev.text || ''));
        } else if (ev.type === 'tool_output') {
          const k = ev.item_id || ev.turn_id || ('tool_' + Math.random());
          let block = toolByItem.get(k);
          if (!block) {
            const details = document.createElement('details');
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
            block = { pre, summary };
            toolByItem.set(k, block);
          }
          block.pre.textContent += (ev.text || '');
        } else if (ev.type === 'error') {
          appendLine('line-err', '[error] ' + (ev.text || ''));
        } else if (ev.type === 'warning' || ev.type === 'approval_requested' || ev.type === 'approval_resolved' || ev.type === 'session_done') {
          if (ev.type === 'approval_requested') {
            const cmd = parseCommandFromApprovalText(ev.text || '');
            if (cmd) pendingToolCommands.push(cmd);
          }
          appendLine('line-warn', '[' + ev.type + '] ' + (ev.text || ''));
        }
      }
    })();
  </script>
</body>
</html>`

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
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&display=swap');
    :root { --bg:#102022; --panel:#193133; --text:#e7fbff; --muted:#8dc7cf; --accent:#13daec; --line:#326267; --warn:#facc15; }
    * { box-sizing:border-box; }
    body { margin:0; font-family: "Space Grotesk", ui-sans-serif, system-ui; background:var(--bg); color:var(--text); min-height:100dvh; display:flex; flex-direction:column; }
    body::before { content:""; position:fixed; inset:0; pointer-events:none; background:linear-gradient(rgba(19,218,236,.04) 50%, rgba(0,0,0,0) 50%); background-size:100% 4px; }
    header { border-bottom:1px solid var(--line); background: rgba(16,32,34,.96); position: sticky; top:0; z-index: 20; }
    .topline { padding:10px 16px; background: rgba(25,49,51,.75); border-bottom:1px solid var(--line); display:flex; justify-content:space-between; align-items:center; gap:10px; }
    .topline b { color:var(--accent); letter-spacing:.14em; text-transform:uppercase; font-size:11px; }
    .topline span { font-size:10px; color:var(--warn); font-weight:700; }
    .navline { display:flex; flex-wrap:wrap; gap:8px; align-items:center; padding:12px 16px; }
    h1 { margin:0; font-size:13px; text-transform:uppercase; letter-spacing:.12em; color:var(--accent); }
    .actions { display:flex; gap:10px; align-items:center; flex-wrap:wrap; font-size:12px; }
    .actions a { color:var(--accent); text-decoration:none; border:1px solid var(--line); padding:7px 10px; border-radius:4px; text-transform:uppercase; font-size:10px; letter-spacing:.08em; }
    .actions form { display:flex; gap:8px; align-items:center; margin:0; }
    details.cols { position: relative; }
    details.cols > summary { list-style:none; cursor:pointer; user-select:none; border:1px solid var(--line); padding:7px 10px; border-radius:4px; color: var(--muted); background: rgba(0,0,0,.2); text-transform:uppercase; font-size:10px; letter-spacing:.08em; }
    details.cols[open] > summary { border-color: var(--accent); color: var(--accent); }
    .actions form.colmenu { display:flex; flex-direction:column; align-items:stretch; gap:0; }
    .colmenu { position:absolute; right:0; top: 38px; z-index:50; width: 240px; max-width: calc(100vw - 48px); border-radius:4px; border:1px solid var(--line); background: #112426; padding:10px 10px; box-shadow: 0 18px 50px rgba(0,0,0,.45); }
    .colmenu { max-height: min(60vh, 360px); overflow:auto; }
    .colmenu label { display:flex; justify-content:space-between; gap:10px; padding:6px 6px; border-radius:10px; }
    .colmenu label:hover { background: rgba(19,218,236,.08); }
    .colmenu input { transform: translateY(1px); }
    .colmenu button { width:100%; margin-top:8px; }
    .colmenu .row { display:flex; gap:8px; align-items:center; }
    .colmenu .row input[type="number"] { width: 96px; }
    .pilltop { display:inline-flex; gap:10px; align-items:center; border:1px solid var(--line); background: rgba(0,0,0,.2); padding:7px 10px; border-radius: 4px; color: var(--muted); font-size:10px; text-transform:uppercase; letter-spacing:.08em; }
    .dot { width:8px; height:8px; border-radius:999px; background: var(--accent); box-shadow: 0 0 0 4px rgba(19,218,236,.12); }
    main { padding:10px; flex:1; }
    .boardbar { display:flex; justify-content:flex-start; gap:10px; align-items:center; margin: 0 0 10px; }
    .boardbar button { padding:8px 10px; border-radius:4px; text-transform:uppercase; font-size:10px; letter-spacing:.08em; }
    dialog { border:1px solid var(--line); border-radius:6px; background: #112426; color: var(--text); padding: 14px 14px; width: min(540px, calc(100vw - 48px)); }
    dialog::backdrop { background: rgba(0,0,0,.55); }
    dialog h3 { margin: 0 0 10px; font-size: 12px; letter-spacing:.12em; text-transform: uppercase; color: var(--accent); }
    dialog form { margin:0; display:flex; flex-direction:column; gap:10px; }
    dialog input[type="text"] { width:100%; padding:10px 12px; border-radius:4px; }
    dialog .dlgrow { display:flex; justify-content:flex-end; gap:10px; align-items:center; }
    dialog .ghost { background: rgba(0,0,0,.2); border:1px solid var(--line); }
    .board { display:grid; gap:10px; grid-template-columns: repeat(var(--cols), minmax(250px, 1fr)); overflow-x:auto; padding-bottom:12px; }
    .col { background: rgba(25,49,51,.35); border:1px solid var(--line); border-radius:4px; padding:10px; min-height: 68vh; }
    .col h2 { margin:0 0 10px; font-size:11px; letter-spacing:.12em; text-transform:uppercase; color: var(--accent); display:flex; justify-content:space-between; border-bottom:1px solid var(--line); padding-bottom:6px; }
    .card { padding:10px; margin:8px 0; border-radius:4px; border:1px solid var(--line); background: linear-gradient(180deg, rgba(0,0,0,.45), rgba(0,0,0,.45)), var(--cardbg, rgba(15,24,48,.92)); }
    .id a { color: var(--accent); text-decoration:none; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size:11px; font-weight:700; }
    .title { margin-top:7px; font-size:12px; line-height:1.35; color: #d9f9ff; }
    .meta { margin-top:10px; display:flex; gap:8px; align-items:center; flex-wrap:wrap; }
    .meta form { margin:0; display:flex; gap:6px; align-items:center; }
    select, input { background: rgba(0,0,0,.28); border:1px solid var(--line); color: var(--text); padding:7px 9px; border-radius:4px; font-size:11px; }
    button { background: rgba(19,218,236,.12); border:1px solid var(--line); color: var(--text); padding:6px 8px; border-radius:4px; font-size:11px; cursor:pointer; text-transform:uppercase; }
    button:hover { border-color: var(--accent); color: var(--accent); }
    .hint { margin-top:8px; color: var(--muted); font-size:10px; text-transform:uppercase; letter-spacing:.06em; }
    .card.dragging { opacity: .6; }
    .card { box-shadow: inset 0 0 0 2px var(--ring, rgba(255,255,255,0)); }
    .card.running { border-color: var(--accent); box-shadow: 0 0 0 2px rgba(19,218,236,.18), inset 0 0 0 2px rgba(19,218,236,.28); }
    @keyframes spin { to { transform: rotate(360deg); } }
    .col.dropover { border-color: var(--accent); box-shadow: 0 0 0 2px rgba(19,218,236,.15); }
    footer { padding: 8px 12px; color:#111; font-size:10px; background:var(--warn); border-top:2px solid #111; text-transform:uppercase; letter-spacing:.08em; display:flex; justify-content:space-between; }
    footer a { color:#111; text-decoration:none; font-weight:700; }
  </style>
</head>
<body>
  <header>
    <div class="topline">
      <b>Hazel Nexus</b>
      <span>[ SECURE_LINK_ACTIVE ]</span>
    </div>
    <div class="navline">
      <div style="flex:1; min-width:200px;">
        <h1>{{.Title}}</h1>
      </div>
      <div class="actions">
        {{if .Running}}
          <a href="/history">Running...</a>
        {{else}}
          <form action="/mutate/run" method="post" onsubmit="return hazelRunTick(this)">
            <button type="submit">Run tick</button>
          </form>
        {{end}}
        <a href="/history">History</a>
        <a href="/chat">Chat</a>
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
    </div>
    <div class="hint" style="padding:0 16px 10px;">Board edits update board.yaml. task.md edits only happen when you explicitly save.</div>
  </header>
  <main>
    <div class="boardbar">
      <button type="button" onclick="hazelOpenCreate()">+ Create task</button>
    </div>
    <dialog id="hzCreateDlg">
      <h3>Create Task</h3>
      <form action="/mutate/new_task" method="post">
        <input id="hzCreateTitle" type="text" name="title" placeholder="Task title" required />
        <div class="dlgrow">
          <button class="ghost" type="button" onclick="hazelCloseCreate()">Cancel</button>
          <button type="submit">Create</button>
        </div>
      </form>
    </dialog>
    <div class="board" style="--cols: {{.ColCount}};">
      {{range .Order}}
        {{$status := .}}
        {{$tasks := index $.Columns $status}}
        <section class="col dropzone" data-status="{{$status}}">
          <h2><span>{{$status}}</span><span>{{len $tasks}}</span></h2>
          {{range $tasks}}
            <div class="card {{if and $.Running (eq .Task.ID $.RunningTask)}}running{{end}}" draggable="true" data-id="{{.Task.ID}}" style="--cardbg: {{.ColorHex}}; --ring: {{.RingHex}};">
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
    <span>Duchess_Operator_OS</span>
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
        .then(() => {
          setTimeout(() => location.href = "/history", 250);
        })
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

    function hazelOpenCreate() {
      const dlg = document.getElementById("hzCreateDlg");
      if (!dlg || typeof dlg.showModal !== "function") return;
      dlg.showModal();
      const input = document.getElementById("hzCreateTitle");
      if (input) input.focus();
    }

    function hazelCloseCreate() {
      const dlg = document.getElementById("hzCreateDlg");
      if (!dlg || typeof dlg.close !== "function") return;
      dlg.close();
    }
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
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&display=swap');
    :root { --bg:#102022; --panel:#193133; --text:#e7fbff; --muted:#8dc7cf; --link:#13daec; --line:#326267; --warn:#facc15; }
    * { box-sizing:border-box; }
    body { margin:0; font-family: "Space Grotesk", ui-sans-serif, system-ui; background:var(--bg); color:var(--text); min-height:100dvh; display:flex; flex-direction:column; }
    body::before { content:""; position:fixed; inset:0; pointer-events:none; background:linear-gradient(rgba(19,218,236,.04) 50%, rgba(0,0,0,0) 50%); background-size:100% 4px; }
    header { border-bottom:1px solid var(--line); background: rgba(16,32,34,.96); position: sticky; top:0; z-index: 20; padding:10px 16px; }
    header a { color: var(--link); text-decoration:none; font-size:11px; text-transform:uppercase; border:1px solid var(--line); padding:6px 8px; border-radius:4px; }
    h1 { margin:10px 0 0; font-size:16px; letter-spacing:.08em; text-transform:uppercase; color:var(--link); }
    .meta { margin-top:6px; color: var(--muted); font-size:10px; text-transform:uppercase; letter-spacing:.08em; }
    main { padding: 12px; max-width: 1400px; margin:0 auto; width:100%; flex:1; }
    .panel { background: rgba(25,49,51,.35); border:1px solid var(--line); border-radius:4px; padding:12px 14px; margin-bottom:10px; }
    .panel h2 { margin:0 0 10px; font-size:11px; letter-spacing:.14em; text-transform:uppercase; color: var(--link); border-bottom:1px solid var(--line); padding-bottom:6px; }
    textarea { width:100%; min-height: 220px; background: rgba(0,0,0,.2); border:1px solid var(--line); color: var(--text); border-radius:4px; padding:10px 12px; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size:12px; }
    .row { display:flex; gap:10px; align-items:center; flex-wrap:wrap; margin-top:10px; }
    .row form { margin:0; display:flex; gap:8px; align-items:center; }
    .pill { border:1px solid var(--line); padding:2px 8px; border-radius:4px; font-size:10px; background: rgba(0,0,0,.2); text-transform:uppercase; }
    .md { padding: 10px 12px; border-radius:4px; border:1px solid var(--line); background: rgba(0,0,0,.2); }
    .md :is(h1,h2,h3){ margin-top: 14px; }
    .md a { color: var(--link); }
    .md code { background: rgba(255,255,255,.08); padding: 1px 5px; border-radius:6px; }
    .md pre { background: rgba(0,0,0,.35); padding: 10px 12px; border-radius:4px; overflow:auto; }
    .topbar { display:flex; align-items:center; justify-content:space-between; gap:12px; }
    .backbtn { display:inline-flex; gap:10px; align-items:center; border:1px solid var(--line); background: rgba(0,0,0,.2); padding:8px 10px; border-radius:4px; }
    .backbtn:hover { border-color: var(--link); }
    .split { display:grid; grid-template-columns: 1fr; gap:12px; }
    @media (min-width: 980px) { .split { grid-template-columns: 1fr 1fr; } }
    .gitbar { display:grid; grid-template-columns: repeat(4, minmax(0,1fr)); gap:8px; }
    .gitbar form { margin:0; display:flex; gap:6px; align-items:center; }
    .gitbar input { width:100%; background: rgba(0,0,0,.2); border:1px solid var(--line); color:var(--text); border-radius:4px; padding:7px 8px; font-size:11px; }
    .gitmeta { margin-top:8px; display:flex; gap:8px; flex-wrap:wrap; font-size:10px; color:var(--muted); text-transform:uppercase; }
    @media (max-width: 1200px) { .gitbar { grid-template-columns: 1fr 1fr; } }
    @media (max-width: 760px) { .gitbar { grid-template-columns: 1fr; } }
    .editbar { display:flex; justify-content:space-between; align-items:center; gap:10px; flex-wrap:wrap; margin: 10px 0 8px; }
    .ghost { background: rgba(0,0,0,.2); border:1px solid var(--line); color: var(--text); padding:8px 10px; border-radius:4px; cursor:pointer; text-transform:uppercase; font-size:10px; }
    .ghost:hover { border-color: var(--link); color:var(--link); }
    .editor { display:none; margin-top:10px; }
    .editor.on { display:block; }
    footer { padding: 8px 12px; color:#111; font-size:10px; background:var(--warn); border-top:2px solid #111; text-transform:uppercase; letter-spacing:.08em; display:flex; justify-content:space-between; }
    footer a { color:#111; text-decoration:none; font-weight:700; }
  </style>
</head>
<body>
  <header>
    <div class="topbar">
      <a class="backbtn" href="{{if .Project}}/?project={{.Project}}{{else}}/{{end}}">Back to board</a>
      <a class="backbtn" id="hzTaskChatBtn" href="{{.ChatHref}}" onclick="return hazelOpenTaskChat(event)">{{if .ChatLabel}}{{.ChatLabel}}{{else}}Run in Chat{{end}}</a>
      <div class="meta"><span {{if .AgentTip}}title="{{.AgentTip}}"{{end}}>Agent: {{.AgentName}}</span> | Status: {{.Task.Status}} | Updated: {{.Task.UpdatedAt}}</div>
    </div>
    <h1>{{.Task.ID}}: {{.Task.Title}}</h1>
  </header>
  <main>
    <section class="panel">
      <h2>Git Flow</h2>
      <div class="gitbar">
        <form action="/mutate/git/start" method="post">
          <input type="hidden" name="id" value="{{.Task.ID}}" />
          {{if .Project}}<input type="hidden" name="project" value="{{.Project}}" />{{end}}
          <button class="ghost" type="submit">Start Branch</button>
        </form>
        <form action="/mutate/git/commit" method="post">
          <input type="hidden" name="id" value="{{.Task.ID}}" />
          {{if .Project}}<input type="hidden" name="project" value="{{.Project}}" />{{end}}
          <input type="text" name="message" placeholder="Commit message (optional)" />
          <button class="ghost" type="submit">Commit</button>
        </form>
        <form action="/mutate/git/pr" method="post">
          <input type="hidden" name="id" value="{{.Task.ID}}" />
          {{if .Project}}<input type="hidden" name="project" value="{{.Project}}" />{{end}}
          <button class="ghost" type="submit">Open PR</button>
        </form>
        <form action="/mutate/git/merge" method="post">
          <input type="hidden" name="id" value="{{.Task.ID}}" />
          {{if .Project}}<input type="hidden" name="project" value="{{.Project}}" />{{end}}
          <input type="text" name="merge_sha" placeholder="Merge SHA (blank = HEAD)" />
          <button class="ghost" type="submit">Mark Merged</button>
        </form>
      </div>
      <div class="gitmeta">
        <span>Branch: {{if .Git.Branch}}<code>{{.Git.Branch}}</code>{{else}}-{{end}}</span>
        <span>Base: {{if .Git.Base}}<code>{{.Git.Base}}</code>{{else}}-{{end}}</span>
        <span>Last Commit: {{if .Git.LastCommit}}<code>{{.Git.LastCommit}}</code>{{else}}-{{end}}</span>
        <span>PR: {{if .Git.PRURL}}<a href="{{.Git.PRURL}}" target="_blank" rel="noreferrer">{{.Git.PRURL}}</a>{{else}}-{{end}}</span>
        <span>Merge: {{if .Git.MergeSHA}}<code>{{.Git.MergeSHA}}</code>{{else}}-{{end}}</span>
      </div>
    </section>
    <div class="split">
      <section class="panel">
        <h2>Task</h2>
	        <div class="editbar">
	          <span class="pill" style="box-shadow: inset 0 0 0 3px {{.RingHex}};">Priority: {{if .Priority}}{{.Priority}}{{else}}Unset{{end}}</span>
	          <div class="row">
	            <form action="/mutate/plan" method="post" onsubmit="return hazelPlan(this)">
	              <input type="hidden" name="id" value="{{.Task.ID}}" />
                {{if .Project}}<input type="hidden" name="project" value="{{.Project}}" />{{end}}
	              <button class="ghost" type="submit">Plan</button>
	            </form>
	            <button class="ghost" type="button" onclick="hazelToggleEdit()">Edit</button>
	          </div>
	        </div>
        <div class="md">{{.TaskHTML}}</div>
        <div id="hzEditor" class="editor">
          <form action="/mutate/task_md" method="post">
            <input type="hidden" name="id" value="{{.Task.ID}}" />
            {{if .Project}}<input type="hidden" name="project" value="{{.Project}}" />{{end}}
            <textarea name="content">{{.TaskMD}}</textarea>
            <div class="row">
              <button type="submit">Save task.md</button>
              <button class="ghost" type="button" onclick="hazelToggleEdit()">Cancel</button>
            </div>
          </form>
        </div>
      </section>
      <section class="panel">
        {{if .HasPlan}}
        <h2>Plan Proposal</h2>
        <div class="row" style="margin-top:0;margin-bottom:10px;">
          <form action="/mutate/plan_decision" method="post">
            <input type="hidden" name="id" value="{{.Task.ID}}" />
            {{if .Project}}<input type="hidden" name="project" value="{{.Project}}" />{{end}}
            <input type="hidden" name="decision" value="accept" />
            <button class="ghost" type="submit">Accept Plan</button>
          </form>
          <form action="/mutate/plan_decision" method="post">
            <input type="hidden" name="id" value="{{.Task.ID}}" />
            {{if .Project}}<input type="hidden" name="project" value="{{.Project}}" />{{end}}
            <input type="hidden" name="decision" value="decline" />
            <button class="ghost" type="submit">Decline Plan</button>
          </form>
        </div>
        <div class="md">{{.PlanHTML}}</div>
        {{else}}
        <h2>Implementation Notes</h2>
        <div class="md">{{.ImplHTML}}</div>
        {{end}}
      </section>
    </div>
  </main>
  <footer>
    <span>Duchess_Operator_OS</span>
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

    function hazelOpenTaskChat(ev) {
      if (!ev) return true;
      try {
        if (window.parent && window.parent !== window) {
          ev.preventDefault();
          window.parent.postMessage({
            type: "hazel-open-chat-task",
            project: "{{.Project}}",
            task: "{{.Task.ID}}",
            autorun: {{if .ChatAutoRun}}true{{else}}false{{end}},
            session: "{{.ChatSession}}"
          }, "*");
          return false;
        }
      } catch (_) {}
      return true;
    }
  </script>
</body>
</html>`
