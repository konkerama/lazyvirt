package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/konkerama/lazyvirt/internal/virt"
)

func TestGroupKey(t *testing.T) {
	cases := map[string]string{
		"talos-local-cp":         "talos-local",
		"talos-local-worker-0":   "talos-local",
		"talos-local-worker-12":  "talos-local",
		"lazyvirt-test-worker-1": "lazyvirt-test",
		"web-1":                  "web",
		"win11":                  "win11",
		"db-primary":             "db",
	}
	for in, want := range cases {
		if got := groupKey(in); got != want {
			t.Errorf("groupKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildGroups(t *testing.T) {
	vms := []virt.VM{
		{UUID: "1", Name: "talos-local-cp"}, {UUID: "2", Name: "talos-local-worker-0"},
		{UUID: "3", Name: "talos-local-worker-1"}, {UUID: "4", Name: "win11"},
		{UUID: "5", Name: "lazyvirt-test-cp"}, {UUID: "6", Name: "lazyvirt-test-worker-0"},
	}
	got := buildGroups(vms)
	var names []string
	for _, g := range got {
		names = append(names, g.Name)
	}
	// "all" first, then prefix groups (2+ members only) sorted by name.
	if want := []string{"all", "lazyvirt-test", "talos-local"}; !slices.Equal(names, want) {
		t.Fatalf("groups %v, want %v", names, want)
	}
	if len(got[0].VMs) != 6 || len(got[2].VMs) != 3 {
		t.Errorf("sizes: all=%d talos=%d", len(got[0].VMs), len(got[2].VMs))
	}
}

func TestShortName(t *testing.T) {
	if got := shortName("talos-local-worker-0", "talos-local"); got != "worker-0" {
		t.Errorf("got %q", got)
	}
	if got := shortName("talos-local-cp", "all"); got != "talos-local-cp" {
		t.Errorf("all group keeps full name, got %q", got)
	}
}

func plotText(g [][]plotCell) string {
	rows := make([]string, len(g))
	for r, cells := range g {
		for _, c := range cells {
			if c.ch == 0 {
				rows[r] += " "
			} else {
				rows[r] += string(c.ch)
			}
		}
	}
	return strings.Join(rows, "\n")
}

func TestPlotLines(t *testing.T) {
	// One sample per column, max 2 on 3 rows so each row is one level.
	g := plotLines([][]float64{{0, 0, 2, 2, 0}}, 2, 5, 3)
	want := "  ╭─╮\n  │ │\n──╯ ╰"
	if got := plotText(g); got != want {
		t.Errorf("line:\n%s\nwant:\n%s", got, want)
	}
	if g[2][0].owner != 0 || g[0][0].owner != -1 {
		t.Errorf("owners: drawn %d, empty %d", g[2][0].owner, g[0][0].owner)
	}
	// Later series wins where lines share a cell.
	g = plotLines([][]float64{{0, 0}, {0, 2}}, 2, 2, 3)
	if g[2][1].owner != 1 || g[2][1].ch != '╯' {
		t.Errorf("collision: owner %d ch %q", g[2][1].owner, g[2][1].ch)
	}
	// Short history is right-aligned, padding cells stay empty.
	g = plotLines([][]float64{{2}}, 2, 2, 1)
	if g[0][0].owner != -1 || g[0][1].ch != '─' {
		t.Errorf("padding: %q", plotText(g))
	}
}

func TestDrawOrderPutsSelectionLast(t *testing.T) {
	if got := drawOrder(4, 1); !slices.Equal(got, []int{0, 2, 3, 1}) {
		t.Errorf("drawOrder(4,1) = %v", got)
	}
	if got := drawOrder(3, -1); !slices.Equal(got, []int{0, 1, 2}) {
		t.Errorf("drawOrder(3,-1) = %v", got)
	}
}

func TestGroupTargets(t *testing.T) {
	vms := []virt.VM{
		{UUID: "a", State: virt.StateRunning}, {UUID: "b", State: virt.StateShutoff},
		{UUID: "c", State: virt.StatePaused}, {UUID: "d", State: virt.StateCrashed},
	}
	ids := func(vs []virt.VM) []string {
		var out []string
		for _, v := range vs {
			out = append(out, v.UUID)
		}
		return out
	}
	cases := map[string][]string{
		"start":    {"b", "d"},
		"shutdown": {"a"},
		"reboot":   {"a"},
		"destroy":  {"a", "c", "d"},
	}
	for action, want := range cases {
		if got := ids(groupTargets(vms, action)); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", action, got, want)
		}
	}
}
