package ui

import (
	"cmp"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/konkerama/lazyvirt/internal/virt"
)

type call struct{ uuid, action string }

func newTestModel(calls *[]call) model {
	m := newModel(
		func() ([]virt.VM, error) { return nil, nil },
		func(uuid, action string) error { *calls = append(*calls, call{uuid, action}); return nil },
		func(uuid string) (string, error) { return "info for " + uuid, nil },
		"", time.Millisecond,
	)
	m.width, m.height = 120, 30
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// send feeds msgs through Update and returns the last command.
func send(m model, msgs ...tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	for _, msg := range msgs {
		var tm tea.Model
		tm, cmd = m.Update(msg)
		m = tm.(model)
	}
	return m, cmd
}

// runAll executes cmd (and nested batches), feeding non-tick results to m.
func runAll(m model, cmd tea.Cmd) model {
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = runAll(m, c)
		}
	case tickMsg, nil:
	default:
		m, _ = send(m, msg)
	}
	return m
}

var threeVMs = []virt.VM{
	{UUID: "u-cp", Name: "talos-local-cp", State: virt.StateRunning, VCPUs: 2},
	{UUID: "u-w0", Name: "talos-local-worker-0", State: virt.StateRunning, VCPUs: 4},
	{UUID: "u-w1", Name: "talos-local-worker-1", State: virt.StateShutoff, VCPUs: 4},
}

// groupModel has 4 snapshots of history: three talos VMs plus a stopped win11.
func groupModel(t testing.TB, w, h int) model {
	t.Helper()
	var calls []call
	t0 := time.Now()
	m := newTestModel(&calls)
	m.width, m.height = w, h
	for i := range uint64(4) {
		vms := []virt.VM{
			{UUID: "u-cp", Name: "talos-local-cp", State: virt.StateRunning, VCPUs: 1, CPUTime: i * 1e8, MaxMemKiB: 1 << 20, RSSKiB: 1 << 18},
			{UUID: "u-w0", Name: "talos-local-worker-0", State: virt.StateRunning, VCPUs: 1, CPUTime: i * 6e8, RdBytes: i << 20},
			{UUID: "u-w1", Name: "talos-local-worker-1", State: virt.StateRunning, VCPUs: 1, CPUTime: i * 3e8, RxBytes: i << 10},
			{UUID: "u-x", Name: "win11", State: virt.StateShutoff},
		}
		m, _ = send(m, snapMsg{vms: vms, at: t0.Add(time.Duration(i) * time.Second)})
	}
	return m
}

// sidebarRows returns the stripped left-column text of each screen line.
func sidebarRows(m model) []string {
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	pw := slices.Index([]rune(lines[0]), '╮') + 1
	var out []string
	for _, l := range lines {
		r := []rune(l)
		out = append(out, string(r[:min(pw, len(r))]))
	}
	return out
}

// lineOf is the screen line index of the first line containing s, or -1.
func lineOf(m model, s string) int {
	return slices.IndexFunc(strings.Split(ansi.Strip(m.View()), "\n"), func(l string) bool { return strings.Contains(l, s) })
}

func TestSelectionFollowsUUIDAcrossRefresh(t *testing.T) {
	var calls []call
	m, _ := send(newTestModel(&calls), snapMsg{vms: threeVMs, at: time.Now()}, key("tab"), key("j"), key("j"))
	if got := m.target().UUID; got != "u-w1" {
		t.Fatalf("selected %q after jj, want u-w1", got)
	}
	reordered := []virt.VM{threeVMs[2], threeVMs[0], threeVMs[1]}
	m, _ = send(m, snapMsg{vms: reordered, at: time.Now()})
	if got := m.target().UUID; got != "u-w1" {
		t.Fatalf("selected %q after reorder, want u-w1", got)
	}
}

