package virt

import (
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

func HostCPUPct(prev, cur Host) float64 {
	if cur.CPUTotal <= prev.CPUTotal || cur.CPUBusy < prev.CPUBusy {
		return 0
	}
	return float64(cur.CPUBusy-prev.CPUBusy) / float64(cur.CPUTotal-prev.CPUTotal) * 100
}

// Allocated sums what running VMs hold: vCPUs and their configured memory.
func Allocated(vms []VM) (vcpus int, memKiB uint64) {
	for _, v := range vms {
		if v.State == StateRunning {
			vcpus += v.VCPUs
			memKiB += v.MaxMemKiB
		}
	}
	return vcpus, memKiB
}

// Host reads node load: 2 RPCs per tick. Model, CPU count, hostname and the
// stats field counts never change, so they are read once per connection.
func (c *Conn) Host() (Host, error) {
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

// Storage lists pools and networks with their DHCP leases. Only fetched while
// the host panel is focused.
func (c *Conn) Storage() ([]Pool, []Net, error) {
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

func (h Host) MemPct() float64 {
	if h.MemKiB == 0 {
		return 0
	}
	return float64(h.MemKiB-min(h.AvailKiB, h.MemKiB)) / float64(h.MemKiB) * 100
}
