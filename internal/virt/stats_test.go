package virt

import (
	"testing"
	"time"

	"github.com/digitalocean/go-libvirt"
)

func p(field string, v any) libvirt.TypedParam {
	return libvirt.TypedParam{Field: field, Value: libvirt.TypedParamValue{I: v}}
}

func TestParseRecord(t *testing.T) {
	rec := libvirt.DomainStatsRecord{
		Dom: libvirt.Domain{Name: "talos-local-cp", UUID: libvirt.UUID{0xab, 0xcd}},
		Params: []libvirt.TypedParam{
			p("state.state", int32(1)),
			p("cpu.time", uint64(5_000_000_000)),
			p("vcpu.current", uint32(2)),
			p("balloon.current", uint64(4194304)),
			p("balloon.maximum", uint64(4194304)),
			p("balloon.rss", uint64(1048576)),
			p("balloon.available", uint64(4000000)),
			p("balloon.usable", uint64(3000000)),
			p("balloon.last-update", uint64(1790967244)),
			p("block.count", uint32(2)),
			p("block.0.name", "vda"),
			p("block.0.rd.bytes", uint64(100)),
			p("block.0.wr.bytes", uint64(10)),
			p("block.1.rd.bytes", uint64(200)),
			p("block.1.wr.bytes", uint64(20)),
			p("net.0.rx.bytes", uint64(7)),
			p("net.0.tx.bytes", uint64(3)),
			p("net.1.rx.bytes", uint64(1)),
		},
	}
	got := parseRecord(rec)
	want := VM{
		UUID: "abcd0000-0000-0000-0000-000000000000", Name: "talos-local-cp",
		State: StateRunning, VCPUs: 2, CPUTime: 5_000_000_000,
		MemKiB: 4194304, MaxMemKiB: 4194304, RSSKiB: 1048576,
		AvailKiB: 4000000, UsableKiB: 3000000, MemStamp: 1790967244,
		RdBytes: 300, WrBytes: 30, RxBytes: 8, TxBytes: 3,
	}
	if got != want {
		t.Fatalf("parseRecord:\n got %+v\nwant %+v", got, want)
	}
}

func TestStateString(t *testing.T) {
	cases := map[State]string{StateRunning: "running", StateShutoff: "shutoff", StatePaused: "paused", State(99): "unknown"}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestComputeRates(t *testing.T) {
	prev := VM{VCPUs: 2, CPUTime: 1_000_000_000, RdBytes: 1000, WrBytes: 0, RxBytes: 0, TxBytes: 500}
	cur := VM{VCPUs: 2, CPUTime: 2_000_000_000, RdBytes: 3000, WrBytes: 4000, RxBytes: 2000, TxBytes: 500}
	got := ComputeRates(prev, cur, 2*time.Second)
	// 1s of CPU over 2s wall on 2 vCPUs = 25%.
	want := Rates{CPU: 25, Rd: 1000, Wr: 2000, Rx: 1000, Tx: 0}
	if got != want {
		t.Fatalf("computeRates = %+v, want %+v", got, want)
	}
}

func TestComputeRatesGuards(t *testing.T) {
	prev := VM{VCPUs: 1, CPUTime: 9e9, RdBytes: 9000}
	cases := map[string]struct {
		cur VM
		dt  time.Duration
	}{
		"zero dt":           {VM{VCPUs: 1, CPUTime: 10e9, RdBytes: 10000}, 0},
		"counters reset":    {VM{VCPUs: 1, CPUTime: 1e9, RdBytes: 10}, time.Second},
		"zero vcpus no cpu": {VM{VCPUs: 0, CPUTime: 10e9, RdBytes: 9000}, time.Second},
	}
	for name, c := range cases {
		if got := ComputeRates(prev, c.cur, c.dt); got != (Rates{}) {
			t.Errorf("%s: got %+v, want zero", name, got)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[float64]string{0: "0B", 512: "512B", 1536: "1.5K", 10 * 1 << 20: "10.0M", 3 << 30: "3.0G", 2 << 40: "2.0T"}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("humanBytes(%v) = %q, want %q", in, got, want)
		}
	}
}

// BenchmarkTickParse is the per-tick CPU cost of turning one stats RPC
// reply for 100 domains into VMs.
func BenchmarkTickParse(b *testing.B) {
	rec := libvirt.DomainStatsRecord{Dom: libvirt.Domain{Name: "vm"}, Params: []libvirt.TypedParam{
		p("state.state", int32(1)), p("cpu.time", uint64(1)), p("vcpu.current", uint32(4)),
		p("balloon.current", uint64(1)), p("balloon.maximum", uint64(1)), p("balloon.rss", uint64(1)),
		p("block.0.rd.bytes", uint64(1)), p("block.0.wr.bytes", uint64(1)),
		p("block.1.rd.bytes", uint64(1)), p("block.1.wr.bytes", uint64(1)),
		p("net.0.rx.bytes", uint64(1)), p("net.0.tx.bytes", uint64(1)),
	}}
	recs := make([]libvirt.DomainStatsRecord, 100)
	for i := range recs {
		recs[i] = rec
	}
	for b.Loop() {
		for _, r := range recs {
			_ = parseRecord(r)
		}
	}
}

func TestMemUsedPrefersFreshGuestStats(t *testing.T) {
	prev := VM{MaxMemKiB: 4000, RSSKiB: 3500, AvailKiB: 4000, UsableKiB: 3000, MemStamp: 10}
	cur := prev
	cur.MemStamp = 15 // guest pushed new stats
	if got := ComputeRates(prev, cur, time.Second).MemKiB; got != 1000 {
		t.Errorf("fresh guest stats: used %v, want 1000 (available-usable)", got)
	}
	if got := ComputeRates(prev, prev, time.Second).MemKiB; got != 3500 {
		t.Errorf("stale guest stats: used %v, want host RSS 3500", got)
	}
	big := prev
	big.RSSKiB = 9000 // qemu overhead can push RSS past guest max
	if got := ComputeRates(big, big, time.Second).MemKiB; got != 4000 {
		t.Errorf("RSS over max: used %v, want capped 4000", got)
	}
}

func TestMemTotalMatchesSource(t *testing.T) {
	prev := VM{MaxMemKiB: 131072, RSSKiB: 170000, AvailKiB: 73364, UsableKiB: 48876, MemStamp: 1}
	cur := prev
	cur.MemStamp = 2
	r := ComputeRates(prev, cur, time.Second)
	// Guest view: total is what the guest sees (MemTotal), not the domain max.
	if r.MemTotalKiB != 73364 || r.MemKiB != 73364-48876 {
		t.Errorf("guest: used %v of %v, want %v of 73364", r.MemKiB, r.MemTotalKiB, 73364-48876)
	}
	if r = ComputeRates(prev, prev, time.Second); r.MemTotalKiB != 131072 {
		t.Errorf("host fallback: total %v, want max 131072", r.MemTotalKiB)
	}
}
