package main

import (
	"slices"
	"testing"
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
	vms := []VM{
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

func TestPlotLines(t *testing.T) {
	// One cell (2 samples wide, 4 dots tall), max 3 so each dot row is a level.
	g := plotLines([][]float64{{3, 3}}, 3, 1, 1)
	if g[0][0].bits != 0x01|0x08 || g[0][0].owner != 0 {
		t.Errorf("flat max line: got %#x owner %d, want top dots 0x09", g[0][0].bits, g[0][0].owner)
	}
	g = plotLines([][]float64{{0, 0}}, 3, 1, 1)
	if g[0][0].bits != 0x40|0x80 {
		t.Errorf("flat zero line: got %#x, want bottom dots 0xc0", g[0][0].bits)
	}
	// Rising 0 -> 3 connects vertically in the right column.
	g = plotLines([][]float64{{0, 3}}, 3, 1, 1)
	if want := rune(0x40 | 0x08 | 0x10 | 0x20 | 0x80); g[0][0].bits != want {
		t.Errorf("rising line: got %#x, want %#x", g[0][0].bits, want)
	}
	// Later series wins the cell's color where lines share a cell.
	g = plotLines([][]float64{{0, 0}, {3, 3}}, 3, 1, 1)
	if g[0][0].owner != 1 || g[0][0].bits != 0x01|0x08|0x40|0x80 {
		t.Errorf("collision: owner %d bits %#x", g[0][0].owner, g[0][0].bits)
	}
	// Empty cells have no owner.
	g = plotLines([][]float64{{3}}, 3, 2, 1)
	if g[0][0].owner != -1 {
		t.Errorf("padding cell owner %d, want -1", g[0][0].owner)
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
	vms := []VM{
		{UUID: "a", State: StateRunning}, {UUID: "b", State: StateShutoff},
		{UUID: "c", State: StatePaused}, {UUID: "d", State: StateCrashed},
	}
	ids := func(vs []VM) []string {
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
