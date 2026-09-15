package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thedavidweng/tg-drive-cli/internal/service"
)

// Scan, listing, and tree browsing commands plus their display helpers.

func NewScanCmd(rt Runtime) *cobra.Command {
	var full, strict, repair, includeDeleted bool
	c := &cobra.Command{
		Use:   "scan [remote-root]",
		Short: "Scan Telegram channel and rebuild index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			root := "/"
			if len(args) > 0 {
				root = args[0]
			}
			data, err := app.Scan(context.Background(), service.ScanOptions{
				Full: full, Strict: strict, Repair: repair, IncludeDeleted: includeDeleted, Root: root,
			})
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(data)
			}
			if warn, ok := data["full_scan_warning"].(string); ok && warn != "" {
				fmt.Fprintln(os.Stderr, "warning: "+warn)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "scan complete (%v): %v active, %v deleted, %v invalid, %v missing\n",
				data["mode"], data["active"], data["deleted"], data["invalid"], data["missing"])
			return nil
		},
	}
	c.Flags().BoolVar(&full, "full", false, "full scan")
	c.Flags().BoolVar(&strict, "strict", false, "exit on invalid messages")
	c.Flags().BoolVar(&repair, "repair", false, "repair DB-only inconsistencies")
	c.Flags().BoolVar(&includeDeleted, "include-deleted", false, "record tombstoned files")
	return c
}

func NewLsCmd(rt Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "ls [remote-path]",
		Short: "List remote directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			p := "/"
			if len(args) > 0 {
				p = args[0]
			}
			entries, err := app.ListDir(context.Background(), p)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]any{"path": p, "entries": entries})
			}
			width := 0
			for _, e := range entries {
				n := displayWidth(e.Name)
				if e.Type == "dir" {
					n++
				}
				if n > width {
					width = n
				}
			}
			for _, e := range entries {
				if e.Type == "dir" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-4s %s/\n", "DIR", e.Name)
				} else {
					pad := width - displayWidth(e.Name)
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-4s %s%s  %8s\n", "FILE", e.Name, strings.Repeat(" ", pad), humanSize(e.Size))
				}
			}
			return nil
		},
	}
}

// displayWidth approximates the terminal cell width of s: East Asian wide
// glyphs occupy two cells, everything else one.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, symbols, punctuation
		r >= 0x3041 && r <= 0x33FF, // Hiragana..CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x2FFFD, // CJK ext B+
		r >= 0x30000 && r <= 0x3FFFD:
		return true
	}
	return false
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func NewTreeCmd(rt Runtime) *cobra.Command {
	var depth int
	c := &cobra.Command{
		Use:   "tree [remote-path]",
		Short: "Show remote tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			r := rt.Renderer()
			app, cleanup, err := rt.OpenApp(cmd)
			if err != nil {
				return r.Error(err)
			}
			defer cleanup()
			p := "/"
			if len(args) > 0 {
				p = args[0]
			}
			nodes, err := app.Tree(context.Background(), p, depth)
			if err != nil {
				return r.Error(err)
			}
			if rt.JSON() {
				return r.Success(map[string]any{"path": p, "tree": nodes})
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, p)
			renderTree(out, nodes, "")
			return nil
		},
	}
	c.Flags().IntVar(&depth, "depth", 0, "max depth")
	return c
}

func renderTree(w io.Writer, nodes []service.TreeNode, prefix string) {
	for i, n := range nodes {
		connector, childPrefix := "├── ", prefix+"│   "
		if i == len(nodes)-1 {
			connector, childPrefix = "└── ", prefix+"    "
		}
		_, _ = fmt.Fprintf(w, "%s%s%s\n", prefix, connector, n.Name)
		renderTree(w, n.Children, childPrefix)
	}
}
