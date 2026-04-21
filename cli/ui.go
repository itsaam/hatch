package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// UI encapsulates styled output. When interactive is false, all styling is
// stripped and sleeps are zero — we degrade gracefully for redirected output
// and CI logs.
type UI struct {
	Out         io.Writer
	Interactive bool
	Animate     bool
	stepDelay   time.Duration

	// styles (cached)
	accent  lipgloss.Style
	success lipgloss.Style
	muted   lipgloss.Style
	warn    lipgloss.Style
	errS    lipgloss.Style
	header  lipgloss.Style
	box     lipgloss.Style
	dryRun  lipgloss.Style
}

// NewUI builds a UI based on the current process state.
// - If stdout is not a TTY: plain output, no colors, no sleep.
// - If noAnimation is true: colors ok, but sleeps are zero.
func NewUI(out io.Writer, noAnimation bool) *UI {
	interactive := false
	if f, ok := out.(*os.File); ok {
		interactive = term.IsTerminal(int(f.Fd()))
	}

	u := &UI{
		Out:         out,
		Interactive: interactive,
		Animate:     interactive && !noAnimation,
	}
	if u.Animate {
		u.stepDelay = 80 * time.Millisecond
	}

	// Palette Hatch
	accentColor := lipgloss.Color("#FF7A3D")
	successColor := lipgloss.Color("#7FD99C")
	mutedColor := lipgloss.Color("#6E6355")
	warnColor := lipgloss.Color("#E9D8A6")
	errColor := lipgloss.Color("#E57373")

	if !u.Interactive {
		// no-color renderer
		r := lipgloss.NewRenderer(out)
		r.SetColorProfile(0) // Ascii
		u.accent = r.NewStyle()
		u.success = r.NewStyle()
		u.muted = r.NewStyle()
		u.warn = r.NewStyle()
		u.errS = r.NewStyle()
		u.header = r.NewStyle()
		u.box = r.NewStyle()
		u.dryRun = r.NewStyle()
		return u
	}

	u.accent = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	u.success = lipgloss.NewStyle().Foreground(successColor)
	u.muted = lipgloss.NewStyle().Foreground(mutedColor)
	u.warn = lipgloss.NewStyle().Foreground(warnColor)
	u.errS = lipgloss.NewStyle().Foreground(errColor).Bold(true)
	u.header = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(0, 2).
		Foreground(accentColor).
		Bold(true)
	u.box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(1, 2)
	u.dryRun = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(mutedColor).
		Padding(0, 1)
	return u
}

// Header prints the rounded title block.
func (u *UI) Header() {
	if !u.Interactive {
		fmt.Fprintln(u.Out, "hatch init")
		fmt.Fprintln(u.Out)
		return
	}
	title := "hatch init"
	fmt.Fprintln(u.Out, u.header.Render(title))
	fmt.Fprintln(u.Out)
}

// Step prints a muted arrow line (e.g. "Scanning project…").
func (u *UI) Step(text string) {
	if u.Interactive {
		fmt.Fprintln(u.Out, "  "+u.accent.Render("▸")+" "+text)
	} else {
		fmt.Fprintln(u.Out, "  > "+text)
	}
	u.sleep()
}

// OK prints a green check line.
func (u *UI) OK(text string) {
	if u.Interactive {
		fmt.Fprintln(u.Out, "  "+u.success.Render("✓")+" "+text)
	} else {
		fmt.Fprintln(u.Out, "  [ok] "+text)
	}
	u.sleep()
}

// Info prints a muted info line.
func (u *UI) Info(text string) {
	if u.Interactive {
		fmt.Fprintln(u.Out, "  "+u.muted.Render(text))
	} else {
		fmt.Fprintln(u.Out, "  "+text)
	}
	u.sleep()
}

// Warn prints a warning line.
func (u *UI) Warn(text string) {
	if u.Interactive {
		fmt.Fprintln(u.Out, "  "+u.warn.Render("!")+" "+text)
	} else {
		fmt.Fprintln(u.Out, "  ! "+text)
	}
}

// Err prints an error line.
func (u *UI) Err(text string) {
	if u.Interactive {
		fmt.Fprintln(u.Out, "  "+u.errS.Render("✗")+" "+text)
	} else {
		fmt.Fprintln(u.Out, "  x "+text)
	}
}

// Blank prints an empty line.
func (u *UI) Blank() { fmt.Fprintln(u.Out) }

// SectionTitle prints a bold section header.
func (u *UI) SectionTitle(text string) {
	if u.Interactive {
		fmt.Fprintln(u.Out, "  "+u.accent.Render(text))
	} else {
		fmt.Fprintln(u.Out, "  "+text+":")
	}
}

// ServiceLine prints a formatted service description row.
func (u *UI) ServiceLine(name, kind, port, extra string) {
	nameCol := padRight(name, 8)
	kindCol := padRight(kind, 22)
	portCol := padRight(port, 12)
	if u.Interactive {
		line := "    " + u.accent.Render("▸ ") +
			u.accent.Render(nameCol) +
			u.muted.Render(kindCol) +
			u.muted.Render(portCol) +
			u.success.Render(extra)
		fmt.Fprintln(u.Out, line)
	} else {
		fmt.Fprintln(u.Out, "    - "+nameCol+kindCol+portCol+extra)
	}
	u.sleep()
}

// SummaryBox prints the final rounded summary.
func (u *UI) SummaryBox(title string, lines []string) {
	if !u.Interactive {
		fmt.Fprintln(u.Out)
		fmt.Fprintln(u.Out, "-- "+title+" --")
		for _, l := range lines {
			fmt.Fprintln(u.Out, "  "+l)
		}
		return
	}

	var sb strings.Builder
	sb.WriteString(u.accent.Render(title))
	sb.WriteString("\n\n")
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	fmt.Fprintln(u.Out)
	fmt.Fprintln(u.Out, u.box.Render(strings.TrimRight(sb.String(), "\n")))
}

// DryRunBlock prints the yaml content inside a bordered block.
func (u *UI) DryRunBlock(yaml string) {
	if !u.Interactive {
		fmt.Fprintln(u.Out)
		fmt.Fprintln(u.Out, "--- .hatch.yml (dry-run) ---")
		fmt.Fprint(u.Out, yaml)
		return
	}
	fmt.Fprintln(u.Out)
	fmt.Fprintln(u.Out, u.muted.Render("Dry run — no file written"))
	fmt.Fprintln(u.Out, u.dryRun.Render(strings.TrimRight(yaml, "\n")))
}

// Accent returns an accent-styled string for inline composition.
func (u *UI) Accent(s string) string { return u.accent.Render(s) }

// Muted returns a muted-styled string for inline composition.
func (u *UI) Muted(s string) string { return u.muted.Render(s) }

func (u *UI) sleep() {
	if u.stepDelay > 0 {
		time.Sleep(u.stepDelay)
	}
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}
