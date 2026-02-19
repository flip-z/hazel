package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flip-z/hazel/internal/hazel"
)

func Run(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(os.Stdout)
		return 0
	}

	cmd := args[0]
	switch cmd {
	case "init":
		return cmdInit(ctx, args[1:])
	case "doctor":
		return cmdDoctor(ctx, args[1:])
	case "run":
		return cmdRun(ctx, args[1:])
	case "archive":
		return cmdArchive(ctx, args[1:])
	case "export":
		return cmdExport(ctx, args[1:])
	case "up":
		return cmdUp(ctx, args[1:])
	case "down":
		return cmdDown(ctx, args[1:])
	case "plan":
		return cmdPlan(ctx, args[1:])
	case "sync-wiki":
		return cmdSyncWiki(ctx, args[1:])
	case "config":
		return cmdConfig(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage(os.Stderr)
		return 2
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "hazel - filesystem-first project work queue")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  hazel init <path> [--projects-root DIR]")
	fmt.Fprintln(w, "  hazel up")
	fmt.Fprintln(w, "  hazel down")
	fmt.Fprintln(w, "  hazel run")
	fmt.Fprintln(w, "  hazel plan HZ-0001")
	fmt.Fprintln(w, "  hazel sync-wiki [--project KEY]")
	fmt.Fprintln(w, "  hazel config [--github-token TOKEN] [--clear-github-token] [--git-base-branch BRANCH]")
	fmt.Fprintln(w, "  hazel export --html [--chatgpt-project]")
	fmt.Fprintln(w, "  hazel archive [--before DATE]")
	fmt.Fprintln(w, "  hazel doctor")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Nexus layout:")
	fmt.Fprintln(w, "  .hazel/config.yaml")
	fmt.Fprintln(w, "  .hazel/projects/<project-key>/.hazel/board.yaml")
	fmt.Fprintln(w, "  .hazel/projects/<project-key>/.hazel/tasks/HZ-0001/{task.md,impl.md,plan.md}")
	fmt.Fprintln(w)
}

