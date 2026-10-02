package main

import (
	"cmp"
	"context"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/digitalocean/go-libvirt"
)

// conn wraps one libvirt connection and re-dials after it drops
// (libvirtd restart, ssh blip).
type conn struct {
	uri *url.URL
	mu  sync.Mutex
	l   *libvirt.Libvirt

	memPeriod            int32 // balloon stats period (s) set on running VMs
	hostInfo             Host  // static node info, read once
	cpuParams, memParams int32
	memStats             map[string]int32 // UUID -> domain ID (new per boot) already asked
}

func dial(raw string) (*conn, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("bad URI %q: %w", raw, err)
	}
	c := &conn{uri: u, memPeriod: 2, memStats: map[string]int32{}}
	_, err = c.get()
	return c, err
}

func (c *conn) get() (*libvirt.Libvirt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.l != nil && c.l.IsConnected() {
		return c.l, nil
	}
	l, err := libvirt.ConnectToURI(c.uri)
	if err != nil {
		return nil, err
	}
	c.l = l
	return l, nil
}

// events delivers a signal whenever any domain changes lifecycle state
// (started, stopped, paused...), so the UI refreshes at once instead of on the
// next tick. Bursts coalesce: the channel holds one pending signal. The
// subscription is re-made after the connection drops.
func (c *conn) events() <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		for {
			l, err := c.get()
			var ch <-chan libvirt.DomainEventLifecycleMsg
			if err == nil {
				ch, err = l.LifecycleEvents(context.Background())
			}
			if err != nil {
				time.Sleep(2 * time.Second) // libvirtd down; the tick shows the error
				continue
			}
			for range ch {
				select {
				case out <- struct{}{}:
				default: // one already pending
				}
			}
		}
	}()
	return out
}

func (c *conn) close() {
	if c.l != nil {
		_ = c.l.Disconnect()
	}
}

const statsWanted = libvirt.DomainStatsState | libvirt.DomainStatsCPUTotal | libvirt.DomainStatsBalloon |
	libvirt.DomainStatsVCPU | libvirt.DomainStatsInterface | libvirt.DomainStatsBlock

// snapshot fetches every domain's stats in a single RPC.
func (c *conn) snapshot() ([]VM, error) {
	l, err := c.get()
	if err != nil {
		return nil, err
	}
	recs, err := l.ConnectGetAllDomainStats(nil, uint32(statsWanted), 0)
	if err != nil {
		return nil, err
	}
	vms := make([]VM, len(recs))
	for i, r := range recs {
		vms[i] = parseRecord(r)
		c.enableMemStats(l, r.Dom, vms[i])
	}
	slices.SortFunc(vms, func(a, b VM) int { return strings.Compare(a.Name, b.Name) })
	return vms, nil
}

