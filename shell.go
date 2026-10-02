package main

import (
	"errors"
	"net/url"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type (
	shellMsg struct {
		name string
		argv []string
		ssh  bool
	}
	shellDoneMsg struct {
		name string
		ssh  bool
		err  error
	}
)

// sshJump returns the [user@]host[:port] of an ssh libvirt URI: the VM's NAT
// network lives on that host, so ssh has to hop through it. "" when local.
func sshJump(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || !strings.Contains(u.Scheme, "ssh") {
		return ""
	}
	if u.User != nil {
		return u.User.Username() + "@" + u.Host
	}
	return u.Host
}

func consoleArgv(uri, name string) []string {
	return []string{"virsh", "-c", uri, "console", name}
}

// shellArgv picks ssh when the VM has an IP, else its serial console.
func shellArgv(uri, name, ip, user string) []string {
	if ip == "" {
		return consoleArgv(uri, name)
	}
	argv := []string{"ssh", "-o", "ConnectTimeout=3"}
	if j := sshJump(uri); j != "" {
		argv = append(argv, "-J", j)
	}
	if user != "" {
		ip = user + "@" + ip
	}
	return append(argv, ip)
}

// shellCmd resolves the target VM's IP off the UI goroutine, then hands the
// command back as a shellMsg for Update to exec.
func (m *model) shellCmd() tea.Cmd {
	v := m.target()
	if v.UUID == "" {
		return nil
	}
	if v.State != StateRunning {
		m.status = v.Name + " is " + v.State.String()
		return nil
	}
	return func() tea.Msg {
		ip, _ := m.ip(v.UUID) // no lease: fall through to the console
		argv := shellArgv(m.uri, v.Name, ip, m.sshUser)
		return shellMsg{name: v.Name, argv: argv, ssh: argv[0] == "ssh"}
	}
}

// execShell suspends the TUI and runs argv on the terminal.
func execShell(name string, argv []string, ssh bool) tea.Cmd {
	c := exec.Command(argv[0], argv[1:]...)
	if !ssh {
		// A serial console prints nothing until it gets input; say how to use it.
		c = exec.Command("sh", append([]string{"-c",
			`printf '\033[2mserial console of %s: press Enter for a prompt, Ctrl+] to leave\033[0m\n' "$0"; exec "$@"`,
			name}, argv...)...)
	}
	return tea.ExecProcess(c, func(err error) tea.Msg { return shellDoneMsg{name: name, ssh: ssh, err: err} })
}

// shellDone falls back to the serial console when ssh could not connect
// (exit 255: refused, timeout, no route; Talos never runs sshd).
func (m model) shellDone(msg shellDoneMsg) (model, tea.Cmd) {
	var exit *exec.ExitError
	if msg.ssh && errors.As(msg.err, &exit) && exit.ExitCode() == 255 {
		m.status = "ssh unavailable, opened serial console of " + msg.name
		return m, execShell(msg.name, consoleArgv(m.uri, msg.name), false)
	}
	if msg.err != nil && !errors.As(msg.err, &exit) {
		m.actErr = msg.err // could not even start ssh/virsh
	}
	return m, nil
}
