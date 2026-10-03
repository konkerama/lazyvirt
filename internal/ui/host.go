package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/konkerama/lazyvirt/internal/virt"
)

// HostSample is one tick of host load for the host charts.
type HostSample struct{ CPU, Mem float64 }

func (m *model) applyHost(h virt.Host) {
	if m.hostNow.CPUTotal > 0 {
		m.hostHist = append(m.hostHist, HostSample{CPU: virt.HostCPUPct(m.hostNow, h), Mem: h.MemPct()})
		if len(m.hostHist) > histLen {
			m.hostHist = m.hostHist[len(m.hostHist)-histLen:]
		}
	}
	m.hostNow = h
}

// storageCmd fetches pools and networks, only while the host view shows them.
func (m model) storageCmd() tea.Cmd {
	if m.storage == nil || m.gfocus != focusHost {
		return nil
	}
	return func() tea.Msg {
		pools, nets, err := m.storage()
		return storageMsg{pools: pools, nets: nets, err: err}
	}
}

// hostPanel: host load and how much of it running VMs have claimed.
func (m model) hostPanel(w, h int, border string) string {
	hn := m.hostNow
	if hn.CPUs == 0 {
		return panel("host", "", []string{dim.Render(" no host data")}, w, h, border)
	}
	cpu := 0.0
	if len(m.hostHist) > 0 {
		cpu = m.hostHist[len(m.hostHist)-1].CPU
	}
	vcpus, memAlloc := virt.Allocated(m.all)
	used := float64(hn.MemKiB - min(hn.AvailKiB, hn.MemKiB))
	pct := func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b * 100
	}
	line := func(label string, p float64, text string) string {
		return fmt.Sprintf(" %-5s %s %s", label, gauge(p, 10), text)
	}
	lines := []string{
		line("cpu", cpu, fmt.Sprintf("%3.0f%%", cpu)),
		line("mem", hn.MemPct(), fmt.Sprintf("%s / %s", virt.HumanBytes(used*1024), virt.HumanBytes(float64(hn.MemKiB)*1024))),
		line("vcpu", pct(float64(vcpus), float64(hn.CPUs)), fmt.Sprintf("%d / %d alloc", vcpus, hn.CPUs)),
		line("ram", pct(float64(memAlloc), float64(hn.MemKiB)),
			fmt.Sprintf("%s / %s alloc", virt.HumanBytes(float64(memAlloc)*1024), virt.HumanBytes(float64(hn.MemKiB)*1024))),
	}
	return panel("host", dim.Render(hn.Name), lines, w, h, border)
}

// hostView replaces the charts while the host panel is focused, on the same
// grid: row 1 host cpu | mem, rows 2-3 pools | networks.
func (m model) hostView(w, h, ph int) string {
	hn := m.hostNow
	header := ansi.Truncate(" "+titleStyle.Render("host "+hn.Name)+
		dim.Render(fmt.Sprintf("   %s · %d cpus · %s", hn.Model, hn.CPUs, virt.HumanBytes(float64(hn.MemKiB)*1024))), w, "…")
	header += strings.Repeat(" ", max(w-lipgloss.Width(header), 0))

	colW := w / 2
	chart := func(name string, f func(HostSample) float64, cw int) string {
		vals := make([]float64, len(m.hostHist))
		for i, s := range m.hostHist {
			vals[i] = f(s)
		}
		cur := 0.0
		if len(vals) > 0 {
			cur = vals[len(vals)-1]
		}
		plot := renderPlot(plotLines([][]float64{vals}, 100, cw-2, ph-2), []string{colAccent})
		return panel(name, fg(colText).Render(fmt.Sprintf("%.0f%%", cur)), plot, cw, ph, colBorder)
	}
	row1 := hjoin(chart("cpu", func(s HostSample) float64 { return s.CPU }, colW),
		chart("mem", func(s HostSample) float64 { return s.Mem }, w-colW))

	bh := h - 1 - ph // rows 2+3 of the chart grid
	var pl []string
	for _, p := range m.pools {
		if !p.Active {
			pl = append(pl, fmt.Sprintf(" %-16s %s", trunc(p.Name, 16), dim.Render("inactive")))
			continue
		}
		u := 0.0
		if p.Capacity > 0 {
			u = float64(p.Allocation) / float64(p.Capacity) * 100
		}
		pl = append(pl, fmt.Sprintf(" %-16s %s %3.0f%%  %s / %s", trunc(p.Name, 16), gauge(u, 10), u,
			virt.HumanBytes(float64(p.Allocation)), virt.HumanBytes(float64(p.Capacity))))
	}
	var nl []string
	for _, n := range m.nets {
		dot := fg(colRunning).Render("●")
		if !n.Active {
			dot = dim.Render("●")
		}
		nl = append(nl, fmt.Sprintf(" %s %s %s", dot, titleStyle.Render(n.Name), dim.Render(n.Bridge)))
		for _, l := range n.Leases {
			nl = append(nl, fmt.Sprintf("   %-15s %s %s", l.IP, dim.Render(l.MAC), l.Hostname))
		}
	}
	row23 := hjoin(panel("pools", dim.Render(fmt.Sprint(len(m.pools))), pl, colW, bh, colBorder),
		panel("networks", dim.Render(fmt.Sprint(len(m.nets))), nl, w-colW, bh, colBorder))
	return header + "\n" + row1 + "\n" + row23
}
