package virt

import (
	"testing"

	"github.com/digitalocean/go-libvirt"
)

func TestParseHostCPU(t *testing.T) {
	busy, total := parseHostCPU([]libvirt.NodeGetCPUStats{
		{Field: "kernel", Value: 10}, {Field: "user", Value: 30}, {Field: "idle", Value: 50}, {Field: "iowait", Value: 10},
	})
	if busy != 40 || total != 100 {
		t.Errorf("busy %d total %d, want 40 100", busy, total)
	}
}

func TestParseHostMem(t *testing.T) {
	total, avail := parseHostMem([]libvirt.NodeGetMemoryStats{
		{Field: "total", Value: 1000}, {Field: "free", Value: 100}, {Field: "buffers", Value: 50}, {Field: "cached", Value: 250},
	})
	if total != 1000 || avail != 400 {
		t.Errorf("total %d avail %d, want 1000 400", total, avail)
	}
}

func TestHostCPUPct(t *testing.T) {
	prev := Host{CPUBusy: 100, CPUTotal: 1000}
	if got := HostCPUPct(prev, Host{CPUBusy: 150, CPUTotal: 1200}); got != 25 {
		t.Errorf("got %v, want 25", got)
	}
	if got := HostCPUPct(prev, prev); got != 0 {
		t.Errorf("no time passed: got %v, want 0", got)
	}
}

func TestAllocated(t *testing.T) {
	vcpus, mem := Allocated([]VM{
		{State: StateRunning, VCPUs: 2, MaxMemKiB: 1000},
		{State: StateRunning, VCPUs: 4, MaxMemKiB: 3000},
		{State: StateShutoff, VCPUs: 8, MaxMemKiB: 9000}, // stopped: holds nothing
	})
	if vcpus != 6 || mem != 4000 {
		t.Errorf("vcpus %d mem %d, want 6 4000", vcpus, mem)
	}
}
