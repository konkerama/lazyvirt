package ui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/konkerama/lazyvirt/internal/virt"
)

// recording returns groupModel with act recording into calls.
func recording(t *testing.T, calls *[]call) model {
	m := groupModel(t, 140, 40)
	m.act = func(uuid, action string) error { *calls = append(*calls, call{uuid, action}); return nil }
	return m
}

func TestPauseTogglesOnVMState(t *testing.T) {
	var calls []call
	m, cmd := send(recording(t, &calls), key("tab"), key("P")) // talos-local-cp, running
	msgsOf(cmd)
	m.all[0].State = virt.StatePaused
	_, cmd = send(m, key("P"))
	msgsOf(cmd)
	if want := []call{{"u-cp", "suspend"}, {"u-cp", "resume"}}; !slices.Equal(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
}

func TestGroupPauseConfirmsRunningVMs(t *testing.T) {
	var calls []call
	m, _ := send(recording(t, &calls), key("P")) // groups focused, "all": 3 running
	if !strings.Contains(m.View(), "pause 3 VMs in all? (y/n)") {
		t.Fatalf("missing prompt:\n%s", m.View())
	}
	_, cmd := send(m, key("y"))
	msgsOf(cmd)
	if len(calls) != 3 || calls[0].action != "suspend" {
		t.Fatalf("calls %v, want 3 suspends", calls)
	}
}

func TestAutostartTogglesSelectedVMOnly(t *testing.T) {
	var calls []call
	m, cmd := send(recording(t, &calls), key("a"))
	if cmd != nil || !strings.Contains(m.status, "tab") {
		t.Fatalf("a in groups panel: cmd %v status %q, want a hint", cmd, m.status)
	}
	_, cmd = send(m, key("tab"), key("j"), key("a"))
	msgsOf(cmd)
	if !slices.Equal(calls, []call{{"u-w0", "autostart"}}) {
		t.Fatalf("calls %v, want autostart u-w0", calls)
	}
}

func TestHelpOverlay(t *testing.T) {
	m, _ := send(groupModel(t, 140, 40), key("?"))
	v := ansi.Strip(m.View())
	for _, want := range []string{"help", "autostart", "pause / resume", "search"} {
		if !strings.Contains(v, want) {
			t.Errorf("help missing %q:\n%s", want, v)
		}
	}
	m, cmd := send(m, key("q")) // any key closes help, even q
	if cmd != nil || m.help {
		t.Fatalf("key should only close help: help=%v cmd=%v", m.help, cmd)
	}
}

func TestSearchFiltersVMs(t *testing.T) {
	m, _ := send(groupModel(t, 140, 40), key("/"), key("W"), key("o"), key("r"))
	if !strings.Contains(ansi.Strip(m.View()), "/Wor") {
		t.Errorf("search input not shown in footer")
	}
	m, _ = send(m, key("j")) // still typing: j is text, not a move
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyBackspace}, key("enter"))
	groups, gi, _ := m.groupNav()
	var names []string
	for _, v := range groups[gi].VMs {
		names = append(names, v.Name)
	}
	if !slices.Equal(names, []string{"talos-local-worker-0", "talos-local-worker-1"}) {
		t.Fatalf("filtered VMs %v, want the two workers (case-insensitive)", names)
	}
	m, _ = send(m, key("j")) // search closed: j moves again
	if m.searching {
		t.Fatal("enter should end the search input")
	}
	m, _ = send(m, key("/"), key("esc"))
	if m.search != "" || len(m.visible()) != 4 {
		t.Fatalf("esc should clear the search, got %q with %d VMs", m.search, len(m.visible()))
	}
}
