package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLerpColor(t *testing.T) {
	cases := map[float64]string{0: "#000000", 1: "#ff8040", 0.5: "#804020"}
	for f, want := range cases {
		if got := lerpColor("#000000", "#ff8040", f); got != want {
			t.Errorf("lerp(%v) = %s, want %s", f, got, want)
		}
	}
}

func TestPanelExactSize(t *testing.T) {
	out := panel("cpu", "12.0%", []string{"a line that is far too long to fit", "x"}, 20, 5, "#888888")
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Fatalf("%d lines, want 5:\n%s", len(lines), out)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != 20 {
			t.Errorf("line %d width %d, want 20: %q", i, w, l)
		}
	}
	if !strings.Contains(lines[0], "cpu") || !strings.Contains(lines[0], "12.0%") {
		t.Errorf("title missing from top border: %q", lines[0])
	}
}
