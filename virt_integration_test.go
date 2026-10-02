//go:build integration

// Needs a running libvirt with the VMs from `make vms-up`.
package main

import (
	"strings"
	"testing"
	"time"
)

func findVM(t *testing.T, c *conn, name string) VM {
	t.Helper()
	vms, err := c.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vms {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("%s not found; run `make vms-up`", name)
	return VM{}
}

func waitState(t *testing.T, c *conn, name string, want State) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if findVM(t, c, name).State == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s never reached %s", name, want)
}

func TestIntegrationSnapshotAndDetails(t *testing.T) {
	c, err := dial("qemu:///system")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	cp := findVM(t, c, "lazyvirt-test-cp")
	if cp.State != StateRunning || cp.VCPUs != 1 || cp.MaxMemKiB == 0 {
		t.Errorf("unexpected cp stats: %+v", cp)
	}
	info, err := c.details(findVM(t, c, "lazyvirt-test-worker-0").UUID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lazyvirt-test-net", "vdb", "lazyvirt-test-worker-0-longhorn.raw"} {
		if !strings.Contains(info, want) {
			t.Errorf("details missing %q:\n%s", want, info)
		}
	}
}

func TestIntegrationForceOffAndStart(t *testing.T) {
	c, err := dial("qemu:///system")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	const name = "lazyvirt-test-worker-1"
	id := findVM(t, c, name).UUID
	if err := c.act(id, "destroy"); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, name, StateShutoff)
	if err := c.act(id, "start"); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, name, StateRunning)
}

func TestIntegrationGuestMemStatsGoFresh(t *testing.T) {
	c, err := dial("qemu:///system")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	const name = "lazyvirt-test-cp"
	first := findVM(t, c, name)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if v := findVM(t, c, name); v.MemStamp != first.MemStamp && v.AvailKiB > 0 {
			return
		}
	}
	t.Fatal("balloon.last-update never advanced; stats period not enabled")
}

func TestIntegrationIP(t *testing.T) {
	c, err := dial("qemu:///system")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	ip, err := c.ip(findVM(t, c, "lazyvirt-test-cp").UUID)
	if err != nil || !strings.HasPrefix(ip, "192.168.160.") {
		t.Fatalf("ip = %q, %v; want a 192.168.160.x lease", ip, err)
	}
}
