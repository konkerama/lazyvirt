package virt

import (
	"fmt"
	"strings"
	"time"

	"github.com/digitalocean/go-libvirt"
)

// State mirrors virDomainState.
type State int32

const (
	StateNoState State = iota
	StateRunning
	StateBlocked
	StatePaused
	StateShutdown
	StateShutoff
	StateCrashed
	StatePMSuspended
)

var stateNames = [...]string{"nostate", "running", "blocked", "paused", "shutdown", "shutoff", "crashed", "suspended"}

func (s State) String() string {
	if s < 0 || int(s) >= len(stateNames) {
		return "unknown"
	}
	return stateNames[s]
}

// VM is one domain's counters from a single ConnectGetAllDomainStats call.
// Byte counters are summed across all disks / NICs.
type VM struct {
	UUID, Name                         string
	State                              State
	VCPUs                              int
	CPUTime                            uint64 // ns, cumulative
	MemKiB, MaxMemKiB, RSSKiB          uint64
	AvailKiB, UsableKiB                uint64 // guest-reported, see MemStamp
	MemStamp                           uint64 // balloon.last-update; frozen = stale
	RdBytes, WrBytes, RxBytes, TxBytes uint64
}

func uuidString(u libvirt.UUID) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

func parseRecord(r libvirt.DomainStatsRecord) VM {
	vm := VM{UUID: uuidString(r.Dom.UUID), Name: r.Dom.Name}
	for _, p := range r.Params {
		f := p.Field
		switch f {
		case "state.state":
			vm.State = State(toU64(p.Value.I))
		case "cpu.time":
			vm.CPUTime = toU64(p.Value.I)
		case "vcpu.current":
			vm.VCPUs = int(toU64(p.Value.I))
		case "balloon.current":
			vm.MemKiB = toU64(p.Value.I)
		case "balloon.maximum":
			vm.MaxMemKiB = toU64(p.Value.I)
		case "balloon.rss":
			vm.RSSKiB = toU64(p.Value.I)
		case "balloon.available":
			vm.AvailKiB = toU64(p.Value.I)
		case "balloon.usable":
			vm.UsableKiB = toU64(p.Value.I)
		case "balloon.last-update":
			vm.MemStamp = toU64(p.Value.I)
		default:
			// block.N.rd.bytes / net.N.rx.bytes etc.
			switch {
			case strings.HasPrefix(f, "block.") && strings.HasSuffix(f, ".rd.bytes"):
				vm.RdBytes += toU64(p.Value.I)
			case strings.HasPrefix(f, "block.") && strings.HasSuffix(f, ".wr.bytes"):
				vm.WrBytes += toU64(p.Value.I)
			case strings.HasPrefix(f, "net.") && strings.HasSuffix(f, ".rx.bytes"):
				vm.RxBytes += toU64(p.Value.I)
			case strings.HasPrefix(f, "net.") && strings.HasSuffix(f, ".tx.bytes"):
				vm.TxBytes += toU64(p.Value.I)
			}
		}
	}
	return vm
}

func toU64(v any) uint64 {
	switch n := v.(type) {
	case int32:
		return uint64(n)
	case uint32:
		return uint64(n)
	case int64:
		return uint64(n)
	case uint64:
		return n
	case float64:
		return uint64(n)
	}
	return 0
}

// Rates are per-second deltas between two snapshots. CPU is % of the VM's
// vCPUs (100 = all vCPUs saturated). MemKiB is a level, not a rate: memory in
// use right now.
type Rates struct {
	CPU, Rd, Wr, Rx, Tx float64
	MemKiB, MemTotalKiB float64
	GuestMem            bool // MemKiB came from the guest, not host RSS
}

func ComputeRates(prev, cur VM, dt time.Duration) Rates {
	secs := dt.Seconds()
	if secs <= 0 {
		return Rates{}
	}
	// A counter going backwards means the VM restarted; report 0 for this tick.
	per := func(a, b uint64) float64 {
		if b < a {
			return 0
		}
		return float64(b-a) / secs
	}
	r := Rates{Rd: per(prev.RdBytes, cur.RdBytes), Wr: per(prev.WrBytes, cur.WrBytes),
		Rx: per(prev.RxBytes, cur.RxBytes), Tx: per(prev.TxBytes, cur.TxBytes)}
	if cur.VCPUs > 0 {
		r.CPU = per(prev.CPUTime, cur.CPUTime) / 1e9 / float64(cur.VCPUs) * 100
	}
	// Guest balloon stats only refresh when a stats period is set on the
	// domain; otherwise last-update freezes and the numbers are stale, so
	// fall back to qemu's RSS on the host (capped: it includes qemu overhead).
	if cur.MemStamp != prev.MemStamp && cur.AvailKiB >= cur.UsableKiB && cur.AvailKiB > 0 {
		// available = guest MemTotal, usable = MemAvailable: used is what
		// free/btop call used, and the base is what the guest actually sees.
		r.MemKiB, r.MemTotalKiB, r.GuestMem = float64(cur.AvailKiB-cur.UsableKiB), float64(cur.AvailKiB), true
	} else {
		r.MemKiB, r.MemTotalKiB = float64(min(cur.RSSKiB, cur.MaxMemKiB)), float64(cur.MaxMemKiB)
	}
	return r
}

func HumanBytes(b float64) string {
	if b < 1024 {
		return fmt.Sprintf("%.0fB", b)
	}
	const units = "KMGTPE"
	i := -1
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	return fmt.Sprintf("%.1f%c", b, units[i])
}
