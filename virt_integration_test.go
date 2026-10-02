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

func TestIntegrationHostAndStorage(t *testing.T) {
	c, err := dial("qemu:///system")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	h, err := c.host()
	if err != nil || h.CPUs == 0 || h.MemKiB == 0 || h.AvailKiB == 0 || h.CPUTotal == 0 || h.Name == "" {
		t.Fatalf("host = %+v, %v", h, err)
	}
	pools, nets, err := c.storage()
	if err != nil {
		t.Fatal(err)
	}
	var pool *Pool
	for i := range pools {
		if pools[i].Name == "lazyvirt-test" {
			pool = &pools[i]
		}
	}
	if pool == nil || !pool.Active || pool.Capacity == 0 || pool.Allocation == 0 {
		t.Errorf("lazyvirt-test pool missing or empty: %+v", pools)
	}
	for _, n := range nets {
		if n.Name == "lazyvirt-test-net" {
			if !n.Active || n.Bridge == "" || len(n.Leases) == 0 {
				t.Errorf("lazyvirt-test-net: %+v", n)
			}
			return
		}
	}
	t.Errorf("lazyvirt-test-net missing: %+v", nets)
}

func TestIntegrationSnapshotLifecycle(t *testing.T) {
	c, err := dial("qemu:///system")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	cp := findVM(t, c, "lazyvirt-test-cp").UUID
	name, err := c.snapshotDo(cp, "create", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.snapshotDo(cp, "delete", name) // cleanup if a step below fails
	snaps, err := c.snapshots(cp)
	if err != nil || len(snaps) == 0 || snaps[len(snaps)-1].Name != name || !snaps[len(snaps)-1].Current {
		t.Fatalf("snapshots = %+v, %v; want %s last and current", snaps, err, name)
	}
	if _, err := c.snapshotDo(cp, "revert", name); err != nil {
		t.Fatal(err)
	}
	if _, err := c.snapshotDo(cp, "delete", name); err != nil {
		t.Fatal(err)
	}
	if snaps, _ = c.snapshots(cp); len(snaps) != 0 {
		t.Fatalf("snapshot not deleted: %+v", snaps)
	}
	// Raw disks can't be snapshotted internally: the error must say so.
	w := findVM(t, c, "lazyvirt-test-worker-0").UUID
	if _, err := c.snapshotDo(w, "create", ""); err == nil || !strings.Contains(err.Error(), "raw") {
		t.Fatalf("worker with raw disk: err = %v, want a raw-disk error", err)
	}
}

func TestIntegrationLifecycleEvents(t *testing.T) {
	c, err := dial("qemu:///system")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	ev := c.events()
	const name = "lazyvirt-test-worker-1"
	id := findVM(t, c, name).UUID
	time.Sleep(300 * time.Millisecond) // let the subscription register
	if err := c.act(id, "destroy"); err != nil {
		t.Fatal(err)
	}
	defer c.act(id, "start")
	select {
	case <-ev:
	case <-time.After(5 * time.Second):
		t.Fatal("no lifecycle event after force off")
	}
}
