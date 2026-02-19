package hazel

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

type nexusCard struct {
	Task          *BoardTask
	ProjectKey    string
	ProjectName   string
	ColorHex      string
	PriorityLabel string
	RingHex       string
}

type nexusCompactItem struct {
	ID          string
	Title       string
	Status      Status
	ProjectKey  string
	ProjectName string
	ColorHex    string
}

func normalizeNexusProjectSelection(nexus *Nexus, selected string) string {
	if nexus == nil || len(nexus.Projects) == 0 {
		return ""
	}
	selected = strings.TrimSpace(selected)
	if selected != "" {
		if _, ok := nexus.ProjectByKey(selected); ok {
			return selected
		}
	}
	return nexus.Projects[0].Key
}

func apiNexusHealth(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	_ = root
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type row struct {
		ProjectKey string `json:"project_key"`
		TaskID     string `json:"task_id,omitempty"`
	}
	out := struct {
		ActiveCount int    `json:"active_count"`
		Light       string `json:"light"`
		ReadyCount  int    `json:"ready_count"`
		Running     []row  `json:"running"`
		UsagePct    *int   `json:"usage_pct,omitempty"`
		UsageHint   string `json:"usage_hint"`
	}{
		Light:     "green",
		UsageHint: "Usage metrics unavailable",
	}
	if nexus == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	for _, p := range nexus.Projects {
		if st, err := readRunState(p.StorageRoot); err == nil && st != nil && st.Running {
			out.ActiveCount++
			out.Running = append(out.Running, row{ProjectKey: p.Key, TaskID: st.TaskID})
		}
		var b Board
		if err := readYAMLFile(boardPath(p.StorageRoot), &b); err == nil {
			for _, t := range b.Tasks {
				if t.Status == StatusReady {
					out.ReadyCount++
				}
			}
		}
	}
	if out.ActiveCount > 0 {
		out.Light = "red"
	}
	out.UsagePct, out.UsageHint = codexTelemetrySnapshot()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}