// enableMemStats turns on guest balloon stats for a running VM once per boot.
// Without a stats period libvirt reports guest memory from whenever it last
// polled (often never), so "used" is unknowable. DomainMemLive touches only
// the running qemu, never the saved config, and is gone after the VM stops;
// the domain ID changes every boot, which is how a restart gets asked again.
// Fails harmlessly for VMs without a virtio balloon; tried once either way.
func (c *conn) enableMemStats(l *libvirt.Libvirt, d libvirt.Domain, v VM) {
	if v.State != StateRunning {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.memStats[v.UUID]; ok && id == d.ID {
		return
	}
	c.memStats[v.UUID] = d.ID
	_ = l.DomainSetMemoryStatsPeriod(d, c.memPeriod, libvirt.DomainMemLive)
}

func (c *conn) lookup(uuid string) (*libvirt.Libvirt, libvirt.Domain, error) {
	l, err := c.get()
	if err != nil {
		return nil, libvirt.Domain{}, err
	}
	u, err := parseUUID(uuid)
	if err != nil {
		return nil, libvirt.Domain{}, err
	}
	d, err := l.DomainLookupByUUID(u)
	return l, d, err
}

func parseUUID(s string) (libvirt.UUID, error) {
	var u libvirt.UUID
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != len(u) {
		return u, fmt.Errorf("bad uuid %q", s)
	}
	copy(u[:], b)
	return u, nil
}

func (c *conn) act(uuid, action string) error {
	l, d, err := c.lookup(uuid)
	if err != nil {
		return err
	}
	switch action {
	case "start":
		return l.DomainCreate(d)
	case "shutdown":
		return l.DomainShutdown(d)
	case "reboot":
		return l.DomainReboot(d, 0)
	case "destroy":
		return l.DomainDestroy(d)
	}
	return fmt.Errorf("unknown action %q", action)
}

func (c *conn) details(uuid string) (string, error) {
	l, d, err := c.lookup(uuid)
	if err != nil {
		return "", err
	}
	desc, err := l.DomainGetXMLDesc(d, 0)
	if err != nil {
		return "", err
	}
	auto, _ := l.DomainGetAutostart(d)
	// DHCP leases from libvirt's own dnsmasq: works without a guest agent
	// (Talos nodes have none). Fails harmlessly for shut-off domains.
	ifaces, _ := l.DomainInterfaceAddresses(d, uint32(libvirt.DomainInterfaceAddressesSrcLease), 0)
	return formatInfo(desc, auto == 1, ifaces)
}

// ip returns the VM's first IPv4 DHCP lease, "" if it has none.
func (c *conn) ip(uuid string) (string, error) {
	l, d, err := c.lookup(uuid)
	if err != nil {
		return "", err
	}
	ifaces, err := l.DomainInterfaceAddresses(d, uint32(libvirt.DomainInterfaceAddressesSrcLease), 0)
	if err != nil {
		return "", err
	}
	for _, i := range ifaces {
		for _, a := range i.Addrs {
			if a.Type == int32(libvirt.IPAddrTypeIpv4) {
				return a.Addr, nil
			}
		}
	}
	return "", nil
}

type domainXML struct {
	Memory uint64 `xml:"memory"` // KiB: libvirt normalizes on output
	VCPU   int    `xml:"vcpu"`
	OS     struct {
		Type struct {
			Arch    string `xml:"arch,attr"`
			Machine string `xml:"machine,attr"`
		} `xml:"type"`
	} `xml:"os"`
	Disks []struct {
		Device string `xml:"device,attr"`
		Driver struct {
			Type string `xml:"type,attr"`
		} `xml:"driver"`
		Source struct {
			File   string `xml:"file,attr"`
			Dev    string `xml:"dev,attr"`
			Pool   string `xml:"pool,attr"`
			Volume string `xml:"volume,attr"`
		} `xml:"source"`
		Target struct {
			Dev string `xml:"dev,attr"`
			Bus string `xml:"bus,attr"`
		} `xml:"target"`
	} `xml:"devices>disk"`
	NICs []struct {
		Type string `xml:"type,attr"`
		MAC  struct {
			Address string `xml:"address,attr"`
		} `xml:"mac"`
		Source struct {
			Network string `xml:"network,attr"`
			Bridge  string `xml:"bridge,attr"`
			Dev     string `xml:"dev,attr"`
		} `xml:"source"`
		Model struct {
			Type string `xml:"type,attr"`
		} `xml:"model"`
	} `xml:"devices>interface"`
}

func formatInfo(desc string, autostart bool, ifaces []libvirt.DomainInterface) (string, error) {
	var d domainXML
	if err := xml.Unmarshal([]byte(desc), &d); err != nil {
		return "", fmt.Errorf("parse domain XML: %w", err)
	}
	ips := map[string][]string{}
	for _, i := range ifaces {
		if len(i.Hwaddr) == 0 {
			continue
		}
		for _, a := range i.Addrs {
			ips[i.Hwaddr[0]] = append(ips[i.Hwaddr[0]], fmt.Sprintf("%s/%d", a.Addr, a.Prefix))
		}
	}
	yes := map[bool]string{true: "yes", false: "no"}

	var b strings.Builder
	fmt.Fprintf(&b, "vCPUs      %d\nMemory     %s\n", d.VCPU, humanBytes(float64(d.Memory)*1024))
	fmt.Fprintf(&b, "Machine    %s (%s)\nAutostart  %s\n", d.OS.Type.Machine, d.OS.Type.Arch, yes[autostart])
	// Network first: the IP is what you look for, and the panel is short.
	b.WriteString("\nNetwork\n")
	for _, n := range d.NICs {
		// Live XML of a network NIC also carries its bridge; the network
		// name is the meaningful one.
		src := cmp.Or(n.Source.Network, n.Source.Bridge, n.Source.Dev)
		ip := strings.Join(ips[n.MAC.Address], ", ")
		if ip == "" {
			ip = "-"
		}
		fmt.Fprintf(&b, "  %s  %s  %s %s  %s\n", n.MAC.Address, ip, n.Type, src, n.Model.Type)
	}
	b.WriteString("\nDisks\n")
	for _, k := range d.Disks {
		src := k.Source.Dev
		if k.Source.File != "" {
			src = filepath.Base(k.Source.File) // the directory is the pool's
		}
		if k.Source.Volume != "" {
			src = k.Source.Pool + "/" + k.Source.Volume
		}
		fmt.Fprintf(&b, "  %s  %s  %-5s  %s\n", k.Target.Dev, k.Target.Bus, k.Driver.Type, src)
	}
	return b.String(), nil
}
