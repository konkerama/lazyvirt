package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// histLen samples per VM: 2 per braille cell, enough for a ~200-column graph.
const histLen = 400

type (
	tickMsg time.Time
	snapMsg struct {
		vms []VM
		at  time.Time
		err error
	}
	actMsg  struct{ err error }
	infoMsg struct {
		key, text string
		err       error
	}
)

type model struct {
	fetch    func() ([]VM, error)
	act      func(uuid, action string) error
	details  func(uuid string) (string, error)
	ip       func(uuid string) (string, error) // for the s shell key
	uri      string                            // libvirt URI: ssh jump host, virsh console
	sshUser  string
	prefix   string
	interval time.Duration

	all    []VM // latest snapshot, prefix-filtered
	prev   map[string]VM
	prevAt time.Time
	rates  map[string]Rates
	hist   map[string][]Rates
	info   map[string]string // details text by UUID

	confirm string // pending action awaiting y/n
	gsel    string // selected group name
	gvm     string // UUID of the VM highlighted inside it
	gfocus  int    // 0 groups panel focused (group charts), 1 vms panel (VM charts)
	status  string
	err     error

	width, height int
}

func newModel(fetch func() ([]VM, error), act func(uuid, action string) error,
	details func(uuid string) (string, error), prefix string, interval time.Duration) model {
	return model{fetch: fetch, act: act, details: details, prefix: prefix, interval: interval,
		prev: map[string]VM{}, rates: map[string]Rates{}, hist: map[string][]Rates{}, info: map[string]string{}}
}

func (m model) Init() tea.Cmd { return m.fetchCmd() }

func (m model) fetchCmd() tea.Cmd {
	return func() tea.Msg {
		vms, err := m.fetch()
		return snapMsg{vms: vms, at: time.Now(), err: err}
	}
}

