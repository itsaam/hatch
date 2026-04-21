package main

import (
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
  --verbose         print detection details`)
}

func runInit(argv []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print YAML to stdout")
	force := fs.Bool("force", false, "overwrite existing .hatch.yml")
	output := fs.String("output", ".hatch.yml", "output path")
	verbose := fs.Bool("verbose", false, "verbose logs")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get cwd: %w", err)
	}

	fmt.Println("\U0001F95A Hatch init")
	fmt.Println()

	filesys := osFS{root: cwd}
	result, err := Detect(filesys)
	if err != nil {
		return err
	}

	if *verbose {
		fmt.Printf("Root:          %s\n", cwd)
		fmt.Printf("Stack:         %s\n", result.StackName)
		fmt.Printf("Service count: %d\n\n", len(result.Services))
	}

	fmt.Printf("Detected stack: %s\n", result.StackName)
	fmt.Println("Services:")
	for _, s := range result.Services {
		desc := describeService(s)
		fmt.Printf("  - %s\n", desc)
	}
	fmt.Println()

	yaml := Generate(result)

	if *dryRun {
		fmt.Println("--- .hatch.yml (dry-run) ---")
		fmt.Print(yaml)
		return nil
	}

	outPath := *output
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(cwd, outPath)
	}

	if _, err := os.Stat(outPath); err == nil && !*force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", outPath)
	}

	if err := os.WriteFile(outPath, []byte(yaml), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	lines := strings.Count(yaml, "\n")
	fmt.Printf("%s written (%d lines)\n\n", filepath.Base(outPath), lines)

	if len(result.Warnings) > 0 {
		fmt.Println("Warnings:")
		for _, w := range result.Warnings {
			fmt.Printf("  ! %s\n", w)
		}
		fmt.Println()
	}

	fmt.Println("Next steps:")
	fmt.Println("  1. Review the generated file")
	fmt.Println("  2. Adjust env vars if needed (placeholders: ${SECRET_<NAME>})")
	fmt.Println("  3. Commit and push to trigger your first preview")

	return nil
}

func describeService(s Service) string {
	kind := "image " + s.Image
	if s.Build != "" {
		kind = "build " + s.Build
	}
	parts := []string{s.Name, kind}
	if s.Port > 0 {
		parts = append(parts, fmt.Sprintf("port %d", s.Port))
	}
	if s.Expose {
		parts = append(parts, "exposed")
	}
	return strings.Join(parts, ", ")
}