func TestRatesFromConsecutiveSnapshots(t *testing.T) {
	var calls []call
	t0 := time.Now()
	a := []virt.VM{{UUID: "u", Name: "vm", State: virt.StateRunning, VCPUs: 1, CPUTime: 0}}
	b := []virt.VM{{UUID: "u", Name: "vm", State: virt.StateRunning, VCPUs: 1, CPUTime: 5e8}}
	m, _ := send(newTestModel(&calls), snapMsg{vms: a, at: t0}, snapMsg{vms: b, at: t0.Add(time.Second)})
	if got := m.rates["u"].CPU; got != 50 {
		t.Fatalf("cpu = %v, want 50", got)
	}
}

func TestForceOffNeedsConfirm(t *testing.T) {
	var calls []call
	m, _ := send(newTestModel(&calls), snapMsg{vms: threeVMs, at: time.Now()}, key("tab"), key("F"))
	if !strings.Contains(m.View(), "force off talos-local-cp?") {
		t.Fatalf("no confirm prompt:\n%s", m.View())
	}
	m, cmd := send(m, key("n"))
	if cmd != nil {
		cmd()
	}
	if len(calls) != 0 {
		t.Fatalf("n should cancel, got calls %v", calls)
	}
	_, cmd = send(m, key("F"), key("y"))
	if cmd == nil {
		t.Fatal("y returned no command")
	}
	cmd()
	if len(calls) != 1 || calls[0] != (call{"u-cp", "destroy"}) {
		t.Fatalf("calls = %v, want destroy u-cp", calls)
	}
}

func TestShellNeedsVMsPanelFocus(t *testing.T) {
	m, cmd := send(groupModel(t, 140, 40), key("s")) // groups panel focused
	if cmd != nil {
		t.Errorf("shell started with the groups panel focused")
	}
	if !strings.Contains(m.status, "tab") {
		t.Errorf("status should hint how to pick a VM, got %q", m.status)
	}
}

func TestGroupActionsConfirmAndTargetApplicableVMs(t *testing.T) {
	var calls []call
	m := groupModel(t, 140, 40) // "all": 3 running + win11 shutoff
	m.act = func(uuid, action string) error { calls = append(calls, call{uuid, action}); return nil }

	m, cmd := send(m, key("u"))
	if cmd != nil || !strings.Contains(m.View(), "start 1 VM in all? (y/n)") {
		t.Fatalf("u should ask first:\n%s", m.View())
	}
	m, cmd = send(m, key("y"))
	msgsOf(cmd)
	if !slices.Equal(calls, []call{{"u-x", "start"}}) {
		t.Fatalf("calls %v, want start of the stopped VM only", calls)
	}

	calls = nil
	m, _ = send(m, key("S"))
	if !strings.Contains(m.View(), "shut down 3 VMs in all? (y/n)") {
		t.Fatalf("missing shutdown prompt:\n%s", m.View())
	}
	m, cmd = send(m, key("n"))
	msgsOf(cmd)
	if len(calls) != 0 {
		t.Fatalf("n should cancel, got %v", calls)
	}

	m, _ = send(m, key("j"), key("u")) // talos-local: all running
	if !strings.Contains(m.status, "nothing to start") {
		t.Errorf("status %q, want nothing-to-do message", m.status)
	}
}

func TestPanelsFocusAndActions(t *testing.T) {
	var calls []call
	m := groupModel(t, 140, 40)
	m.act = func(uuid, action string) error { calls = append(calls, call{uuid, action}); return nil }
	if got := m.target().UUID; got != "u-cp" {
		t.Fatalf("default VM %q, want first VM u-cp", got)
	}
	m, _ = send(m, key("tab"), key("j")) // focus vms, move to worker-0
	if m.gfocus != 1 || m.target().UUID != "u-w0" {
		t.Fatalf("focus %d target %q, want vms/u-w0", m.gfocus, m.target().UUID)
	}
	_, cmd := send(m, key("u"))
	cmd()
	if len(calls) != 1 || calls[0] != (call{"u-w0", "start"}) {
		t.Fatalf("calls %v, want start u-w0", calls)
	}
	// Back to groups, pick talos-local: worker-0 is a member, so it stays selected.
	m, _ = send(m, key("esc"), key("j"))
	if m.gsel != "talos-local" || m.target().UUID != "u-w0" {
		t.Fatalf("gsel %q target %q, want talos-local/u-w0", m.gsel, m.target().UUID)
	}
	rows := strings.Join(sidebarRows(m), "\n")
	if !strings.Contains(rows, "worker-0") || strings.Contains(rows, "win11") {
		t.Errorf("vms panel should list talos-local members only:\n%s", rows)
	}
}