func (m model) tickCmd() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// infoCmd loads the selected VM's details. force refetches a cached entry:
// IPs appear only after a guest boots, so the info panel refreshes per tick.
func (m model) infoCmd(force bool) tea.Cmd {
	v := m.target()
	if v.UUID == "" || (!force && m.info[v.UUID] != "") {
		return nil
	}
	return func() tea.Msg {
		text, err := m.details(v.UUID)
		return infoMsg{key: v.UUID, text: text, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		return m, m.fetchCmd()
	case snapMsg:
		m.err = msg.err
		if msg.err == nil {
			m.applySnap(msg.vms, msg.at)
		}
		// Next fetch is scheduled only after this one lands, so a slow
		// hypervisor never piles up requests.
		return m, tea.Batch(m.tickCmd(), m.infoCmd(true))
	case actMsg:
		m.err = msg.err
		return m, m.fetchCmd()
	case shellMsg:
		return m, execShell(msg.name, msg.argv, msg.ssh)
	case shellDoneMsg:
		return m.shellDone(msg)
	case infoMsg:
		if msg.err != nil {
			m.info[msg.key] = "error: " + msg.err.Error()
		} else {
			m.info[msg.key] = msg.text
		}
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) applySnap(vms []VM, at time.Time) {
	vms = filterVMs(vms, m.prefix)
	dt := at.Sub(m.prevAt)
	next := make(map[string]VM, len(vms))
	for _, v := range vms {
		if p, ok := m.prev[v.UUID]; ok {
			r := computeRates(p, v, dt)
			m.rates[v.UUID] = r
			h := append(m.hist[v.UUID], r)
			if len(h) > histLen {
				h = h[len(h)-histLen:]
			}
			m.hist[v.UUID] = h
		}
		next[v.UUID] = v
	}
	for id := range m.prev {
		if _, ok := next[id]; !ok { // undefined VM: drop its history
			delete(m.rates, id)
			delete(m.hist, id)
			delete(m.info, id)
		}
	}
	m.prev, m.prevAt, m.all = next, at, vms
}

var actionKeys = map[string]string{"u": "start", "S": "shutdown", "r": "reboot", "F": "destroy"}

func (m model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if m.confirm != "" {
		a := m.confirm
		m.confirm = ""
		if s == "y" {
			return m, m.actCmd(a)
		}
		m.status = "cancelled"
		return m, nil
	}
	if s == "R" {
		return m, m.fetchCmd()
	}
	prev := m.target().UUID
	tm, cmd := m.groupKey(s)
	if next := tm.(model); next.target().UUID != prev {
		// A new selection loads its info panel unless already cached.
		return next, tea.Batch(cmd, next.infoCmd(false))
	}
	return tm, cmd
}

// target is the VM that action and shell keys apply to.
func (m model) target() VM {
	groups, gi, vi := m.groupNav()
	if vms := groups[gi].VMs; len(vms) > 0 {
		return vms[vi]
	}
	return VM{}
}

func (m *model) actCmd(action string) tea.Cmd {
	v := m.target()
	m.status = action + " " + v.Name
	return func() tea.Msg { return actMsg{err: m.act(v.UUID, action)} }
}

// --- view -----------------------------------------------------------------

// Palette loosely after btop's default theme: gradients run from a graph's
// tip color down to its base color.
const (
	colAccent  = "#7aa2f7"
	colDim     = "#5c6370"
	colBorder  = "#3b4048"
	colText    = "#c0caf5"
	colRunning = "#50f095"
	colPaused  = "#f2e266"
	colCrashed = "#fa1e1e"
)

var (
	cpuGrad   = []string{"#fa1e1e", "#f2e266", "#50f095"} // hot at the top
	memGrad   = []string{"#ff79c6", "#bd93f9", "#6272a4"}
	readGrad  = []string{"#8be9fd", "#4fa3d1"}
	writeGrad = []string{"#ffb86c", "#ff5555"}
	rxGrad    = []string{"#50fa7b", "#2e9e5b"}
	txGrad    = []string{"#bd93f9", "#ff79c6"}

	fg         = func(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }
	dim        = fg(colDim)
	titleStyle = fg(colText).Bold(true)
	selStyle   = lipgloss.NewStyle().Background(lipgloss.Color("#2f3549")).Foreground(lipgloss.Color(colText)).Bold(true)
	errStyle   = fg(colCrashed).Bold(true)
	stateColor = map[State]string{StateRunning: colRunning, StatePaused: colPaused, StateCrashed: colCrashed}
)

func (m model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	h := m.height - 1 // footer
	if h < 10 || m.width < 60 {
		return "terminal too small"
	}
	return m.groupView(h)
}

// gauge is a w-cell meter for a 0-100 value, each cell colored by position.
func gauge(pct float64, w int) string {
	filled := int(math.Round(min(max(pct, 0), 100) / 100 * float64(w)))
	if pct > 0 && filled == 0 {
		filled = 1
	}
	var b strings.Builder
	for i := range w {
		if i >= filled {
			b.WriteString(fg(colBorder).Render("■"))
			continue
		}
		b.WriteString(fg(gradAt(cpuGrad, 1-float64(i)/float64(max(w-1, 1)))).Render("■"))
	}
	return b.String()
}

// gradAt samples a multi-stop gradient at t in [0,1].
func gradAt(stops []string, t float64) string {
	if len(stops) == 1 {
		return stops[0]
	}
	seg := min(int(t*float64(len(stops)-1)), len(stops)-2)
	return lerpColor(stops[seg], stops[seg+1], t*float64(len(stops)-1)-float64(seg))
}

// series pulls one metric out of the history.
func series(hist []Rates, f func(Rates) float64) []float64 {
	out := make([]float64, len(hist))
	for i, r := range hist {
		out[i] = f(r)
	}
	return out
}

func (m model) footer() string {
	switch {
	case m.confirm != "":
		return " " + errStyle.Render(fmt.Sprintf("force off %s? (y/n)", m.target().Name))
	case m.err != nil:
		return " " + errStyle.Render("error: "+m.err.Error())
	}
	keys := [][2]string{{"j/k", "move"}, {"tab", "groups/vms"}, {"s", "shell"}, {"u", "start"}, {"S", "shutdown"},
		{"r", "reboot"}, {"F", "force off"}, {"q", "quit"}}
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(" " + fg(colAccent).Bold(true).Render(k[0]) + " " + dim.Render(k[1]) + " ")
	}
	out := b.String()
	if m.status != "" {
		out = " " + fg(colText).Render(m.status) + " " + dim.Render("│") + out
	}
	return ansi.Truncate(out, m.width, "…")
}

// trunc cuts the middle out of long names: VM names share prefixes
// (talos-local-…) and differ at the end (worker-0, worker-1).
func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	head := (n - 1) / 3 // keep more of the end, where names differ
	return s[:head] + "…" + s[len(s)-(n-1-head):]
}

// hjoin puts equal-height blocks side by side. Every block here is built at an
// exact size, so unlike lipgloss.JoinHorizontal it skips re-measuring lines.
func hjoin(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range al {
		if i < len(bl) {
			al[i] += bl[i]
		}
	}
	return strings.Join(al, "\n")
}
