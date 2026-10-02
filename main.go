package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	uri := flag.String("c", envOr("LIBVIRT_DEFAULT_URI", "qemu:///system"), "libvirt URI, e.g. qemu+ssh://user@host/system")
	prefix := flag.String("prefix", "", "only show domains whose name starts with this")
	interval := flag.Duration("interval", 2*time.Second, "refresh interval")
	sshUser := flag.String("ssh-user", "", "user for the s shell key (default: your ssh config)")
	flag.Parse()

	c, err := dial(*uri)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lazyvirt: connect %s: %v\n", *uri, err)
		os.Exit(1)
	}
	defer c.close()
	c.memPeriod = int32(max(interval.Seconds(), 1)) // guest stats as fresh as our ticks

	m := newModel(c.snapshot, c.act, c.details, *prefix, *interval)
	m.ip, m.uri, m.sshUser = c.ip, *uri, *sshUser
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyvirt:", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