func cmdInit(ctx context.Context, args []string) int {
	var positional []string
	var flagArgs []string
	expectValue := false
	for _, a := range args {
		if expectValue {
			flagArgs = append(flagArgs, a)
			expectValue = false
			continue
		}
		if a == "-agent" || a == "--agent" || a == "-projects-root" || a == "--projects-root" {
			flagArgs = append(flagArgs, a)
			expectValue = true
			continue
		}
		if strings.HasPrefix(a, "--agent=") || strings.HasPrefix(a, "-agent=") || strings.HasPrefix(a, "--projects-root=") || strings.HasPrefix(a, "-projects-root=") {
			flagArgs = append(flagArgs, a)
			continue
		}
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			continue
		}
		positional = append(positional, a)
	}

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agent := fs.String("agent", "", "agent preset: codex|claude|none (if empty, may prompt)")
	nonInteractive := fs.Bool("non-interactive", false, "do not prompt; treat empty --agent as none")
	projectsRoot := fs.String("projects-root", "", "optional directory to scan for tracked git repositories (nexus mode)")
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: hazel init <path> [--projects-root DIR]")
		return 2
	}

	rootArg := strings.TrimSpace(positional[0])
	root, err := filepath.Abs(rootArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := hazel.ValidateInitRoot(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	preset := strings.ToLower(strings.TrimSpace(*agent))
	if preset == "" && !*nonInteractive {
		preset = promptAgentPreset()
	}
	if preset == "" {
		preset = "none"
	}
	pr := strings.TrimSpace(*projectsRoot)
	if pr == "" {
		// Default to scanning the nexus path itself for tracked repos.
		pr = "."
	}
	if err := hazel.InitRepo(ctx, root, hazel.InitOptions{
		AgentPreset:     preset,
		ProjectsRootDir: pr,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := hazel.SetActiveRoot(root); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: failed to set active root: %v\n", err)
	}
	return 0
}

func promptAgentPreset() string {
	if st, err := os.Stdin.Stat(); err != nil || (st.Mode()&os.ModeCharDevice) == 0 {
		return ""
	}
	fmt.Println("Select agent preset:")
	fmt.Println("  1) codex")
	fmt.Println("  2) claude")
	fmt.Println("  3) none")
	fmt.Print("> ")
	var s string
	_, _ = fmt.Fscanln(os.Stdin, &s)
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "1", "codex":
		return "codex"
	case "2", "claude":
		return "claude"
	case "3", "none", "":
		return "none"
	default:
		return "none"
	}
}

func cmdDoctor(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root, err := resolveCommandRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	report, err := hazel.Doctor(ctx, root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(report.Problems) == 0 {
		fmt.Println("OK")
		return 0
	}

	for _, p := range report.Problems {
		fmt.Println("PROBLEM:", p)
	}
	for _, w := range report.Warnings {
		fmt.Println("WARN:", w)
	}
	return 1
}

func cmdRun(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dry := fs.Bool("dry-run", false, "do not modify files or run agent command")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root, err := resolveCommandRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	res, err := hazel.RunTick(ctx, root, hazel.RunOptions{DryRun: *dry})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if res.DispatchedTaskID == "" {
		fmt.Println("No READY tasks")
		return 0
	}
	fmt.Printf("Dispatched %s\n", res.DispatchedTaskID)
	if res.AgentExitCode != nil {
		fmt.Printf("Agent exit: %d\n", *res.AgentExitCode)
	}
	if res.RunLogPath != "" {
		fmt.Printf("Run log: %s\n", res.RunLogPath)
	}
	return 0
}

func cmdArchive(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("archive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	beforeStr := fs.String("before", "", "archive DONE tasks updated before DATE (YYYY-MM-DD)")
	dry := fs.Bool("dry-run", false, "do not modify files")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var before *time.Time
	if strings.TrimSpace(*beforeStr) != "" {
		t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(*beforeStr), time.Local)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --before date: %v\n", err)
			return 2
		}
		before = &t
	}

	root, err := resolveCommandRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	res, err := hazel.ArchiveDone(ctx, root, hazel.ArchiveOptions{Before: before, DryRun: *dry})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Archived %d tasks\n", len(res.ArchivedIDs))
	for _, id := range res.ArchivedIDs {
		fmt.Println(" -", id)
	}
	return 0
}

func cmdExport(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	html := fs.Bool("html", false, "export static HTML")
	chatgpt := fs.Bool("chatgpt-project", false, "export markdown bundle for ChatGPT Projects")
	out := fs.String("out", "", "output directory (default: .hazel/export)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*html && !*chatgpt {
		fmt.Fprintln(os.Stderr, "export supports --html or --chatgpt-project")
		return 2
	}

	root, err := resolveCommandRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	outDir := *out
	if strings.TrimSpace(outDir) == "" {
		outDir = filepath.Join(root, ".hazel", "export")
	} else if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(root, outDir)
	}

	if *html {
		if err := hazel.ExportHTML(ctx, root, outDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Exported HTML to %s\n", outDir)
	}
	if *chatgpt {
		bundleOut := filepath.Join(outDir, "chatgpt-project")
		if err := hazel.ExportChatGPTBundle(ctx, root, bundleOut); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Exported ChatGPT bundle to %s\n", bundleOut)
	}
	return 0
}

func cmdUp(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	port := fs.Int("port", 0, "override config port")
	foreground := fs.Bool("foreground", false, "run in foreground (default: background)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root, err := resolveCommandRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if !*foreground {
		pid, addr, err := hazel.SpawnBackgroundServer(ctx, root, hazel.SpawnOptions{
			PortOverride: *port,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Started (pid %d) on %s\n", pid, addr)
		return 0
	}

	addr, err := hazel.Up(ctx, root, hazel.UpOptions{PortOverride: *port})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Listening on %s\n", addr)
	<-ctx.Done()
	return 0
}

func cmdDown(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "send SIGKILL if needed")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root, err := resolveCommandRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	res, err := hazel.Down(ctx, root, hazel.DownOptions{Force: *force})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !res.WasRunning {
		fmt.Println("Not running")
		return 0
	}
	fmt.Printf("Stopped pid %d\n", res.PID)
	return 0
}

func cmdPlan(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	tail := fs.Int("tail", 40, "print last N lines from run log (0 to disable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: hazel plan [--tail N] HZ-0001")
		return 2
	}
	id := fs.Arg(0)

	root, err := resolveCommandRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	res, err := hazel.Plan(ctx, root, id)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Planned %s\n", id)
	if res.AgentExitCode != nil {
		fmt.Printf("Agent exit: %d\n", *res.AgentExitCode)
	}
	if res.RunLogPath != "" {
		fmt.Printf("Run log: %s\n", res.RunLogPath)
		if *tail > 0 {
			if s, err := tailFileLines(res.RunLogPath, *tail, 128*1024); err == nil && strings.TrimSpace(s) != "" {
				fmt.Println()
				fmt.Println("---- run log (tail) ----")
				fmt.Print(s)
				if s[len(s)-1] != '\n' {
					fmt.Println()
				}
			}
		}
	}
	return 0
}

func cmdSyncWiki(ctx context.Context, args []string) int {
	_ = ctx
	fs := flag.NewFlagSet("sync-wiki", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "optional tracked project key to refresh")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root, err := resolveCommandRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	n, err := hazel.SyncWiki(root, strings.TrimSpace(*project))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Refreshed wiki for %d project(s)\n", n)
	return 0
}

func cmdConfig(ctx context.Context, args []string) int {
	_ = ctx
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	token := fs.String("github-token", "", "GitHub token for PR automation")
	clearToken := fs.Bool("clear-github-token", false, "remove stored github token")
	baseBranch := fs.String("git-base-branch", "", "default base branch for task PR flow")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*token) == "" && !*clearToken && strings.TrimSpace(*baseBranch) == "" {
		fmt.Fprintln(os.Stderr, "usage: hazel config [--github-token TOKEN] [--clear-github-token] [--git-base-branch BRANCH]")
		return 2
	}

	root, err := resolveCommandRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	upd := hazel.ConfigUpdate{
		ClearGitHubToken: *clearToken,
	}
	if strings.TrimSpace(*token) != "" {
		t := strings.TrimSpace(*token)
		upd.GitHubToken = &t
	}
	if strings.TrimSpace(*baseBranch) != "" {
		b := strings.TrimSpace(*baseBranch)
		upd.GitBaseBranch = &b
	}
	if err := hazel.UpdateConfig(root, upd); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Updated config")
	return 0
}

func resolveCommandRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	if hasHazelConfig(abs) {
		return abs, nil
	}
	if ar, err := hazel.GetActiveRoot(); err == nil && hasHazelConfig(ar) {
		return ar, nil
	}
	return "", fmt.Errorf("no hazel config in current directory (%s) and no active root is configured; run: hazel init <path>", abs)
}

func hasHazelConfig(root string) bool {
	p := filepath.Join(root, ".hazel", "config.yaml")
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
