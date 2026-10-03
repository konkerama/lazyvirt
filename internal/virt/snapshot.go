package virt

import (
	"encoding/xml"
	"fmt"
	"slices"
	"time"

	"github.com/digitalocean/go-libvirt"
)

// Snapshot is one internal libvirt snapshot of a VM.
type Snapshot struct {
	Name, State string // State: the VM's state when taken (running = memory included)
	Created     time.Time
	Current     bool
}

func parseSnapshot(desc string) (Snapshot, error) {
	var x struct {
		Name    string `xml:"name"`
		State   string `xml:"state"`
		Created int64  `xml:"creationTime"`
	}
	if err := xml.Unmarshal([]byte(desc), &x); err != nil {
		return Snapshot{}, fmt.Errorf("parse snapshot XML: %w", err)
	}
	return Snapshot{Name: x.Name, State: x.State, Created: time.Unix(x.Created, 0)}, nil
}

// Snapshots lists a VM's Snapshots, oldest first.
func (c *Conn) Snapshots(uuid string) ([]Snapshot, error) {
	l, d, err := c.lookup(uuid)
	if err != nil {
		return nil, err
	}
	raw, _, err := l.DomainListAllSnapshots(d, 1, 0)
	if err != nil {
		return nil, err
	}
	current := ""
	if cur, err := l.DomainSnapshotCurrent(d, 0); err == nil {
		current = cur.Name
	}
	out := make([]Snapshot, 0, len(raw))
	for _, r := range raw {
		desc, err := l.DomainSnapshotGetXMLDesc(r, 0)
		if err != nil {
			return nil, err
		}
		s, err := parseSnapshot(desc)
		if err != nil {
			return nil, err
		}
		s.Current = s.Name == current
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Snapshot) int { return a.Created.Compare(b.Created) })
	return out, nil
}

// SnapshotDo runs create (name ignored; one is generated and returned),
// revert or delete. Only internal snapshots: the whole VM, memory included
// when running. libvirt refuses them for raw disks ("internal snapshot for
// disk vdb unsupported for storage type raw") and that error is passed on;
// external snapshots leave overlay files behind on revert, too fragile to
// drive from a TUI.
func (c *Conn) SnapshotDo(uuid, op, name string) (string, error) {
	l, d, err := c.lookup(uuid)
	if err != nil {
		return "", err
	}
	if op == "create" {
		name = "lazyvirt-" + time.Now().Format("20060102-150405")
		_, err := l.DomainSnapshotCreateXML(d, "<domainsnapshot><name>"+name+"</name></domainsnapshot>", 0)
		return name, err
	}
	snap := libvirt.DomainSnapshot{Name: name, Dom: d}
	switch op {
	case "revert":
		return name, l.DomainRevertToSnapshot(snap, 0)
	case "delete":
		return name, l.DomainSnapshotDelete(snap, 0)
	}
	return "", fmt.Errorf("unknown snapshot op %q", op)
}
