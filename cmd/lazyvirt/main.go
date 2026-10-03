package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/konkerama/lazyvirt/internal/ui"
	"github.com/konkerama/lazyvirt/internal/virt"
)

func main() {
	uri := flag.String("c", envOr("LIBVIRT_DEFAULT_URI", "qemu:///system"), "libvirt URI, e.g. qemu+ssh://user@host/system")
	prefix := flag.String("prefix", "", "only show domains whose name starts with this")
	interval := flag.Duration("interval", 2*time.Second, "refresh interval")
	sshUser := flag.String("ssh-user", "", "user for the s shell key (default: your ssh config)")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *version {
		// Go stamps the module version from the git tag (or go install @version).
		bi, _ := debug.ReadBuildInfo()
		fmt.Println("lazyvirt", bi.Main.Version)
		return
	}

	c, err := virt.Dial(*uri)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lazyvirt: connect %s: %v\n", *uri, err)
		os.Exit(1)
	}
	defer c.Close()
	c.MemPeriod = int32(max(interval.Seconds(), 1)) // guest stats as fresh as our ticks

	m := ui.New(c, *uri, *prefix, *sshUser, *interval)
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