func TestGroupFocusOverlaysAllVMs(t *testing.T) {
	m := groupModel(t, 160, 40)
	v := ansi.Strip(m.View())
	for _, want := range []string{"cpu", "mem", "disk read", "disk write", "net rx", "net tx", "talos-local-worker-0"} {
		if !strings.Contains(v, want) {
			t.Errorf("group charts missing %q", want)
		}
	}
	// cp 10%, worker-0 60%, worker-1 30%: one value per running VM.
	if top := strings.Split(v, "\n")[lineOf(m, "─ cpu")]; !strings.Contains(top, "10% 60% 30%") {
		t.Errorf("cpu title should list every running VM: %q", top)
	}
}

func TestVMFocusShowsOnlyThatVM(t *testing.T) {
	m, _ := send(groupModel(t, 160, 40), key("tab"), key("j")) // worker-0
	v := ansi.Strip(m.View())
	header := strings.Split(v, "\n")[0]
	if !strings.Contains(header, "talos-local-worker-0") || !strings.Contains(header, "running") {
		t.Errorf("header should name the VM and its state: %q", header)
	}
	top := strings.Split(v, "\n")[lineOf(m, "─ cpu")]
	if !strings.Contains(top, "60.0%") || strings.Contains(top, "10%") {
		t.Errorf("cpu title should show worker-0 only: %q", top)
	}
	for _, want := range []string{"mem", "disk read", "disk write", "net rx", "net tx", "1.0M/s"} {
		if !strings.Contains(v, want) {
			t.Errorf("VM charts missing %q", want)
		}
	}
}

func TestChartLayoutSameInBothFocuses(t *testing.T) {
	m := groupModel(t, 140, 40)
	n, _ := send(m, key("tab"))
	for _, s := range []string{"─ cpu", "─ disk read", "─ net rx", "─ vms", "─ info"} {
		if a, b := lineOf(m, s), lineOf(n, s); a != b || a < 0 {
			t.Errorf("%q at line %d with groups focus, %d with vms focus", s, a, b)
		}
	}
}

func TestInfoPanelFollowsSelection(t *testing.T) {
	m := groupModel(t, 140, 40)
	m = runAll(m, m.infoCmd(false))
	if !strings.Contains(strings.Join(sidebarRows(m), "\n"), "info for u-cp") {
		t.Fatalf("info panel missing selected VM details:\n%s", m.View())
	}
	m, cmd := send(m, key("tab"), key("j"))
	m = runAll(m, cmd)
	if !strings.Contains(strings.Join(sidebarRows(m), "\n"), "info for u-w0") {
		t.Fatalf("info panel did not follow selection:\n%s", m.View())
	}
}

func TestInfoRefreshesEachTick(t *testing.T) {
	var calls []call
	n := 0
	m := newTestModel(&calls)
	m.details = func(uuid string) (string, error) { n++; return fmt.Sprintf("ip v%d", n), nil }
	m, cmd := send(m, snapMsg{vms: threeVMs, at: time.Now()})
	m = runAll(m, cmd)
	m, cmd = send(m, snapMsg{vms: threeVMs, at: time.Now()})
	m = runAll(m, cmd)
	if !strings.Contains(m.View(), "ip v2") {
		t.Fatalf("info not refreshed on tick (details calls=%d):\n%s", n, m.View())
	}
}

func TestViewFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 20}, {140, 40}, {220, 60}} {
		for _, focus := range []string{"", "tab"} {
			m := groupModel(t, size[0], size[1])
			if focus != "" {
				m, _ = send(m, key(focus))
			}
			lines := strings.Split(m.View(), "\n")
			if len(lines) != size[1] {
				t.Errorf("%v %q: %d lines", size, focus, len(lines))
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > size[0] {
					t.Errorf("%v %q: line %d is %d wide", size, focus, i, w)
				}
			}
		}
	}
}

func TestMembersShowState(t *testing.T) {
	m := groupModel(t, 140, 40) // "all" selected
	left := strings.Join(sidebarRows(m), "\n")
	for _, want := range []string{"talos-local-cp", "running", "win11", "shutoff"} {
		if !strings.Contains(left, want) {
			t.Errorf("sidebar missing %q:\n%s", want, left)
		}
	}
	if strings.Contains(left, "…") {
		t.Errorf("sidebar clipped a row:\n%s", left)
	}
	wide := groupModel(t, 200, 40)
	if !strings.Contains(wide.View(), "win11 (shutoff)") {
		t.Errorf("legend should flag non-running VMs:\n%s", strings.Split(wide.View(), "\n")[0])
	}
}

func TestSidebarStable(t *testing.T) {
	m := groupModel(t, 140, 40)
	width := func(m model) int {
		return slices.Index([]rune(ansi.Strip(strings.Split(m.View(), "\n")[0])), '╮') + 1
	}
	w0, vms0 := width(m), lineOf(m, "─ vms")
	for range 2 {
		m, _ = send(m, key("j"))
		if w, v := width(m), lineOf(m, "─ vms"); w != w0 || v != vms0 {
			t.Fatalf("sidebar moved on group %q: width %d->%d, vms panel line %d->%d", m.gsel, w0, w, vms0, v)
		}
	}
}

func TestVMsPanelScrollsToSelection(t *testing.T) {
	var calls []call
	m := newTestModel(&calls)
	m.width, m.height = 120, 20
	var vms []virt.VM
	for i := range 20 {
		vms = append(vms, virt.VM{UUID: fmt.Sprint(i), Name: fmt.Sprintf("node-%02d", i), State: virt.StateRunning})
	}
	m, _ = send(m, snapMsg{vms: vms, at: time.Now()}, key("tab"), key("G"))
	if !strings.Contains(strings.Join(sidebarRows(m), "\n"), "node-19") {
		t.Errorf("last VM not scrolled into view:\n%s", m.View())
	}
}

func TestScrollStart(t *testing.T) {
	cases := []struct{ n, cur, rows, want int }{
		{5, 0, 10, 0}, {5, 4, 10, 0}, {20, 0, 5, 0}, {20, 4, 5, 0}, {20, 5, 5, 1}, {20, 19, 5, 15},
	}
	for _, c := range cases {
		if got := scrollStart(c.n, c.cur, c.rows); got != c.want {
			t.Errorf("scrollStart(%d,%d,%d) = %d, want %d", c.n, c.cur, c.rows, got, c.want)
		}
	}
}

func TestTruncMiddle(t *testing.T) {
	if got := trunc("talos-local-worker-0", 12); got != "tal…worker-0" {
		t.Errorf("trunc = %q", got)
	}
	if got := trunc("short", 12); got != "short" {
		t.Errorf("trunc = %q", got)
	}
}

func TestShellKeyResolvesIPThenSSH(t *testing.T) {
	var calls []call
	m := newTestModel(&calls)
	m.uri = "qemu:///system"
	m.ip = func(uuid string) (string, error) { return map[string]string{"u-w0": "10.0.0.5"}[uuid], nil }
	m, cmd := send(m, snapMsg{vms: threeVMs, at: time.Now()}, key("tab"), key("j"), key("s"))
	if cmd == nil {
		t.Fatal("s returned no command")
	}
	msg, ok := cmd().(shellMsg)
	if !ok || !slices.Equal(msg.argv, []string{"ssh", "-o", "ConnectTimeout=3", "10.0.0.5"}) || !msg.ssh {
		t.Fatalf("got %#v, want ssh to 10.0.0.5", msg)
	}
	if _, cmd = send(m, msg); cmd == nil {
		t.Fatal("shellMsg should exec the shell")
	}
}

