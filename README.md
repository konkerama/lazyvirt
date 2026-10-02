# lazyvirt

lazydocker-style TUI for KVM/libvirt VMs. Pure Go (no cgo); one
`ConnectGetAllDomainStats` RPC per refresh covers every VM.

```bash
make build
./lazyvirt                                   # qemu:///system (or $LIBVIRT_DEFAULT_URI)
./lazyvirt --prefix talos-local              # homelab Talos nodes only
./lazyvirt -c qemu+ssh://user@host/system    # remote host
```

Remote `qemu+ssh` uses Go's ssh client: keys from `~/.ssh/id_*` or the agent,
host keys from `~/.ssh/known_hosts`. `~/.ssh/config` aliases are not read, so
give the real hostname and user.

One screen. The sidebar has three fixed panels: **groups**, the selected
group's **vms** (chart color, state, CPU) and **info** for the selected VM
(network with lease IP first, then disks). `tab` switches focus between groups
and vms, and the charts follow it in the same 6-panel layout (cpu, mem %, disk
read/write, net rx/tx):

- groups focused: one colored line per VM of the group, overlaid on the same
  axes to compare them; the selected VM's line is drawn on top.
- vms focused: the selected VM alone, with richer titles (vCPUs, used / total
  memory, current and peak rates).

Groups are `all` plus one per name prefix with 2+ VMs: a trailing number and
then the last segment are dropped, so `talos-local-cp`, `talos-local-worker-0`
and `talos-local-worker-1` form `talos-local`.

| Key | Action |
| --- | --- |
| `j`/`k`, arrows, `g`/`G` | move in the focused panel |
| `tab`, `h`/`l`, `enter`/`esc` | switch focus between groups and vms |
| `s` | shell into the VM (see below) |
| `u` / `S` / `r` | start / ACPI shutdown / reboot |
| `F` | force off (asks y/n) |
| `R` | refresh now |
| `q` | quit |

VM keys (`s`, `u`, `S`, `r`, `F`) need the vms panel focused.

Memory is what the guest itself reports as used (MemTotal minus MemAvailable,
like `free`/btop). For that, lazyvirt sets a balloon stats period on each
running VM, live only: the saved domain config is untouched and the setting is
gone when the VM stops. VMs without a virtio balloon driver fall back to qemu's
RSS on the host, labelled `host rss`.

`s` opens a shell on the selected VM. It runs `ssh <lease IP>` (`--ssh-user` sets the user, otherwise your ssh config
decides). For a `qemu+ssh://` URI the VM's network lives on the remote host,
so ssh jumps through it (`-J user@host`). If ssh cannot connect (exit 255:
refused, timeout, unknown host key), lazyvirt opens the serial console instead
(`virsh console`, Ctrl+] returns to lazyvirt). Talos nodes run no sshd, so for
them `s` always lands on the console.

IPs in the info panel come from libvirt's DHCP leases, so they need no guest
agent but only show for VMs on a libvirt NAT network.

## Development

```bash
make test               # unit tests, no libvirt needed
make vms-up             # tiny cirros VMs named lazyvirt-test-*
make test-integration
make run ARGS='--prefix lazyvirt-test'
make vms-down
```
