package ui

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/konkerama/lazyvirt/internal/virt"
)

// Group is a set of VMs compared side by side in the groups view.
type Group struct {
	Name string
	VMs  []virt.VM
}

// groupKey derives a VM's group from its name: drop a trailing number, then
// the last segment. talos-local-worker-0 and talos-local-cp -> talos-local.
func groupKey(name string) string {
	s := name
	if i := strings.LastIndex(s, "-"); i > 0 {
		if _, err := strconv.Atoi(s[i+1:]); err == nil {
			s = s[:i]
		}
	}
	if i := strings.LastIndex(s, "-"); i > 0 {
		s = s[:i]
	}
	return s
}

// buildGroups returns "all" plus every prefix group with 2+ VMs, by name.
func buildGroups(vms []virt.VM) []Group {
	by := map[string][]virt.VM{}
	for _, v := range vms {
		k := groupKey(v.Name)
		by[k] = append(by[k], v)
	}
	out := []Group{{Name: "all", VMs: vms}}
	for k, members := range by {
		if len(members) > 1 {
			out = append(out, Group{Name: k, VMs: members})
		}
	}
	slices.SortFunc(out[1:], func(a, b Group) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// shortName strips the group prefix for legends: worker-0, not talos-local-worker-0.
func shortName(name, group string) string {
	if s, ok := strings.CutPrefix(name, group+"-"); ok {
		return s
	}
	return name
}

type plotCell struct {
	ch    rune // box-drawing glyph, 0 if empty
	owner int  // index of the last series drawn here, -1 if empty
}

// plotLines draws each series as a connected box-drawing line (lazydocker /
// asciigraph style) on a shared w x h grid scaled to maxV, one sample per
// column, newest at the right edge. Where series cross, the later one wins.
func plotLines(series [][]float64, maxV float64, w, h int) [][]plotCell {
	grid := make([][]plotCell, h)
	for r := range grid {
		grid[r] = make([]plotCell, w)
		for c := range grid[r] {
			grid[r][c].owner = -1
		}
	}
	row := func(v float64) int { // row from the top
		if maxV <= 0 {
			return h - 1
		}
		return h - 1 - int(math.Round(min(max(v, 0), maxV)/maxV*float64(h-1)))
	}
	for si, vals := range series {
		if len(vals) > w {
			vals = vals[len(vals)-w:]
		}
		pad := w - len(vals)
		set := func(y, x int, ch rune) { grid[y][x] = plotCell{ch, si} }
		prev := -1
		for i, v := range vals {
			x, y := pad+i, row(v)
			switch {
			case prev < 0 || prev == y:
				set(y, x, '─')
			case y < prev: // rising
				set(prev, x, '╯')
				set(y, x, '╭')
			default: // falling
				set(prev, x, '╮')
				set(y, x, '╰')
			}
			for yy := min(y, prev) + 1; prev >= 0 && yy < max(y, prev); yy++ {
				set(yy, x, '│')
			}
			prev = y
		}
	}
	return grid
}

// renderPlot turns a grid into colored rows, one color per owning series.
// Runs of same-owner cells share one Render call; lipgloss is the frame cost.
func renderPlot(grid [][]plotCell, colors []string) []string {
	styles := make([]lipgloss.Style, len(colors))
	for i, c := range colors {
		styles[i] = fg(c)
	}
	rows := make([]string, len(grid))
	var b strings.Builder
	run := make([]rune, 0, 64)
	for r, cells := range grid {
		b.Reset()
		owner := -1
		flush := func() {
			if owner < 0 {
				b.WriteString(string(run))
			} else {
				b.WriteString(styles[owner%len(styles)].Render(string(run)))
			}
			run = run[:0]
		}
		for _, c := range cells {
			// Blank cells join any run: color on an empty glyph is invisible.
			if c.owner >= 0 && c.owner != owner {
				flush()
				owner = c.owner
			}
			if c.ch == 0 {
				run = append(run, ' ')
			} else {
				run = append(run, c.ch)
			}
		}
		flush()
		rows[r] = b.String()
	}
	return rows
}

// --- groups view ------------------------------------------------------------

// Series colors, picked to stay apart on dark backgrounds.
var seriesColors = []string{"#7aa2f7", "#50f095", "#ff79c6", "#f2e266", "#ff9e64", "#8be9fd", "#bd93f9", "#fa1e1e"}

// groupNav is the groups view's selection: the selected group, the
// highlighted VM inside it (always one when the group has VMs) and its index.
func (m model) groupNav() (groups []Group, gi, vi int) {
	groups = buildGroups(m.visible())
	gi = max(slices.IndexFunc(groups, func(g Group) bool { return g.Name == m.gsel }), 0)
	vi = max(slices.IndexFunc(groups[gi].VMs, func(v virt.VM) bool { return v.UUID == m.gvm }), 0)
	return groups, gi, vi
}

// groupKey drives the sidebar: groups on top, the selected group's VMs below.
// j/k move in the focused panel; tab/h/l switch focus, which also switches
// the charts between the whole group and the selected VM. The highlighted
// VM is kept across group changes when the new group contains it.
func (m model) groupKey(s string) (tea.Model, tea.Cmd) {
	groups, gi, vi := m.groupNav()
	vmsFocused := m.gfocus == focusVMs
	move := func(i, n, d int) int { return min(max(i+d, 0), max(n-1, 0)) }
	switch s {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "l", "right":
		m.gfocus = (m.gfocus + 1) % 3
	case "shift+tab", "h", "left":
		m.gfocus = (m.gfocus + 2) % 3
	case "enter":
		m.gfocus = focusVMs
	case "esc":
		m.gfocus = focusGroups
	case "j", "down", "k", "up", "g", "home", "G", "end":
		if m.gfocus == focusHost {
			return m, nil
		}
		d := map[string]int{"j": 1, "down": 1, "k": -1, "up": -1, "g": -1 << 30, "home": -1 << 30, "G": 1 << 30, "end": 1 << 30}[s]
		if vmsFocused {
			vi = move(vi, len(groups[gi].VMs), d)
		} else {
			gi = move(gi, len(groups), d)
			// Keep the VM if the new group has it, else start at the top.
			vi = max(slices.IndexFunc(groups[gi].VMs, func(v virt.VM) bool { return v.UUID == m.gvm }), 0)
		}
	default:
		action, isAction := actionKeys[s]
		if s == "P" { // one key for both: what P does depends on state
			action, isAction = "suspend", true
			if vmsFocused && m.target().State == virt.StatePaused ||
				!vmsFocused && len(groupTargets(groups[gi].VMs, "suspend")) == 0 {
				action = "resume"
			}
		}
		switch {
		case s == "a" && vmsFocused:
			if v := m.target(); v.UUID != "" {
				return m, m.actCmd("autostart", []virt.VM{v})
			}
			return m, nil
		case s == "a":
			m.status = "tab to the vms panel to pick a VM"
		case s == "s" && vmsFocused:
			return m, m.shellCmd()
		case s == "p" && vmsFocused:
			m.snapOpen, m.snapList, m.snapCur = true, nil, 0
			return m, m.snapsCmd()
		case s == "p":
			m.status = "tab to the vms panel to pick a VM"
		case s == "s", isAction && m.gfocus == focusHost:
			m.status = "tab to the vms panel to pick a VM"
		case !isAction:
		case vmsFocused:
			v := m.target()
			if v.UUID == "" {
				return m, nil
			}
			if action != "destroy" {
				return m, m.actCmd(action, []virt.VM{v})
			}
			m.confirm = pending{action: action, prompt: fmt.Sprintf("force off %s? (y/n)", v.Name), vms: []virt.VM{v}}
		default: // groups panel: the whole group, always confirmed
			g := groups[gi]
			vms := groupTargets(g.VMs, action)
			if len(vms) == 0 {
				m.status = fmt.Sprintf("nothing to %s in %s", actionVerbs[action], g.Name)
				return m, nil
			}
			plural := "s"
			if len(vms) == 1 {
				plural = ""
			}
			m.confirm = pending{action: action, prompt: fmt.Sprintf("%s %d VM%s in %s? (y/n)", actionVerbs[action], len(vms), plural, g.Name), vms: vms}
		}
		return m, nil
	}
	m.gsel, m.gvm = groups[gi].Name, ""
	if vms := groups[gi].VMs; len(vms) > 0 {
		m.gvm = vms[vi].UUID
	}
	return m, nil
}

// groupTargets keeps the VMs an action applies to, so a group start skips
// running VMs instead of failing on them.
func groupTargets(vms []virt.VM, action string) []virt.VM {
	ok := map[string]func(virt.State) bool{
		"start":    func(s virt.State) bool { return s == virt.StateShutoff || s == virt.StateCrashed },
		"shutdown": func(s virt.State) bool { return s == virt.StateRunning },
		"reboot":   func(s virt.State) bool { return s == virt.StateRunning },
		"destroy": func(s virt.State) bool {
			return s == virt.StateRunning || s == virt.StatePaused || s == virt.StateCrashed
		},
		"suspend": func(s virt.State) bool { return s == virt.StateRunning },
		"resume":  func(s virt.State) bool { return s == virt.StatePaused },
	}[action]
	var out []virt.VM
	for _, v := range vms {
		if ok != nil && ok(v.State) {
			out = append(out, v)
		}
	}
	return out
}

// drawOrder lists series indexes with sel last, so the selected VM's line is
// drawn on top and owns any cell it shares with another line.
func drawOrder(n, sel int) []int {
	out := make([]int, 0, n)
	for i := range n {
		if i != sel {
			out = append(out, i)
		}
	}
	if sel >= 0 && sel < n {
		out = append(out, sel)
	}
	return out
}

// scrollStart is the first row to show so row cur stays visible in rows lines.
func scrollStart(n, cur, rows int) int {
	return max(0, min(cur-rows+1, n-rows))
}

func (m model) groupView(h int) string {
	groups, cur, vcur := m.groupNav()
	g := groups[cur]

	longest, longestVM := 0, 0
	// Size for every group's rows, not just the selected one, so the sidebar
	// doesn't jump as the selection moves.
	for _, gr := range groups {
		longest = max(longest, len(gr.Name))
		for _, v := range gr.VMs {
			longestVM = max(longestVM, len(shortName(v.Name, gr.Name)))
		}
	}
	leftW := max(min(max(longest+26, longestVM+24), m.width*2/5), 24)
	rightW := m.width - leftW
	inner := leftW - 3

	// Selected row: full highlight in the focused panel, accent text in the
	// other, so both selections stay visible.
	pick := func(focused bool, row string) string {
		if focused {
			return selStyle.Render(" " + row + strings.Repeat(" ", max(inner-lipgloss.Width(row), 0)))
		}
		return " " + fg(colAccent).Bold(true).Render(row)
	}
	border := func(focused bool) string {
		if focused {
			return colAccent
		}
		return colBorder
	}

	// Groups panel: fixed height (all groups, capped at a third of the
	// screen) so the vms panel below never moves.
	gh := min(len(groups)+2, max(h/3, 5))
	nameW := max(inner-21, 6)
	var glines []string
	for i, gr := range groups {
		sum, n := 0.0, 0
		for _, v := range gr.VMs {
			if v.State == virt.StateRunning {
				sum += m.rates[v.UUID].CPU
				n++
			}
		}
		avg := 0.0
		if n > 0 {
			avg = sum / float64(n)
		}
		cnt := fmt.Sprintf("%2d vm", len(gr.VMs))
		if i == cur {
			glines = append(glines, pick(m.gfocus == focusGroups,
				fmt.Sprintf("▶ %-*s %s %s %5.1f%%", nameW, trunc(gr.Name, nameW), cnt, ansi.Strip(gauge(avg, 5)), avg)))
			continue
		}
		glines = append(glines, fmt.Sprintf(" %s %-*s %s %s %5.1f%%", fg(colAccent).Render("◆"), nameW,
			trunc(gr.Name, nameW), dim.Render(cnt), gauge(avg, 5), avg))
	}
	glines = glines[scrollStart(len(glines), cur, gh-2):]
	gpanel := panel("groups", dim.Render(fmt.Sprint(len(groups))), glines, leftW, gh, border(m.gfocus == focusGroups))

	// VMs panel: the selected group's members with chart color and state.
	vmW := max(inner-20, 6) // row: 3 lead, swatch 2, state 9, cpu 7
	var vlines []string
	for j, v := range g.VMs {
		cpu, swatch := "", colDim // stopped VMs draw no line
		if v.State == virt.StateRunning {
			cpu, swatch = fmt.Sprintf("%5.1f%%", m.rates[v.UUID].CPU), seriesColors[j%len(seriesColors)]
		}
		name := trunc(shortName(v.Name, g.Name), vmW)
		if j == vcur {
			vlines = append(vlines, pick(m.gfocus == focusVMs, fmt.Sprintf("▶ ━ %-*s %-8s %6s", vmW, name, v.State, cpu)))
			continue
		}
		vlines = append(vlines, fmt.Sprintf("   %s %-*s %s %6s",
			fg(swatch).Render("━"), vmW, name,
			fg(cmp.Or(stateColor[v.State], colDim)).Render(fmt.Sprintf("%-8s", v.State)), cpu))
	}
	if len(g.VMs) == 0 {
		vlines = append(vlines, dim.Render(" no domains"))
	}
	// Info panel: fixed height at the bottom, always the selected VM.
	ih := min(max(h/3, 6), 14)
	const hh = 6 // host panel: 4 gauge lines
	vh := h - gh - ih - hh
	vlines = vlines[scrollStart(len(vlines), vcur, vh-2):]
	vpanel := panel("vms", dim.Render(g.Name), vlines, leftW, vh, border(m.gfocus == focusVMs))
	sel := m.target()
	var ilines []string
	if sel.UUID != "" {
		for l := range strings.SplitSeq(cmp.Or(m.info[sel.UUID], dim.Render("loading")), "\n") {
			ilines = append(ilines, " "+l)
		}
	}
	ipanel := panel("info", dim.Render(shortName(sel.Name, g.Name)), ilines, leftW, ih, colBorder)
	left := gpanel + "\n" + vpanel + "\n" + ipanel + "\n" + m.hostPanel(leftW, hh, border(m.gfocus == focusHost))

	// Header: group name + color legend shared by every chart.
	legend := ""
	for i, v := range g.VMs {
		c := seriesColors[i%len(seriesColors)]
		if v.State != virt.StateRunning {
			c = colDim
		}
		label := "● " + shortName(v.Name, g.Name)
		if v.State != virt.StateRunning {
			label += " (" + v.State.String() + ")"
		}
		st := fg(c)
		if i == vcur {
			st = st.Bold(true).Underline(true)
		}
		legend += st.Render(label) + "  "
	}
	head := " " + titleStyle.Render(g.Name) + dim.Render(fmt.Sprintf("  %d vms   ", len(g.VMs))) + legend
	vmFocus := m.gfocus == focusVMs && sel.UUID != ""
	if vmFocus {
		head = " " + fg(seriesColors[vcur%len(seriesColors)]).Render("━ ") + titleStyle.Render(sel.Name) + "  " +
			fg(cmp.Or(stateColor[sel.State], colDim)).Render(sel.State.String()) + dim.Render("   "+g.Name+"   "+sel.UUID)
	}
	header := ansi.Truncate(head, rightW, "…")
	header += strings.Repeat(" ", max(rightW-lipgloss.Width(header), 0))

	pct := func(r virt.Rates) float64 {
		if r.MemTotalKiB == 0 {
			return 0
		}
		return r.MemKiB / r.MemTotalKiB * 100
	}
	type metric struct {
		name   string
		f      func(virt.Rates) float64
		max    float64 // 0 = autoscale to the visible peak
		format func(float64) string
		detail func(virt.Rates) string // title in VM focus; nil = format + visible peak
	}
	perc := func(x float64) string { return fmt.Sprintf("%.0f%%", x) }
	rate := func(x float64) string { return virt.HumanBytes(x) + "/s" }
	metrics := []metric{
		{"cpu", func(r virt.Rates) float64 { return r.CPU }, 100, perc, func(r virt.Rates) string {
			return fmt.Sprintf("%.1f%%", r.CPU) + dim.Render(fmt.Sprintf(" · %d vCPU", sel.VCPUs))
		}},
		{"mem", pct, 100, perc, func(r virt.Rates) string {
			src := "host rss"
			if r.GuestMem {
				src = "guest"
			}
			return fmt.Sprintf("%.0f%%  %s / %s", pct(r), virt.HumanBytes(r.MemKiB*1024), virt.HumanBytes(r.MemTotalKiB*1024)) + dim.Render(" · "+src)
		}},
		{"disk read", func(r virt.Rates) float64 { return r.Rd }, 0, rate, nil},
		{"disk write", func(r virt.Rates) float64 { return r.Wr }, 0, rate, nil},
		{"net rx", func(r virt.Rates) float64 { return r.Rx }, 0, rate, nil},
		{"net tx", func(r virt.Rates) float64 { return r.Tx }, 0, rate, nil},
	}

	ph := (h - 1) / 3
	colW := rightW / 2
	chart := func(mt metric, w, ph int) string {
		gw := w - 2
		var lines [][]float64
		var colors []string
		var info []string
		peak := 1024.0
		members := drawOrder(len(g.VMs), vcur)
		if vmFocus {
			members = []int{vcur}
		}
		for _, i := range members {
			v := g.VMs[i]
			if v.State != virt.StateRunning {
				continue
			}
			vals := series(m.hist[v.UUID], mt.f)
			for _, x := range vals[max(len(vals)-gw, 0):] {
				peak = max(peak, x)
			}
			lines = append(lines, vals)
			colors = append(colors, seriesColors[i%len(seriesColors)])
		}
		// Title values stay in member order so they don't shuffle as the
		// selection moves; the selected one is bold.
		for i, v := range g.VMs {
			if v.State != virt.StateRunning {
				continue
			}
			st := fg(seriesColors[i%len(seriesColors)])
			if i == vcur {
				st = st.Bold(true)
			}
			info = append(info, st.Render(mt.format(mt.f(m.rates[v.UUID]))))
		}
		maxV := mt.max
		if maxV == 0 {
			maxV = peak
		}
		if vmFocus { // one VM: richer title instead of one value per VM
			info = nil
			if sel.State == virt.StateRunning {
				r := m.rates[sel.UUID]
				t := mt.format(mt.f(r)) + dim.Render("  peak "+mt.format(peak))
				if mt.detail != nil {
					t = mt.detail(r)
				}
				info = []string{fg(colText).Render(t)}
			}
		}
		return panel(mt.name, strings.Join(info, " "), renderPlot(plotLines(lines, maxV, gw, ph-2), colors), w, ph, colBorder)
	}
	var rows []string
	for i := 0; i < len(metrics); i += 2 {
		rh := ph
		if i == 4 {
			rh = h - 1 - 2*ph // last row takes the remainder
		}
		if i == 2 && vmFocus && m.snapOpen { // snapshots take rows 2-3
			rows = append(rows, m.snapPanel(rightW, h-1-ph))
			break
		}
		rows = append(rows, hjoin(chart(metrics[i], colW, rh), chart(metrics[i+1], rightW-colW, rh)))
	}
	right := header + "\n" + strings.Join(rows, "\n")
	if m.gfocus == focusHost {
		right = m.hostView(rightW, h, ph)
	}
	return hjoin(left, right) + "\n" + m.footer()
}
