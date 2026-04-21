package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// osFS is the real filesystem adapter, rooted at a base directory.
type osFS struct {
	root string
}

func (o osFS) abs(p string) string { return filepath.Join(o.root, p) }

func (o osFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(o.abs(path)) }

func (o osFS) Exists(path string) bool {
	_, err := os.Stat(o.abs(path))
	return err == nil
}

func (o osFS) ListDir(path string) ([]string, error) {
	entries, err := os.ReadDir(o.abs(path))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "init":
		if err := runInit(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "hatch: "+err.Error())
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "hatch: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`hatch - PR preview deployments, self-hosted

Usage:
  hatch init [flags]

Flags:
  --dry-run         print generated YAML to stdout, don't write
  --force           overwrite an existing .hatch.yml
  --output <path>   output path (default: .hatch.yml)
  --verbose         print detection details
  --no-animation    disable step delays (still colored if TTY)`)
}

func runInit(argv []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print YAML to stdout")
	force := fs.Bool("force", false, "overwrite existing .hatch.yml")
	output := fs.String("output", ".hatch.yml", "output path")
	verbose := fs.Bool("verbose", false, "verbose logs")
	noAnim := fs.Bool("no-animation", false, "disable step delays")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	fs.BoolVar(yes, "y", false, "skip confirmation prompt (short)")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get cwd: %w", err)
	}

	ui := NewUI(os.Stdout, *noAnim)
	ui.Header()

	filesys := osFS{root: cwd}

	ui.Step("Scanning project…")

	result, err := Detect(filesys)
	if err != nil {
		ui.Err(err.Error())
		return err
	}

	// Post-detection OK lines.
	reportDetection(ui, filesys, result, *verbose)

	// Build stack.
	ui.Blank()
	ui.SectionTitle("Building stack")
	for _, s := range result.Services {
		name := s.Name
		var kind string
		if s.Image != "" {
			kind = s.Image
		} else if s.Build != "" {
			kind = "build " + s.Build
		} else {
			kind = "build ."
		}
		port := ""
		if s.Port > 0 {
			port = fmt.Sprintf("port %d", s.Port)
		}
		extra := ""
		if s.Expose {
			extra = "expose ✓"
		} else if s.Healthcheck != nil {
			extra = "healthcheck ✓"
		}
		ui.ServiceLine(name, kind, port, extra)
	}

	yaml := Generate(result)

	if *dryRun {
		printSummary(ui, yaml, result, "", *dryRun)
		ui.DryRunBlock(yaml)
		return nil
	}

	outPath := *output
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(cwd, outPath)
	}
	if _, err := os.Stat(outPath); err == nil && !*force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", outPath)
	}

	// Résumé d'abord, puis confirmation sauf si --yes.
	printSummary(ui, yaml, result, filepath.Base(outPath), false)
	if !*yes && !confirm(ui, "Create "+filepath.Base(outPath)+"?") {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Muted("Aborted. No file written."))
		return nil
	}

	if err := os.WriteFile(outPath, []byte(yaml), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	ui.OK(filepath.Base(outPath) + " written (" + fmt.Sprintf("%d", strings.Count(yaml, "\n")) + " lines)")

	if len(result.Warnings) > 0 {
		ui.Blank()
		for _, w := range result.Warnings {
			ui.Warn(w)
		}
	}
	return nil
}

// confirm lit une réponse Y/n sur stdin. Default Y.
// Si pas de TTY (pipe/CI), considère "yes" par défaut pour que les scripts marchent.
func confirm(ui *UI, prompt string) bool {
	if !ui.Interactive {
		return true
	}
	fmt.Fprint(os.Stdout, "\n  "+ui.Accent("?")+" "+prompt+" "+ui.Muted("[Y/n]")+" ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	trimmed := strings.TrimSpace(strings.ToLower(line))
	return trimmed == "" || trimmed == "y" || trimmed == "yes" || trimmed == "o" || trimmed == "oui"
}

func reportDetection(ui *UI, filesys FS, r *DetectResult, verbose bool) {
	// Found a package.json / Gemfile / etc
	seen := map[string]bool{}
	for _, f := range []string{"package.json", "Gemfile", "pyproject.toml", "requirements.txt", "docker-compose.yml", "docker-compose.yaml", "compose.yml"} {
		if filesys.Exists(f) && !seen[f] {
			ui.OK(fmt.Sprintf("Found %s (%s)", f, r.StackName))
			seen[f] = true
			break
		}
	}
	if filesys.Exists("prisma/schema.prisma") {
		ui.OK("Found prisma/ (Prisma ORM)")
	}
	if filesys.Exists("drizzle.config.ts") || filesys.Exists("drizzle.config.js") {
		ui.OK("Found drizzle config (Drizzle ORM)")
	}
	if r.EnvSource != "" {
		ui.OK(fmt.Sprintf("Found %s (%d env vars)", r.EnvSource, len(r.EnvEntries)))
	}
	if len(r.DetectedDeps) > 0 {
		ui.OK("Detected dependencies: " + strings.Join(r.DetectedDeps, ", "))
	}
	if verbose {
		ui.Blank()
		ui.Info(fmt.Sprintf("Root:         %s", mustCwd()))
		ui.Info(fmt.Sprintf("Stack:        %s", r.StackName))
		ui.Info(fmt.Sprintf("Services:     %d", len(r.Services)))
		if r.EnvSource != "" {
			ui.Info(fmt.Sprintf("Env source:   %s (%d vars)", r.EnvSource, len(r.EnvEntries)))
		}
	}
}

func mustCwd() string {
	c, _ := os.Getwd()
	return c
}

func printSummary(ui *UI, yaml string, r *DetectResult, writtenName string, dryRun bool) {
	lines := strings.Count(yaml, "\n")
	envCount := 0
	for _, s := range r.Services {
		envCount += len(s.Env)
	}
	head := fmt.Sprintf("%d lines · %d services · %d env vars", lines, len(r.Services), envCount)

	title := ".hatch.yml generated"
	if dryRun {
		title = ".hatch.yml (dry-run)"
	} else if writtenName != "" {
		title = writtenName + " generated"
	}

	body := []string{
		head,
		"",
		"Next steps:",
		"  1. Review .hatch.yml (esp. ${SECRET_*})",
		"  2. hatch secrets add STRIPE_SECRET_KEY",
		"  3. git commit && git push",
	}
	ui.SummaryBox(title, body)
}
