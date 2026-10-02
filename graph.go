package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// lerpColor blends two #rrggbb colors; t=0 gives a, t=1 gives b.
func lerpColor(a, b string, t float64) string {
	var ar, ag, ab, br, bg, bb int
	fmt.Sscanf(a, "#%02x%02x%02x", &ar, &ag, &ab)
	fmt.Sscanf(b, "#%02x%02x%02x", &br, &bg, &bb)
	mix := func(x, y int) int { return int(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb))
}

// panel draws a rounded box exactly w x h with the title and an info string
// set into the top border, btop style. Content lines are clipped or padded.
func panel(title, info string, lines []string, w, h int, color string) string {
	bc := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	t := " " + titleStyle.Render(title) + " "
	i := ""
	if info != "" {
		i = " " + info + " "
	}
	fill := w - 4 - lipgloss.Width(t) - lipgloss.Width(i)
	if fill < 0 {
		i, fill = "", max(w-4-lipgloss.Width(t), 0)
	}
	if fill == 0 && lipgloss.Width(t) > w-4 {
		t = ansi.Truncate(t, max(w-4, 0), "")
	}
	var b strings.Builder
	b.WriteString(bc.Render("╭─") + t + bc.Render(strings.Repeat("─", fill)) + i + bc.Render("─╮"))
	side := bc.Render("│")
	for r := range max(h-2, 0) {
		l := ""
		if r < len(lines) {
			l = ansi.Truncate(lines[r], w-2, "…")
		}
		b.WriteString("\n" + side + l + strings.Repeat(" ", w-2-lipgloss.Width(l)) + side)
	}
	b.WriteString("\n" + bc.Render("╰"+strings.Repeat("─", max(w-2, 0))+"╯"))
	return b.String()
}