func TestShellFallsBackToConsoleWhenSSHCannotConnect(t *testing.T) {
	var calls []call
	m, _ := send(newTestModel(&calls), snapMsg{vms: threeVMs, at: time.Now()})
	m.uri = "qemu:///system"
	exit255 := exec.Command("sh", "-c", "exit 255").Run()
	m, cmd := send(m, shellDoneMsg{name: "talos-local-cp", ssh: true, err: exit255})
	if cmd == nil || !strings.Contains(m.status, "console") {
		t.Fatalf("want console fallback, status %q", m.status)
	}
	// A normal ssh exit (user typed exit 1) must not open a console.
	exit1 := exec.Command("sh", "-c", "exit 1").Run()
	if _, cmd = send(m, shellDoneMsg{name: "talos-local-cp", ssh: true, err: exit1}); cmd != nil {
		t.Fatal("exit 1 should not fall back to console")
	}
}

func BenchmarkView(b *testing.B) {
	var calls []call
	m := newTestModel(&calls)
	m.width, m.height = 200, 60
	t0 := time.Now()
	for i := range uint64(histLen) {
		var vms []virt.VM
		for j := range uint64(8) {
			vms = append(vms, virt.VM{UUID: fmt.Sprint(j), Name: fmt.Sprintf("talos-local-worker-%d", j), State: virt.StateRunning,
				VCPUs: 1, MaxMemKiB: 1 << 20, RSSKiB: (i * j) << 8, CPUTime: i * i * j * 1e5, RdBytes: i * i * j << 8, RxBytes: i * j})
		}
		m, _ = send(m, snapMsg{vms: vms, at: t0.Add(time.Duration(i) * time.Second)})
	}
	for b.Loop() {
		_ = m.View()
	}
}

