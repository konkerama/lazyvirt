package main

import (
	"strings"
	"testing"

	"github.com/digitalocean/go-libvirt"
)

const workerXML = `<domain type='kvm'>
  <name>talos-local-worker-0</name>
  <memory unit='KiB'>6291456</memory>
  <vcpu placement='static'>4</vcpu>
  <os><type arch='x86_64' machine='pc-q35-9.0'>hvm</type></os>
  <devices>
    <disk type='volume' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source pool='talos-local' volume='talos-local-worker-0-boot.qcow2'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <disk type='file' device='disk'>
      <driver name='qemu' type='raw'/>
      <source file='/var/lib/libvirt/images/talos-local/talos-local-worker-0-longhorn.raw'/>
      <target dev='vdb' bus='virtio'/>
    </disk>
    <interface type='network'>
      <mac address='52:54:00:6c:61:0b'/>
      <source network='talos-local-net' portid='x' bridge='virbr2'/>
      <model type='virtio'/>
    </interface>
  </devices>
</domain>`

func TestFormatInfo(t *testing.T) {
	ifaces := []libvirt.DomainInterface{{
		Name:   "vnet3",
		Hwaddr: libvirt.OptString{"52:54:00:6c:61:0b"},
		Addrs:  []libvirt.DomainIPAddr{{Addr: "192.168.150.11", Prefix: 24}},
	}}
	got, err := formatInfo(workerXML, true, ifaces)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"vCPUs      4",
		"Memory     6.0G",
		"Machine    pc-q35-9.0 (x86_64)",
		"Autostart  yes",
		"vda  virtio  qcow2  talos-local/talos-local-worker-0-boot.qcow2",
		"vdb  virtio  raw    talos-local-worker-0-longhorn.raw",
		"52:54:00:6c:61:0b  192.168.150.11/24  network talos-local-net  virtio",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The IP matters most and the sidebar panel is short: network before disks.
	if strings.Index(got, "Network") > strings.Index(got, "Disks") {
		t.Errorf("Network should come before Disks:\n%s", got)
	}
}

func TestFormatInfoBadXML(t *testing.T) {
	if _, err := formatInfo("<nope", false, nil); err == nil {
		t.Fatal("want error for bad XML")
	}
}

func TestParseUUIDRoundTrip(t *testing.T) {
	u := libvirt.UUID{0xde, 0xad, 0xbe, 0xef, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	got, err := parseUUID(uuidString(u))
	if err != nil || got != u {
		t.Fatalf("parseUUID(%s) = %x, %v", uuidString(u), got, err)
	}
	if _, err := parseUUID("zz"); err == nil {
		t.Fatal("want error for bad uuid")
	}
}

func TestParseSnapshotXML(t *testing.T) {
	got, err := parseSnapshot(`<domainsnapshot><name>pre-upgrade</name><state>running</state>
<creationTime>1790967244</creationTime><memory snapshot='internal'/></domainsnapshot>`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "pre-upgrade" || got.State != "running" || got.Created.Unix() != 1790967244 {
		t.Errorf("got %+v", got)
	}
}
