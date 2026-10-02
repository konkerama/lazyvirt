package main

import (
	"slices"
	"testing"
)

func TestSSHJump(t *testing.T) {
	cases := map[string]string{
		"qemu:///system":                   "",
		"qemu+ssh://kostas@homelab/system": "kostas@homelab",
		"qemu+ssh://homelab:2222/system":   "homelab:2222",
		"qemu+tcp://homelab/system":        "",
		"qemu+libssh2://k@homelab/system":  "k@homelab",
	}
	for uri, want := range cases {
		if got := sshJump(uri); got != want {
			t.Errorf("sshJump(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestShellArgv(t *testing.T) {
	cases := []struct {
		name, uri, ip, user string
		want                []string
	}{
		{"no IP: console", "qemu:///system", "", "", []string{"virsh", "-c", "qemu:///system", "console", "vm"}},
		{"local ssh", "qemu:///system", "10.0.0.5", "", []string{"ssh", "-o", "ConnectTimeout=3", "10.0.0.5"}},
		{"local ssh with user", "qemu:///system", "10.0.0.5", "cirros", []string{"ssh", "-o", "ConnectTimeout=3", "cirros@10.0.0.5"}},
		{"remote ssh jumps via host", "qemu+ssh://k@homelab/system", "192.168.150.11", "",
			[]string{"ssh", "-o", "ConnectTimeout=3", "-J", "k@homelab", "192.168.150.11"}},
	}
	for _, c := range cases {
		if got := shellArgv(c.uri, "vm", c.ip, c.user); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
