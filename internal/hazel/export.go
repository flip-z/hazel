package hazel

import (
	"context"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

func ExportHTML(ctx context.Context, root string, outDir string) error {
	_ = ctx
	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		return err
	}
	if err := b.Validate(); err != nil {
		return err
	}
	if err := ensureDir(outDir); err != nil {
		return err
	}
	if err := ensureDir(filepath.Join(outDir, "tasks")); err != nil {
		return err
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)

	cols := map[Status][]*BoardTask{}
	for _, t := range b.Tasks {
		cols[t.Status] = append(cols[t.Status], t)
	}
	for _, s := range []Status{StatusBacklog, StatusReady, StatusActive, StatusReview, StatusDone} {
		sort.SliceStable(cols[s], func(i, j int) bool { return cols[s][i].ID < cols[s][j].ID })
	}

	funcs := template.FuncMap{
		"intp": func(p *int) string {
			if p == nil {
				return ""
			}
			return fmtInt(*p)
		},
	}
	indexT := template.Must(template.New("index").Funcs(funcs).Parse(exportIndexHTML))
	taskT := template.Must(template.New("task").Funcs(funcs).Parse(exportTaskHTML))

	if err := writeHTML(filepath.Join(outDir, "index.html"), indexT, map[string]any{
		"Columns": cols,
		"Order":   []Status{StatusBacklog, StatusReady, StatusActive, StatusReview, StatusDone},
	}); err != nil {
		return err
	}

	for _, t := range b.Tasks {
		td := taskDir(root, t.ID)
		render := func(name string) template.HTML {
			p := filepath.Join(td, name)
			b, err := os.ReadFile(p)
			if err != nil {
				return ""
			}
			var sb strings.Builder
			if err := md.Convert(b, &sb); err != nil {
				return ""
			}
			return template.HTML(sb.String()) // rendered markdown
		}
		if err := writeHTML(filepath.Join(outDir, "tasks", t.ID+".html"), taskT, map[string]any{
			"Task":   t,
			"TaskMD": render("task.md"),
			"ImplMD": render("impl.md"),
		}); err != nil {
			return err
		}
	}

	return nil
}

func writeHTML(path string, t *template.Template, data any) error {
	var sb strings.Builder
	if err := t.Execute(&sb, data); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(sb.String()), 0o644)
}

func fmtInt(v int) string {
	// Avoid fmt import for one function.
	if v == 0 {
		return "0"
	}
	neg := false
	if v < 0 {
		neg = true
		v = -v
	}
	var buf [32]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + (v % 10))
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

const exportIndexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Hazel Board</title>
  <style>
    :root { --bg:#0b1020; --panel:#101a33; --card:#0f1830; --text:#e9eefc; --muted:#aab4d6; --accent:#86f7c5; --warn:#ffcc66; }
    * { box-sizing:border-box; }
    body { margin:0; font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, Arial; background: radial-gradient(1200px 500px at 20% 0%, #14214a 0%, var(--bg) 60%); color:var(--text); }
    header { padding:20px 24px; border-bottom:1px solid rgba(255,255,255,.08); background: rgba(16,26,51,.6); backdrop-filter: blur(8px); position: sticky; top:0; }
    header h1 { margin:0; font-size:16px; letter-spacing:.08em; text-transform:uppercase; color:var(--muted); }
    main { padding:18px 18px 28px; }
    .board { display:grid; gap:12px; grid-template-columns: repeat(6, minmax(220px, 1fr)); overflow-x:auto; padding-bottom:12px; }
    .col { background: rgba(16,26,51,.85); border:1px solid rgba(255,255,255,.08); border-radius:12px; padding:10px; min-height: 70vh; }
    .col h2 { margin:4px 6px 10px; font-size:12px; letter-spacing:.12em; text-transform:uppercase; color:var(--muted); display:flex; justify-content:space-between; }
    .count { font-size:11px; color: rgba(233,238,252,.65); }
    .card { display:block; padding:10px 10px; margin:8px 4px; border-radius:10px; border:1px solid rgba(255,255,255,.09); background: rgba(15,24,48,.92); text-decoration:none; color:var(--text); }
    .card:hover { border-color: rgba(134,247,197,.55); box-shadow: 0 0 0 3px rgba(134,247,197,.08); }
    .id { font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size:12px; color: rgba(233,238,252,.75); }
    .title { margin-top:6px; font-size:13px; line-height:1.35; }
    .meta { margin-top:8px; font-size:11px; color: rgba(233,238,252,.55); display:flex; gap:8px; }
    .pill { border:1px solid rgba(255,255,255,.12); padding:2px 6px; border-radius:999px; }
  </style>
</head>
<body>
  <header><h1>Hazel Board (Static Export)</h1></header>
  <main>
    <div class="board">
      {{range .Order}}
        {{$status := .}}
        {{$tasks := index $.Columns $status}}
        <section class="col">
          <h2><span>{{$status}}</span><span class="count">{{len $tasks}}</span></h2>
          {{range $tasks}}
            <a class="card" href="tasks/{{.ID}}.html">
              <div class="id">{{.ID}}</div>
              <div class="title">{{.Title}}</div>
              <div class="meta">
                {{if .Order}}<span class="pill">o{{intp .Order}}</span>{{end}}
              </div>
            </a>
          {{end}}
        </section>
      {{end}}
    </div>
  </main>
</body>
</html>`

const exportTaskHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{.Task.ID}} - {{.Task.Title}}</title>
  <style>
    :root { --bg:#0b1020; --panel:#101a33; --text:#e9eefc; --muted:#aab4d6; --link:#86f7c5; }
    * { box-sizing:border-box; }
    body { margin:0; font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, Arial; background: radial-gradient(1200px 500px at 20% 0%, #14214a 0%, var(--bg) 60%); color:var(--text); }
    header { padding:16px 24px; border-bottom:1px solid rgba(255,255,255,.08); background: rgba(16,26,51,.6); backdrop-filter: blur(8px); position: sticky; top:0; }
    header a { color: var(--link); text-decoration:none; font-size:12px; }
    h1 { margin:6px 0 0; font-size:18px; }
    .meta { margin-top:6px; color: rgba(233,238,252,.6); font-size:12px; }
    main { padding: 18px 18px 28px; max-width: 980px; margin:0 auto; }
    .panel { background: rgba(16,26,51,.85); border:1px solid rgba(255,255,255,.08); border-radius:12px; padding:14px 16px; margin-bottom:14px; }
    .panel h2 { margin:0 0 10px; font-size:12px; letter-spacing:.12em; text-transform:uppercase; color: var(--muted); }
    .md :is(h1,h2,h3){ margin-top: 16px; }
    .md a { color: var(--link); }
    .md code { background: rgba(255,255,255,.08); padding: 1px 5px; border-radius:6px; }
    .md pre { background: rgba(255,255,255,.06); padding: 10px 12px; border-radius:10px; overflow:auto; }
  </style>
</head>
<body>
  <header>
    <a href="../index.html">Back to board</a>
    <h1>{{.Task.ID}}: {{.Task.Title}}</h1>
    <div class="meta">Status: {{.Task.Status}} | Updated: {{.Task.UpdatedAt}}</div>
  </header>
  <main>
    <section class="panel"><h2>task.md</h2><div class="md">{{.TaskMD}}</div></section>
    <section class="panel"><h2>impl.md</h2><div class="md">{{.ImplMD}}</div></section>
  </main>
</body>
</html>`