// msgsOf runs cmd and any nested batch, returning every message produced.
func msgsOf(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, msgsOf(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func countTicks(msgs []tea.Msg) int {
	n := 0
	for _, m := range msgs {
		if _, ok := m.(tickMsg); ok {
			n++
		}
	}
	return n
}

func TestOnlyTickSnapshotsScheduleTheNextTick(t *testing.T) {
	var calls []call
	m := newTestModel(&calls)
	// An action's follow-up refresh must not start a second refresh loop.
	_, cmd := send(m, actMsg{})
	var snap tea.Msg
	for _, msg := range msgsOf(cmd) {
		if s, ok := msg.(snapMsg); ok {
			snap = s
		}
	}
	if snap == nil {
		t.Fatal("actMsg should refresh")
	}
	if _, cmd = send(m, snap); countTicks(msgsOf(cmd)) != 0 {
		t.Error("refresh after an action scheduled another tick loop")
	}
	// The tick loop itself keeps going.
	_, cmd = send(m, tickMsg(time.Now()))
	tickSnap := msgsOf(cmd)[0]
	if _, cmd = send(m, tickSnap); countTicks(msgsOf(cmd)) != 1 {
		t.Error("tick refresh should schedule exactly one next tick")
	}
}

// hostModel is groupModel with a host and storage source attached.
func hostModel(t *testing.T) model {
	t.Helper()
	busy := uint64(0)
	m := newTestModel(new([]call))
	m.width, m.height = 160, 44
	m.host = func() (virt.Host, error) {
		busy += 50
		return virt.Host{Name: "pc", Model: "x86_64", CPUs: 12, MemKiB: 32 << 20, AvailKiB: 16 << 20, CPUBusy: busy, CPUTotal: busy * 4}, nil
	}
	m.storage = func() ([]virt.Pool, []virt.Net, error) {
		return []virt.Pool{{Name: "talos-local", Active: true, Capacity: 100 << 30, Allocation: 45 << 30}},
			[]virt.Net{{Name: "talos-local-net", Bridge: "virbr1", Active: true,
				Leases: []virt.Lease{{IP: "192.168.150.11", MAC: "52:54:00:6c:61:0b", Hostname: "worker-0"}}}}, nil
	}
	for range 3 {
		m = runAll(m, m.fetchCmd(false))
	}
	m, _ = send(m, snapMsg{vms: []virt.VM{
		{UUID: "u-cp", Name: "talos-local-cp", State: virt.StateRunning, VCPUs: 2, MaxMemKiB: 4 << 20},
		{UUID: "u-w0", Name: "talos-local-worker-0", State: virt.StateRunning, VCPUs: 4, MaxMemKiB: 6 << 20},
	}, at: time.Now()})
	return m
}

func TestHostPanelShowsCapacity(t *testing.T) {
	m := hostModel(t)
	side := strings.Join(sidebarRows(m), "\n")
	for _, want := range []string{"─ host", "pc", "25%", "16.0G / 32.0G", "6 / 12", "10.0G / 32.0G"} {
		if !strings.Contains(side, want) {
			t.Errorf("host panel missing %q:\n%s", want, side)
		}
	}
}

func TestHostFocusShowsPoolsAndNetworks(t *testing.T) {
	m := hostModel(t)
	n, cmd := send(m, key("tab"), key("tab"))
	if n.gfocus != 2 {
		t.Fatalf("focus %d, want host (2)", n.gfocus)
	}
	n = runAll(n, cmd)
	v := ansi.Strip(n.View())
	for _, want := range []string{"pools", "talos-local", "45%", "networks", "virbr1", "192.168.150.11", "worker-0"} {
		if !strings.Contains(v, want) {
			t.Errorf("host view missing %q", want)
		}
	}
	// Same grid as the other focuses: cpu row, then pools where disk read was.
	if a, b := lineOf(m, "─ cpu"), lineOf(n, "─ cpu"); a != b {
		t.Errorf("cpu chart moved: line %d vs %d", a, b)
	}
	if a, b := lineOf(m, "─ disk read"), lineOf(n, "─ pools"); a != b {
		t.Errorf("pools panel at line %d, disk read was at %d", b, a)
	}
	if _, c := send(n, key("u")); c != nil {
		t.Error("VM keys must do nothing with the host panel focused")
	}
}

type snapCall struct{ uuid, op, name string }

// snapModel is groupModel with the vms panel focused and a fake snapshot store.
func snapModel(t *testing.T) (model, *[]snapCall) {
	t.Helper()
	calls := &[]snapCall{}
	m := groupModel(t, 140, 40)
	m.snaps = func(uuid string) ([]virt.Snapshot, error) {
		return []virt.Snapshot{
			{Name: "s1", State: "running", Created: time.Unix(1790000000, 0)},
			{Name: "s2", State: "shutoff", Created: time.Unix(1790100000, 0), Current: true},
		}, nil
	}
	m.snapDo = func(uuid, op, name string) (string, error) {
		*calls = append(*calls, snapCall{uuid, op, name})
		return cmp.Or(name, "lazyvirt-new"), nil
	}
	m, _ = send(m, key("tab")) // vms panel, talos-local-cp selected
	return m, calls
}

func TestSnapshotsPanelOpensInPlaceOfIOCharts(t *testing.T) {
	m, _ := snapModel(t)
	before := lineOf(m, "─ disk read")
	m, cmd := send(m, key("p"))
	m = runAll(m, cmd)
	v := ansi.Strip(m.View())
	for _, want := range []string{"s1", "s2", "current", "─ cpu"} {
		if !strings.Contains(v, want) {
			t.Errorf("snapshot view missing %q", want)
		}
	}
	if got := lineOf(m, "─ snapshots"); got != before {
		t.Errorf("snapshots panel at line %d, disk read was at %d", got, before)
	}
	if m, _ = send(m, key("p")); lineOf(m, "─ snapshots") >= 0 {
		t.Error("p should close the snapshots panel")
	}
}

func TestSnapshotCreateRevertDelete(t *testing.T) {
	m, calls := snapModel(t)
	m, cmd := send(m, key("p"))
	m = runAll(m, cmd)

	m, cmd = send(m, key("n")) // create: no prompt
	m = runAll(m, cmd)
	if len(*calls) != 1 || (*calls)[0] != (snapCall{"u-cp", "create", ""}) {
		t.Fatalf("calls %v, want create", *calls)
	}

	m, _ = send(m, key("enter")) // s1 selected
	if !strings.Contains(m.View(), "revert talos-local-cp to s1? (y/n)") {
		t.Fatalf("missing revert prompt:\n%s", m.View())
	}
	m, cmd = send(m, key("y"))
	m = runAll(m, cmd)
	if (*calls)[1] != (snapCall{"u-cp", "revert", "s1"}) {
		t.Fatalf("calls %v, want revert s1", *calls)
	}

	m, _ = send(m, key("j"), key("d"))
	if !strings.Contains(m.View(), "delete snapshot s2 of talos-local-cp? (y/n)") {
		t.Fatalf("missing delete prompt:\n%s", m.View())
	}
	m, cmd = send(m, key("n"))
	m = runAll(m, cmd)
	if len(*calls) != 2 {
		t.Fatalf("n should cancel delete, calls %v", *calls)
	}
}

func TestActionErrorSurvivesTheFollowUpRefresh(t *testing.T) {
	var calls []call
	m, _ := send(newTestModel(&calls), snapMsg{vms: threeVMs, at: time.Now()})
	m, _ = send(m, actMsg{err: fmt.Errorf("worker-0: domain is already running")})
	m, _ = send(m, snapMsg{vms: threeVMs, at: time.Now()}) // the refresh actMsg triggers
	if !strings.Contains(m.View(), "already running") {
		t.Fatalf("action error wiped by refresh:\n%s", strings.Split(m.View(), "\n")[m.height-1])
	}
	m, _ = send(m, snapDoneMsg{op: "create", err: fmt.Errorf("internal snapshot for disk vdb unsupported for storage type raw")})
	m, _ = send(m, snapMsg{vms: threeVMs, at: time.Now()})
	if !strings.Contains(m.View(), "storage type raw") {
		t.Fatal("snapshot error wiped by refresh")
	}
	m, _ = send(m, key("j")) // the next keypress dismisses it
	if strings.Contains(m.View(), "storage type raw") {
		t.Error("error should clear on the next key")
	}
}

func TestLifecycleEventRefreshesAndRearms(t *testing.T) {
	var calls []call
	m := newTestModel(&calls)
	ch := make(chan struct{}, 1)
	ch <- struct{}{} // the next event, already queued
	m.events = ch
	_, cmd := send(m, eventMsg{})
	var snaps, events int
	for _, msg := range msgsOf(cmd) {
		switch msg := msg.(type) {
		case snapMsg:
			snaps++
			if msg.tick {
				t.Error("event refresh must not start a tick loop")
			}
		case eventMsg:
			events++
		}
	}
	if snaps != 1 || events != 1 {
		t.Fatalf("got %d refreshes and %d re-armed events, want 1 and 1", snaps, events)
	}
}

func TestFilterVMs(t *testing.T) {
	vms := []virt.VM{{Name: "talos-local-cp"}, {Name: "talos-local-worker-0"}, {Name: "win11"}, {Name: "other-talos-local-x"}}
	names := func(vs []virt.VM) []string {
		var out []string
		for _, v := range vs {
			out = append(out, v.Name)
		}
		return out
	}
	if got := names(filterVMs(vms, "talos-local")); !slices.Equal(got, []string{"talos-local-cp", "talos-local-worker-0"}) {
		t.Errorf("prefix: %v", got)
	}
	if got := names(filterVMs(vms, "")); len(got) != 4 {
		t.Errorf("no prefix: %v", got)
	}
}
