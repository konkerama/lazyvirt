package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/digitalocean/go-libvirt"
)

// Host is the hypervisor's own capacity and load. CPU counters are
// cumulative ns across all CPUs; memory is KiB.
type Host struct {
	Name, Model       string
	CPUs              int
	MemKiB, AvailKiB  uint64
	CPUBusy, CPUTotal uint64
}

type Pool struct {
	Name                 string
	Active               bool
	Capacity, Allocation uint64 // bytes
}

type Lease struct{ IP, MAC, Hostname string }

type Net struct {
	Name, Bridge string
	Active       bool
	Leases       []Lease
}

func parseHostCPU(stats []libvirt.NodeGetCPUStats) (busy, total uint64) {
	for _, s := range stats {
		switch s.Field {
		case "kernel", "user":
			busy += s.Value
			total += s.Value
		case "idle", "iowait":
			total += s.Value
		}
	}
	return busy, total
}

// parseHostMem returns total and available KiB; page cache and buffers count
// as available, as in free(1).
func parseHostMem(stats []libvirt.NodeGetMemoryStats) (total, avail uint64) {
	for _, s := range stats {
		switch s.Field {
		case "total":
			total = s.Value
		case "free", "buffers", "cached":
			avail += s.Value
		}
	}
	return total, avail
}

func hostCPUPct(prev, cur Host) float64 {
	if cur.CPUTotal <= prev.CPUTotal || cur.CPUBusy < prev.CPUBusy {
		return 0
	}
	return float64(cur.CPUBusy-prev.CPUBusy) / float64(cur.CPUTotal-prev.CPUTotal) * 100
}

// allocated sums what running VMs hold: vCPUs and their configured memory.
func allocated(vms []VM) (vcpus int, memKiB uint64) {
	for _, v := range vms {
		if v.State == StateRunning {
			vcpus += v.VCPUs
			memKiB += v.MaxMemKiB
		}
	}
	return vcpus, memKiB
}

// host reads node load: 2 RPCs per tick. Model, CPU count, hostname and the
// stats field counts never change, so they are read once per connection.
func (c *conn) host() (Host, error) {
	l, err := c.get()
	if err != nil {
		return Host{}, err
	}
	c.mu.Lock() // fetches from the tick loop and from actions/events can overlap
	h, cpuN, memN := c.hostInfo, c.cpuParams, c.memParams
	c.mu.Unlock()
	if h.CPUs == 0 {
		model, _, cpus, _, _, _, _, _, err := l.NodeGetInfo()
		if err != nil {
			return Host{}, err
		}
		h = Host{CPUs: int(cpus)}
		for _, b := range model {
			if b == 0 {
				break
			}
			h.Model += string(rune(b))
		}
		h.Name, _ = l.ConnectGetHostname()
		// libvirt wants the exact field count, so ask for it (nparams=0).
		if _, cpuN, err = l.NodeGetCPUStats(-1, 0, 0); err != nil {
			return Host{}, err
		}
		if _, memN, err = l.NodeGetMemoryStats(0, -1, 0); err != nil {
			return Host{}, err
		}
		c.mu.Lock()
		c.hostInfo, c.cpuParams, c.memParams = h, cpuN, memN
		c.mu.Unlock()
	}
	cpu, _, err := l.NodeGetCPUStats(-1, cpuN, 0)
	if err != nil {
		return Host{}, err
	}
	h.CPUBusy, h.CPUTotal = parseHostCPU(cpu)
	mem, _, err := l.NodeGetMemoryStats(memN, -1, 0)
	if err != nil {
		return Host{}, err
	}
	h.MemKiB, h.AvailKiB = parseHostMem(mem)
	return h, nil
}

// storage lists pools and networks with their DHCP leases. Only fetched while
// the host panel is focused.
func (c *conn) storage() ([]Pool, []Net, error) {
	l, err := c.get()
	if err != nil {
		return nil, nil, err
	}
	rawPools, _, err := l.ConnectListAllStoragePools(1, 0)
	if err != nil {
		return nil, nil, err
	}
	var pools []Pool
	for _, p := range rawPools {
		state, capacity, alloc, _, err := l.StoragePoolGetInfo(p)
		if err != nil {
			continue // pool vanished between list and info
		}
		pools = append(pools, Pool{Name: p.Name, Active: state == uint8(libvirt.StoragePoolRunning),
			Capacity: capacity, Allocation: alloc})
	}
	rawNets, _, err := l.ConnectListAllNetworks(1, 0)
	if err != nil {
		return nil, nil, err
	}
	var nets []Net
	for _, n := range rawNets {
		active, _ := l.NetworkIsActive(n)
		net := Net{Name: n.Name, Active: active == 1}
		net.Bridge, _ = l.NetworkGetBridgeName(n)
		if net.Active {
			leases, _, _ := l.NetworkGetDhcpLeases(n, nil, 1, 0)
			for _, ls := range leases {
				lease := Lease{IP: ls.Ipaddr}
				if len(ls.Mac) > 0 {
					lease.MAC = ls.Mac[0]
				}
				if len(ls.Hostname) > 0 {
					lease.Hostname = ls.Hostname[0]
				}
				net.Leases = append(net.Leases, lease)
			}
		}
		nets = append(nets, net)
	}
	return pools, nets, nil
}

// --- host UI ----------------------------------------------------------------

// HostSample is one tick of host load for the host charts.
type HostSample struct{ CPU, Mem float64 }

func (h Host) memPct() float64 {
	if h.MemKiB == 0 {
		return 0
	}
	return float64(h.MemKiB-min(h.AvailKiB, h.MemKiB)) / float64(h.MemKiB) * 100
}

func (m *model) applyHost(h Host) {
	if m.hostNow.CPUTotal > 0 {
		m.hostHist = append(m.hostHist, HostSample{CPU: hostCPUPct(m.hostNow, h), Mem: h.memPct()})
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
	vcpus, memAlloc := allocated(m.all)
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
		line("mem", hn.memPct(), fmt.Sprintf("%s / %s", humanBytes(used*1024), humanBytes(float64(hn.MemKiB)*1024))),
		line("vcpu", pct(float64(vcpus), float64(hn.CPUs)), fmt.Sprintf("%d / %d alloc", vcpus, hn.CPUs)),
		line("ram", pct(float64(memAlloc), float64(hn.MemKiB)),
			fmt.Sprintf("%s / %s alloc", humanBytes(float64(memAlloc)*1024), humanBytes(float64(hn.MemKiB)*1024))),
	}
	return panel("host", dim.Render(hn.Name), lines, w, h, border)
}

// hostView replaces the charts while the host panel is focused, on the same
// grid: row 1 host cpu | mem, rows 2-3 pools | networks.
func (m model) hostView(w, h, ph int) string {
	hn := m.hostNow
	header := ansi.Truncate(" "+titleStyle.Render("host "+hn.Name)+
		dim.Render(fmt.Sprintf("   %s · %d cpus · %s", hn.Model, hn.CPUs, humanBytes(float64(hn.MemKiB)*1024))), w, "…")
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
			humanBytes(float64(p.Allocation)), humanBytes(float64(p.Capacity))))
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