func uiBoardNexus(w http.ResponseWriter, r *http.Request, root string, cfg Config, nexus *Nexus) {
	latest, _ := loadConfigOrDefault(root)
	cfg = latest
	selected := normalizeNexusProjectSelection(nexus, r.URL.Query().Get("project"))
	tpl := template.Must(template.New("nexus_dashboard").Parse(uiNexusDashboardHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	base := strings.TrimSpace(cfg.GitBaseBranch)
	if base == "" {
		base = "main"
	}
	_ = tpl.Execute(w, map[string]any{
		"Projects":        nexus.Projects,
		"SelectedProject": selected,
		"GitBaseBranch":   base,
		"HasGitHubToken":  strings.TrimSpace(cfg.GitHubToken) != "",
	})
}

func uiNexusBoardPanel(w http.ResponseWriter, r *http.Request, root string, cfg Config, nexus *Nexus) {
	if nexus == nil {
		http.Error(w, "nexus mode required", http.StatusInternalServerError)
		return
	}
	selected := normalizeNexusProjectSelection(nexus, r.URL.Query().Get("project"))
	latest, _ := loadConfigOrDefault(root)
	cfg = latest
	all := []Status{StatusBacklog, StatusReady, StatusActive, StatusReview, StatusDone}
	visible := parseVisibleColumns(r, cfg, all)
	visibleSet := map[Status]bool{}
	for _, s := range visible {
		visibleSet[s] = true
	}

	cols := map[Status][]nexusCard{}
	for _, p := range nexus.Projects {
		if selected != "" && p.Key != selected {
			continue
		}
		var b Board
		if err := readYAMLFile(boardPath(p.StorageRoot), &b); err != nil {
			continue
		}
		for _, t := range b.Tasks {
			if !visibleSet[t.Status] {
				continue
			}
			colorKey := defaultColorKeyForID(t.ID)
			lbl := ""
			if md, err := readTaskMD(p.StorageRoot, t.ID); err == nil {
				if k, ok := getTaskColorFromMD(md); ok {
					colorKey = k
				}
				if l, ok := getTaskPriorityFromMD(md); ok {
					lbl = l
				}
			}
			tc := *t
			cols[t.Status] = append(cols[t.Status], nexusCard{
				Task:          &tc,
				ProjectKey:    p.Key,
				ProjectName:   p.Name,
				ColorHex:      colorHexForKey(colorKey),
				PriorityLabel: lbl,
				RingHex:       ringHexForPriorityLabel(lbl),
			})
		}
	}

	for _, s := range all {
		sort.SliceStable(cols[s], func(i, j int) bool {
			a, b := cols[s][i], cols[s][j]
			if a.ProjectName != b.ProjectName {
				return a.ProjectName < b.ProjectName
			}
			return a.Task.ID < b.Task.ID
		})
	}

	runEnabled := selected != ""
	running := false
	runningTask := ""
	runningProject := ""
	agentName, agentTip := "multiple", "Select a project"
	if selected != "" {
		if p, ok := nexus.ProjectByKey(selected); ok {
			pcfg, _ := loadConfigOrDefault(p.StorageRoot)
			agentName, agentTip = agentUI(pcfg)
			if st, err := readRunState(p.StorageRoot); err == nil && st != nil && st.Running {
				running = true
				runningTask = st.TaskID
				runningProject = p.Key
			}
		}
	}

	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "compact" {
		statusCounts := map[string]int{}
		for _, s := range all {
			statusCounts[string(s)] = len(cols[s])
		}
		focus := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("focus")))
		validFocus := map[string]bool{
			"ALL":     true,
			"BACKLOG": true,
			"READY":   true,
			"ACTIVE":  true,
			"REVIEW":  true,
			"DONE":    true,
		}
		if !validFocus[focus] {
			focus = "ALL"
		}
		focusOrder := []Status{StatusActive, StatusReview, StatusReady, StatusBacklog, StatusDone}
		if focus != "ALL" {
			focusOrder = []Status{Status(focus)}
		}
		items := make([]nexusCompactItem, 0, 12)
		for _, s := range focusOrder {
			for _, c := range cols[s] {
				items = append(items, nexusCompactItem{
					ID:          c.Task.ID,
					Title:       c.Task.Title,
					Status:      c.Task.Status,
					ProjectKey:  c.ProjectKey,
					ProjectName: c.ProjectName,
					ColorHex:    c.ColorHex,
				})
				if len(items) >= 12 {
					break
				}
			}
			if len(items) >= 12 {
				break
			}
		}
		tpl := template.Must(template.New("nexus_board_compact").Parse(uiBoardNexusCompactHTML))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = tpl.Execute(w, map[string]any{
			"SelectedProject": selected,
			"Items":           items,
			"StatusCounts":    statusCounts,
			"Focus":           focus,
			"RunEnabled":      runEnabled,
			"Running":         running,
			"RunningTask":     runningTask,
		})
		return
	}

	tpl := template.Must(template.New("nexus_board").Parse(uiBoardNexusPanelHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tpl.Execute(w, map[string]any{
		"Title":           "Hazel Nexus",
		"Projects":        nexus.Projects,
		"SelectedProject": selected,
		"Columns":         cols,
		"Order":           visible,
		"AllStatuses":     all,
		"VisibleSet":      visibleSet,
		"ColCount":        len(visible),
		"CanCreate":       selected != "",
		"RunEnabled":      runEnabled,
		"Running":         running,
		"RunningTask":     runningTask,
		"RunningProject":  runningProject,
		"AgentName":       agentName,
		"AgentTip":        agentTip,
		"HazelURL":        hazelPoweredByURL(),
	})
}

func uiTaskNexus(w http.ResponseWriter, r *http.Request, cfg Config, nexus *Nexus) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/task/"), "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	projectKey, id := parts[0], parts[1]
	project, ok := nexus.ProjectByKey(projectKey)
	if !ok {
		http.NotFound(w, r)
		return
	}

	latest, _ := loadConfigOrDefault(project.StorageRoot)
	cfg = latest

	var b Board
	if err := readYAMLFile(boardPath(project.StorageRoot), &b); err != nil {
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
		p := taskFile(project.StorageRoot, id, name)
		b, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		return string(b)
	}

	tpl := template.Must(template.New("task").Parse(uiTaskHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	taskMD := read("task.md")
	priority := ""
	if p, ok := getTaskPriorityFromMD(taskMD); ok {
		priority = p
	}
	renderMD := func(src string) template.HTML {
		md := goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
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
	agentName, agentTip := agentUI(cfg)
	chatLabel := "Run in Chat"
	chatAutoRun := true
	chatSession := ""
	if ss, ok := latestChatSessionForTask(project.StorageRoot, task.ID); ok {
		chatLabel = "Open in Chat"
		chatAutoRun = false
		chatSession = strings.TrimSuffix(ss.Name, ".jsonl")
	}
	chatHref := "/chat?project=" + url.QueryEscape(projectKey) + "&task=" + url.QueryEscape(task.ID)
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
		"RingHex":     ringHexForPriorityLabel(priority),
		"Priority":    priority,
		"AllPrios":    []string{"", "HIGH", "MEDIUM", "LOW"},
		"Title":       project.Name,
		"RepoSlug":    project.RepoSlug,
		"HazelURL":    hazelPoweredByURL(),
		"AgentName":   agentName,
		"AgentTip":    agentTip,
		"Project":     projectKey,
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

func uiWiki(w http.ResponseWriter, r *http.Request, root string, nexus *Nexus) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	selected := strings.TrimSpace(r.URL.Query().Get("project"))
	projectName := "Wiki"
	wikiDir := filepath.Join(root, "wiki")
	projects := []TrackedProject{}
	if nexus != nil {
		selected = normalizeNexusProjectSelection(nexus, selected)
		project, ok := nexus.ProjectByKey(selected)
		if !ok {
			projectName = "Wiki"
			wikiDir = filepath.Join(root, "wiki")
		} else {
			projectName = project.Name
			wikiDir = filepath.Join(project.StorageRoot, "wiki")
		}
		projects = nexus.Projects
	} else {
		projectName = projectTitle(resolveRepoRoot(root))
	}
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "full"
	}
	type wikiNode struct {
		Rel     string
		Name    string
		Depth   int
		IsDir   bool
		Href    string
		Current bool
	}
	nodes := []wikiNode{}
	files := []string{}
	_ = filepath.WalkDir(wikiDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == wikiDir {
			return nil
		}
		rel, rerr := filepath.Rel(wikiDir, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		depth := strings.Count(rel, "/")
		name := filepath.Base(rel)
		n := wikiNode{
			Rel:   rel,
			Name:  name,
			Depth: depth,
			IsDir: d.IsDir(),
		}
		nodes = append(nodes, n)
		if !d.IsDir() {
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)

	selectedFile := strings.TrimSpace(r.URL.Query().Get("file"))
	if selectedFile == "" {
		for _, cand := range []string{"README.md", "FEATURES_AND_USAGE.md", "SOURCE_README.md", "CHANGELOG.md"} {
			if exists(filepath.Join(wikiDir, cand)) {
				selectedFile = cand
				break
			}
		}
	}
	if selectedFile == "" && len(files) > 0 {
		selectedFile = files[0]
	}
	selectedFile = filepath.ToSlash(filepath.Clean(selectedFile))
	if strings.HasPrefix(selectedFile, "../") || strings.Contains(selectedFile, "/../") {
		selectedFile = ""
	}
	selectedPath := filepath.Join(wikiDir, filepath.FromSlash(selectedFile))
	if selectedFile == "" || !exists(selectedPath) {
		if len(files) > 0 {
			selectedFile = files[0]
			selectedPath = filepath.Join(wikiDir, filepath.FromSlash(selectedFile))
		}
	}
	rendered := template.HTML("<p>No wiki files yet.</p>")
	if exists(selectedPath) {
		b, err := os.ReadFile(selectedPath)
		if err == nil {
			ext := strings.ToLower(filepath.Ext(selectedFile))
			if ext == ".md" || ext == ".markdown" {
				md := goldmark.New(
					goldmark.WithExtensions(extension.GFM),
					goldmark.WithParserOptions(parser.WithAutoHeadingID()),
					goldmark.WithRendererOptions(),
				)
				var sb strings.Builder
				if err := md.Convert(b, &sb); err == nil {
					rendered = template.HTML(sb.String())
				} else {
					rendered = template.HTML("<p>Unable to render markdown.</p>")
				}
			} else {
				rendered = template.HTML("<pre>" + template.HTMLEscapeString(string(b)) + "</pre>")
			}
		}
	}
	for i := range nodes {
		nodes[i].Current = !nodes[i].IsDir && nodes[i].Rel == selectedFile
		if !nodes[i].IsDir {
			nodes[i].Href = "/wiki?mode=" + url.QueryEscape(mode) + "&file=" + url.QueryEscape(nodes[i].Rel)
			if selected != "" {
				nodes[i].Href += "&project=" + url.QueryEscape(selected)
			}
			if strings.TrimSpace(r.URL.Query().Get("embed")) == "1" {
				nodes[i].Href += "&embed=1"
			}
		}
	}

	tpl := template.Must(template.New("wiki").Parse(uiWikiHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	embed := strings.TrimSpace(r.URL.Query().Get("embed")) == "1"
	_ = tpl.Execute(w, map[string]any{
		"Projects":        projects,
		"SelectedProject": selected,
		"ProjectName":     projectName,
		"Nodes":           nodes,
		"SelectedFile":    selectedFile,
		"SelectedHTML":    rendered,
		"Mode":            mode,
		"Embed":           embed,
	})
}

const uiNexusDashboardHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Hazel Nexus Dashboard</title>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&display=swap');
    :root { --bg:#102022; --panel:#193133; --text:#e7fbff; --accent:#13daec; --line:#326267; --warn:#facc15; }
    * { box-sizing:border-box; }
    body { margin:0; font-family:"Space Grotesk", ui-sans-serif, system-ui; background:var(--bg); color:var(--text); height:100dvh; overflow:hidden; display:flex; flex-direction:column; }
    body::before { content:""; position:fixed; inset:0; pointer-events:none; background:linear-gradient(rgba(19,218,236,.04) 50%, rgba(0,0,0,0) 50%); background-size:100% 4px; }
    header { border-bottom:1px solid var(--line); background: rgba(16,32,34,.96); position: sticky; top:0; z-index:10; }
    .bar { display:flex; gap:10px; align-items:center; flex-wrap:wrap; padding:12px 16px; }
    .status { margin-left:auto; display:flex; align-items:center; gap:8px; font-size:10px; text-transform:uppercase; letter-spacing:.08em; color:#a8d5db; }
    .dot { width:9px; height:9px; border-radius:50%; background:#38d18f; box-shadow:0 0 8px rgba(56,209,143,.8); }
    .dot.red { background:#ff5f5f; box-shadow:0 0 8px rgba(255,95,95,.8); }
    .usage { position:relative; display:flex; align-items:center; }
    .meter { width:70px; height:8px; border:1px solid var(--line); border-radius:99px; overflow:hidden; background:rgba(0,0,0,.25); }
    .meter > span { display:block; height:100%; width:0%; background:#38d18f; transition: width .2s ease, background-color .2s ease; }
    .meter.warn > span { background:#facc15; }
    .meter.crit > span { background:#ff5f5f; }
    .usage-tip { position:absolute; right:0; top:140%; min-width:240px; max-width:320px; border:1px solid var(--line); border-radius:6px; background:rgba(16,32,34,.98); color:var(--text); padding:8px 10px; box-shadow:0 8px 20px rgba(0,0,0,.35); opacity:0; transform:translateY(-3px); pointer-events:none; transition:opacity .14s ease, transform .14s ease; z-index:25; }
    .usage:hover .usage-tip, .usage:focus-within .usage-tip { opacity:1; transform:translateY(0); }
    .usage-title { font-size:10px; letter-spacing:.1em; text-transform:uppercase; color:var(--accent); margin-bottom:4px; }
    .usage-row { font-size:11px; color:#d9f9ff; text-transform:none; letter-spacing:0; line-height:1.35; }
    .usage-sub { margin-top:4px; font-size:10px; color:#97d4dd; text-transform:none; letter-spacing:0; line-height:1.35; white-space:pre-line; }
    .brand { color:var(--accent); letter-spacing:.14em; text-transform:uppercase; font-size:12px; font-weight:700; }
    .tabs { display:flex; gap:8px; align-items:center; flex-wrap:wrap; }
    .tab { border:1px solid var(--line); border-radius:4px; padding:7px 10px; color:#d9f9ff; text-decoration:none; text-transform:uppercase; font-size:10px; letter-spacing:.08em; }
    .tab.active { border-color:var(--accent); color:var(--accent); background:rgba(19,218,236,.1); }
    .cfg details { position:relative; }
    .cfg summary { list-style:none; cursor:pointer; border:1px solid var(--line); border-radius:4px; padding:5px 8px; color:#d9f9ff; font-size:10px; text-transform:uppercase; letter-spacing:.08em; }
    .cfg summary::-webkit-details-marker { display:none; }
    .cfgmenu { position:absolute; right:0; top:110%; min-width:320px; border:1px solid var(--line); border-radius:6px; background:rgba(16,32,34,.98); padding:10px; display:grid; gap:8px; z-index:40; }
    .cfgmenu label { display:grid; gap:4px; font-size:10px; text-transform:uppercase; color:#97d4dd; }
    .cfgmenu input { width:100%; background:rgba(0,0,0,.25); border:1px solid var(--line); color:var(--text); border-radius:4px; padding:7px 8px; font-size:12px; }
    .cfgmenu button { background:rgba(0,0,0,.2); border:1px solid var(--line); color:var(--text); border-radius:4px; padding:7px 8px; font-size:10px; text-transform:uppercase; cursor:pointer; }
    main { padding:10px; flex:1; overflow:hidden; }
    .grid { display:grid; grid-template-columns: repeat(2, minmax(0, 1fr)); grid-template-rows: repeat(2, minmax(0, 1fr)); gap:10px; height:100%; }
    .widget { border:1px solid var(--line); background: rgba(25,49,51,.35); border-radius:4px; display:flex; flex-direction:column; position:relative; min-height:0; max-height:1000px; opacity:1; transform:scale(1); transition:max-height .18s ease, opacity .18s ease, transform .18s ease, border-color .18s ease; overflow:hidden; }
    .widget.preview { background: linear-gradient(180deg, rgba(25,49,51,.6), rgba(12,22,24,.7)); }
    .grid.expanded .widget:not(.full) { opacity:0; transform:scale(.98); max-height:0; border-width:0; pointer-events:none; }
    .widget.full { grid-column: 1 / -1; grid-row: 1 / -1; border-color: var(--accent); }
    .wh { padding:8px 10px; border-bottom:1px solid var(--line); display:flex; justify-content:space-between; align-items:center; text-transform:uppercase; letter-spacing:.1em; font-size:10px; color:var(--accent); }
    .wh button { background: rgba(0,0,0,.2); border:1px solid var(--line); color:var(--text); border-radius:4px; padding:4px 8px; cursor:pointer; font-size:10px; text-transform:uppercase; }
    .wh button:hover { border-color:var(--accent); color:var(--accent); }
    iframe { border:0; width:100%; flex:1; background:transparent; }
    @media (max-width: 1024px) { body { overflow:auto; height:auto; } main { overflow:visible; } .grid { grid-template-columns:1fr; grid-template-rows:none; height:auto; } .widget { min-height:46vh; max-height:none; } .widget.full { grid-column:auto; grid-row:auto; } }
  </style>
</head>
<body>
  <header>
    <div class="bar">
      <div class="brand">Hazel Nexus</div>
      <nav class="tabs">
        {{range .Projects}}
          <a class="tab {{if eq $.SelectedProject .Key}}active{{end}}" href="/?project={{.Key}}">{{.Name}}</a>
        {{end}}
      </nav>
      <div class="status" id="hzStatus">
        <div class="cfg">
          <details>
            <summary>Config</summary>
            <form class="cfgmenu" action="/mutate/config" method="post">
              <input type="hidden" name="project" value="{{.SelectedProject}}" />
              <label>
                GitHub Token
                <input type="password" name="github_token" placeholder="{{if .HasGitHubToken}}saved (enter new to replace){{else}}ghp_xxx{{end}}" />
              </label>
              <label style="display:flex;align-items:center;gap:8px;text-transform:none;font-size:11px;color:#d9f9ff;">
                <input type="checkbox" name="clear_github_token" value="1" style="width:auto;" />
                Clear stored token
              </label>
              <label>
                Base Branch
                <input type="text" name="git_base_branch" value="{{.GitBaseBranch}}" />
              </label>
              <button type="submit">Save</button>
            </form>
          </details>
        </div>
        <span class="dot" id="hzDot"></span>
        <span id="hzCount">0 active</span>
        <div class="usage">
          <div class="meter" id="hzMeterWrap" aria-label="Usage meter"><span id="hzMeter"></span></div>
          <div class="usage-tip" id="hzUsageTip" role="status" aria-live="polite">
            <div class="usage-title">Codex Usage</div>
            <div class="usage-row" id="hzUsageRow">Loading...</div>
            <div class="usage-sub" id="hzUsageHint">Polling nexus health.</div>
          </div>
        </div>
      </div>
    </div>
  </header>
  <main>
    <section class="grid" id="hzGrid">
      <article class="widget preview" id="w-board">
        <div class="wh"><span>Task Board</span><button type="button" onclick="hzToggle('w-board')">Expand</button></div>
        <iframe data-compact-src="/panel/board?project={{.SelectedProject}}&mode=compact" data-full-src="/panel/board?project={{.SelectedProject}}" src="/panel/board?project={{.SelectedProject}}&mode=compact"></iframe>
      </article>
      <article class="widget" id="w-chat">
        <div class="wh"><span>Chat</span><button type="button" onclick="hzToggle('w-chat')">Expand</button></div>
        <iframe src="/chat?project={{.SelectedProject}}&embed=1"></iframe>
      </article>
      <article class="widget preview" id="w-wiki">
        <div class="wh"><span>Wiki</span><button type="button" onclick="hzToggle('w-wiki')">Expand</button></div>
        <iframe data-compact-src="/wiki?project={{.SelectedProject}}&embed=1&mode=compact" data-full-src="/wiki?project={{.SelectedProject}}&embed=1" src="/wiki?project={{.SelectedProject}}&embed=1&mode=compact"></iframe>
      </article>
      <article class="widget" id="w-history">
        <div class="wh"><span>History</span><button type="button" onclick="hzToggle('w-history')">Expand</button></div>
        <iframe src="/history?project={{.SelectedProject}}&embed=1"></iframe>
      </article>
    </section>
  </main>
  <script>
    let hzOpenID = "";
    function hzSamePathAndQuery(frameSrc, targetSrc) {
      try {
        const a = new URL(frameSrc, window.location.origin);
        const b = new URL(targetSrc, window.location.origin);
        return a.pathname + a.search === b.pathname + b.search;
      } catch (_) {
        return frameSrc === targetSrc;
      }
    }

    function hzSetFrameSrc(frame, target) {
      if (!frame || !target) return;
      if (!hzSamePathAndQuery(frame.getAttribute("src") || frame.src || "", target)) {
        frame.setAttribute("src", target);
      }
    }

    function hzApplyWidgetSources() {
      const grid = document.getElementById("hzGrid");
      if (!grid) return;
      const widgets = Array.from(grid.querySelectorAll(".widget"));
      widgets.forEach((w) => {
        const f = w.querySelector("iframe");
        if (!f) return;
        const target = (hzOpenID === w.id && f.dataset.fullSrc) ? f.dataset.fullSrc : f.dataset.compactSrc;
        hzSetFrameSrc(f, target);
      });
    }

    function hzOpen(id) {
      const grid = document.getElementById("hzGrid");
      const el = document.getElementById(id);
      if (!el || !grid) return;
      if (hzOpenID === id && el.classList.contains("full")) return;
      const widgets = Array.from(grid.querySelectorAll(".widget"));
      widgets.forEach((w) => {
        w.classList.remove("full");
        const b = w.querySelector(".wh button");
        if (b) b.textContent = "Expand";
      });
      el.classList.add("full");
      grid.classList.add("expanded");
      const btn = el.querySelector(".wh button");
      if (btn) btn.textContent = "Collapse";
      hzOpenID = id;
      hzApplyWidgetSources();
    }

    function hzToggle(id) {
      const grid = document.getElementById("hzGrid");
      const el = document.getElementById(id);
      if (!el || !grid) return;
      const widgets = Array.from(grid.querySelectorAll(".widget"));
      const opening = hzOpenID !== id;
      widgets.forEach((w) => {
        w.classList.remove("full");
        const b = w.querySelector(".wh button");
        if (b) b.textContent = "Expand";
      });
      if (opening) {
        hzOpen(id);
      } else {
        grid.classList.remove("expanded");
        hzOpenID = "";
        hzApplyWidgetSources();
      }
    }

    function hzOpenChatFromMessage(d) {
      const chatWidget = document.getElementById("w-chat");
      if (!chatWidget) return;
      const chatFrame = chatWidget.querySelector("iframe");
      if (!chatFrame) return;
      const u = new URL(chatFrame.src || "/chat", window.location.origin);
      u.searchParams.set("embed", "1");
      if (d.project) u.searchParams.set("project", d.project); else u.searchParams.delete("project");
      if (d.task) u.searchParams.set("task", d.task); else u.searchParams.delete("task");
      if (d.session) u.searchParams.set("session", d.session); else u.searchParams.delete("session");
      if (d.autorun) u.searchParams.set("autorun", "1"); else u.searchParams.delete("autorun");
      chatFrame.src = u.pathname + "?" + u.searchParams.toString();
      hzOpen("w-chat");
    }

    window.addEventListener("message", (event) => {
      const d = event && event.data;
      if (!d) return;
      if (d.type === "hazel-open-chat-task" || d.type === "hazel-open-chat-session") {
        hzOpenChatFromMessage(d);
      }
    });

    async function hzPollHealth() {
      try {
        const res = await fetch('/api/nexus/health');
        if (!res.ok) return;
        const js = await res.json();
        const dot = document.getElementById('hzDot');
        const count = document.getElementById('hzCount');
        if (dot) dot.classList.toggle('red', (js.light || 'green') === 'red');
        if (count) count.textContent = (js.active_count || 0) + ' active / ' + (js.ready_count || 0) + ' ready';
        const meter = document.getElementById('hzMeter');
        const meterWrap = document.getElementById('hzMeterWrap');
        const usageRow = document.getElementById('hzUsageRow');
        const usageHint = document.getElementById('hzUsageHint');
        const pct = (js.usage_pct === null || js.usage_pct === undefined) ? 0 : js.usage_pct;
        const usedPct = Math.max(0, Math.min(100, pct));
        const remainingPct = 100 - usedPct;
        if (meter) meter.style.width = remainingPct + '%';
        if (meterWrap) {
          meterWrap.classList.remove('warn', 'crit');
          if (remainingPct <= 20) {
            meterWrap.classList.add('crit');
          } else if (remainingPct <= 50) {
            meterWrap.classList.add('warn');
          }
          meterWrap.setAttribute('aria-label', 'Remaining capacity ' + remainingPct + '%');
        }
        if (usageRow) usageRow.textContent = 'Used ' + usedPct + '% | Remaining ' + remainingPct + '%';
        if (usageHint) usageHint.textContent = js.usage_hint || 'Usage metrics unavailable';
      } catch (_) {}
    }
    hzApplyWidgetSources();
    window.addEventListener("pageshow", hzApplyWidgetSources);
    hzPollHealth();
    setInterval(hzPollHealth, 3000);
  </script>
</body>
</html>`

const uiBoardNexusPanelHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Title}}</title>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&display=swap');
    :root { --bg:#102022; --panel:#193133; --text:#e7fbff; --accent:#13daec; --line:#326267; --warn:#facc15; }
    * { box-sizing:border-box; }
    body { margin:0; font-family: "Space Grotesk", ui-sans-serif, system-ui; background:var(--bg); color:var(--text); height:100dvh; overflow:hidden; display:flex; flex-direction:column; }
    body::before { content:""; position:fixed; inset:0; pointer-events:none; background:linear-gradient(rgba(19,218,236,.04) 50%, rgba(0,0,0,0) 50%); background-size:100% 4px; }
    .actions { display:flex; gap:8px; align-items:center; flex-wrap:wrap; padding:8px 10px; border-bottom:1px solid var(--line); background:rgba(16,32,34,.7); }
    .actions form { margin:0; display:flex; gap:8px; align-items:center; }
    .board { display:grid; gap:10px; grid-template-columns: repeat(var(--cols), minmax(260px, 1fr)); padding:10px; flex:1; overflow:auto; }
    .col { background: rgba(25,49,51,.35); border:1px solid var(--line); border-radius:4px; padding:10px; min-height: 0; }
    .col h2 { margin:0 0 10px; font-size:11px; letter-spacing:.12em; text-transform:uppercase; color: var(--accent); display:flex; justify-content:space-between; border-bottom:1px solid var(--line); padding-bottom:6px; }
    .card { padding:10px; margin:8px 0; border-radius:4px; border:1px solid var(--line); background: linear-gradient(180deg, rgba(0,0,0,.45), rgba(0,0,0,.45)), var(--cardbg, rgba(15,24,48,.92)); box-shadow: inset 0 0 0 2px var(--ring, rgba(255,255,255,0)); }
    .id a { color: var(--accent); text-decoration:none; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size:11px; font-weight:700; }
    .title { margin-top:8px; font-size:12px; color:#d9f9ff; }
    .meta { margin-top:10px; display:flex; gap:8px; align-items:center; flex-wrap:wrap; }
    .meta form { margin:0; display:flex; gap:6px; align-items:center; }
    .pill { border:1px solid var(--line); padding:2px 8px; border-radius:4px; font-size:10px; background: rgba(0,0,0,.2); text-transform:uppercase; }
    select, input, button { background: rgba(0,0,0,.25); border:1px solid var(--line); color: var(--text); padding:7px 9px; border-radius:4px; font-size:11px; }
    button:hover, .tab:hover { border-color:var(--accent); color:var(--accent); }
    .hint { color: #8dc7cf; font-size:10px; text-transform:uppercase; letter-spacing:.08em; }
  </style>
</head>
<body>
  <div class="actions">
      {{if .CanCreate}}
        <form action="/mutate/new_task" method="post">
          <input type="hidden" name="project" value="{{.SelectedProject}}" />
          <input type="text" name="title" placeholder="Task title" required />
          <button type="submit">Create task</button>
        </form>
      {{else}}
        <span class="hint">Select a project tab to create tasks or run agents.</span>
      {{end}}
      {{if .RunEnabled}}
        {{if .Running}}
          <span class="pill">Background run: {{.RunningTask}}</span>
        {{else}}
          <span class="pill">Background queue idle</span>
        {{end}}
      {{end}}
      <span class="pill" {{if .AgentTip}}title="{{.AgentTip}}"{{end}}>Agent: {{.AgentName}}</span>
  </div>
  <main>
    <div class="board" style="--cols: {{.ColCount}};">
      {{range .Order}}
        {{$status := .}}
        {{$tasks := index $.Columns $status}}
        <section class="col">
          <h2><span>{{$status}}</span><span>{{len $tasks}}</span></h2>
          {{range $tasks}}
            <div class="card" style="--cardbg: {{.ColorHex}}; --ring: {{.RingHex}};">
              <div class="id"><a href="/task/{{.ProjectKey}}/{{.Task.ID}}">{{.Task.ID}}</a></div>
              <div class="title">{{.Task.Title}}</div>
              <div class="meta">
                <span class="pill">{{.ProjectName}}</span>
                <form action="/mutate/status" method="post">
                  <input type="hidden" name="project" value="{{.ProjectKey}}" />
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
                  <input type="hidden" name="project" value="{{.ProjectKey}}" />
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
  <script>
    function hazelSubmit(form) {
      const fd = new FormData(form);
      fetch(form.action, { method: "POST", body: new URLSearchParams(fd), headers: { "X-Hazel-Ajax": "1" } })
        .then(() => location.reload());
    }
  </script>
</body>
</html>`

const uiBoardNexusCompactHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Board Preview</title>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&display=swap');
    :root { --bg:#102022; --line:#326267; --accent:#13daec; --warn:#facc15; --text:#e7fbff; }
    * { box-sizing:border-box; }
    body { margin:0; font-family:"Space Grotesk",ui-sans-serif,system-ui; color:var(--text); background:transparent; min-height:100dvh; display:flex; flex-direction:column; }
    .head { display:flex; gap:8px; align-items:center; padding:8px; border-bottom:1px solid var(--line); flex-wrap:wrap; }
    .pill { border:1px solid var(--line); border-radius:4px; padding:3px 6px; font-size:10px; text-transform:uppercase; color:var(--text); text-decoration:none; }
    .pill.active { border-color:var(--accent); color:var(--accent); }
    .list { padding:8px; overflow:auto; flex:1; }
    .item { border:1px solid var(--line); border-radius:4px; padding:8px; margin-bottom:6px; background:rgba(0,0,0,.25); }
    .item a { color:var(--accent); text-decoration:none; font-size:11px; font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace; }
    .title { margin-top:4px; font-size:12px; color:#d9f9ff; }
    .meta { margin-top:6px; display:flex; gap:6px; flex-wrap:wrap; }
    .empty { padding:12px; font-size:12px; color:#9dc9d0; }
  </style>
</head>
<body>
  <div class="head">
    <a class="pill {{if eq .Focus "ALL"}}active{{end}}" href="/panel/board?project={{.SelectedProject}}&mode=compact&focus=ALL">All</a>
    <a class="pill {{if eq .Focus "BACKLOG"}}active{{end}}" href="/panel/board?project={{.SelectedProject}}&mode=compact&focus=BACKLOG">Backlog {{index .StatusCounts "BACKLOG"}}</a>
    <a class="pill {{if eq .Focus "READY"}}active{{end}}" href="/panel/board?project={{.SelectedProject}}&mode=compact&focus=READY">Ready {{index .StatusCounts "READY"}}</a>
    <a class="pill {{if eq .Focus "ACTIVE"}}active{{end}}" href="/panel/board?project={{.SelectedProject}}&mode=compact&focus=ACTIVE">Active {{index .StatusCounts "ACTIVE"}}</a>
    <a class="pill {{if eq .Focus "REVIEW"}}active{{end}}" href="/panel/board?project={{.SelectedProject}}&mode=compact&focus=REVIEW">Review {{index .StatusCounts "REVIEW"}}</a>
    <a class="pill {{if eq .Focus "DONE"}}active{{end}}" href="/panel/board?project={{.SelectedProject}}&mode=compact&focus=DONE">Done {{index .StatusCounts "DONE"}}</a>
    {{if .Running}}<span class="pill" style="border-color:var(--warn);color:var(--warn);">Running {{.RunningTask}}</span>{{end}}
  </div>
  <div class="list">
    {{if .Items}}
      {{range .Items}}
        <div class="item" style="border-left:3px solid {{.ColorHex}};">
          <a href="/task/{{.ProjectKey}}/{{.ID}}">{{.ID}}</a>
          <div class="title">{{.Title}}</div>
          <div class="meta">
            <span class="pill">{{.ProjectName}}</span>
            <span class="pill">{{.Status}}</span>
          </div>
        </div>
      {{end}}
    {{else}}
      <div class="empty">No tasks in visible columns.</div>
    {{end}}
  </div>
</body>
</html>`

const uiWikiHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Wiki</title>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;700&display=swap');
    :root { --bg:#102022; --line:#326267; --accent:#13daec; --text:#e7fbff; --panel:rgba(25,49,51,.35); }
    * { box-sizing:border-box; }
    body { margin:0; font-family:"Space Grotesk", ui-sans-serif, system-ui; background:var(--bg); color:var(--text); min-height:100dvh; display:flex; flex-direction:column; }
    body::before { content:""; position:fixed; inset:0; pointer-events:none; background:linear-gradient(rgba(19,218,236,.04) 50%, rgba(0,0,0,0) 50%); background-size:100% 4px; }
    header { padding:10px 16px; border-bottom:1px solid var(--line); background: rgba(16,32,34,.95); position: sticky; top:0; z-index:10; }
    a { color:var(--accent); text-decoration:none; font-size:11px; text-transform:uppercase; }
    .tabs { margin-top:10px; display:flex; gap:8px; flex-wrap:wrap; }
    .tab { border:1px solid var(--line); border-radius:4px; padding:7px 10px; color:#d9f9ff; }
    .tab.active { border-color:var(--accent); color:var(--accent); background:rgba(19,218,236,.1); }
    main { padding:10px; flex:1; min-height:0; }
    .layout { height:100%; display:grid; grid-template-columns: 280px 1fr; gap:10px; min-height:0; }
    .tree, .doc { background:var(--panel); border:1px solid var(--line); border-radius:4px; min-height:0; }
    .tree { overflow:auto; padding:10px; }
    .doc { display:flex; flex-direction:column; overflow:hidden; }
    .dochead { border-bottom:1px solid var(--line); padding:8px 10px; font-size:11px; text-transform:uppercase; color:#97d4dd; letter-spacing:.08em; }
    .docbody { padding:14px 16px; overflow:auto; }
    .node { display:block; margin:3px 0; border-radius:4px; padding:4px 6px; color:#cfeff4; text-decoration:none; font-size:12px; }
    .node.dir { color:#8cc9d1; text-transform:uppercase; font-size:10px; letter-spacing:.08em; cursor:default; }
    .node.file:hover { background:rgba(255,255,255,.06); }
    .node.file.active { border:1px solid var(--accent); color:var(--accent); background:rgba(19,218,236,.08); }
    .md a { color:var(--accent); }
    .md code { background: rgba(255,255,255,.08); padding:1px 5px; border-radius:6px; }
    .md pre { background: rgba(0,0,0,.3); padding:10px 12px; border-radius:4px; overflow:auto; }
    .compact main { padding:8px; }
    .compact .layout { grid-template-columns: 1fr; }
    .compact .tree { max-height:140px; }
    @media (max-width: 960px) { .layout { grid-template-columns:1fr; } .tree { max-height:180px; } }
  </style>
</head>
<body class="{{if eq .Mode "compact"}}compact{{end}}">
  {{if not .Embed}}
  <header>
    <a href="/?project={{.SelectedProject}}">Back to board</a>
    <h1>{{.ProjectName}} Wiki</h1>
  </header>
  {{end}}
  <main>
    <section class="layout">
      <aside class="tree">
        {{range .Nodes}}
          {{if .IsDir}}
            <div class="node dir" style="padding-left: {{printf "%d" .Depth}}em;">{{.Name}}/</div>
          {{else}}
            <a class="node file {{if .Current}}active{{end}}" style="padding-left: {{printf "%d" .Depth}}em;" href="{{.Href}}">{{.Name}}</a>
          {{end}}
        {{end}}
      </aside>
      <article class="doc">
        <div class="dochead">{{.SelectedFile}}</div>
        <div class="docbody md">{{.SelectedHTML}}</div>
      </article>
    </section>
  </main>
</body>
</html>`
