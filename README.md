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

One screen. The sidebar has four fixed panels: **groups**, the selected
group's **vms** (chart color, state, CPU), **info** for the selected VM
(network with lease IP first, then disks) and **host** (host CPU and memory,
plus vCPUs and RAM claimed by running VMs against what the host has). `tab`
cycles focus groups → vms → host, and the right side follows it on the same
grid (row 1 cpu | mem, rows 2-3 below):

- groups focused: one colored line per VM of the group, overlaid on the same
  axes (cpu, mem %, disk read/write, net rx/tx); the selected VM's line is
  drawn on top.
- vms focused: the selected VM alone, with richer titles (vCPUs, used / total
  memory, current and peak rates). `p` swaps rows 2-3 for its snapshots.
- host focused: host cpu and mem history, storage pools with fill bars, and
  networks with their DHCP leases.

Groups are `all` plus one per name prefix with 2+ VMs: a trailing number and
then the last segment are dropped, so `talos-local-cp`, `talos-local-worker-0`
and `talos-local-worker-1` form `talos-local`.

| Key | Action |
| --- | --- |
| `j`/`k`, arrows, `g`/`G` | move in the focused panel |
| `tab` / `shift+tab`, `h`/`l` | next / previous panel (`enter` vms, `esc` groups) |
| `s` | shell into the VM (see below) |
| `u` / `S` / `r` | start / ACPI shutdown / reboot |
| `F` | force off |
| `p` | snapshots of the VM: `n` new, `enter` revert, `d` delete, `p` close |
| `R` | refresh now |
| `q` | quit |

With the vms panel focused, `u`/`S`/`r`/`F` act on the selected VM (`F` asks
first). With the groups panel focused they act on the whole group: only the
VMs the action applies to (start skips running ones), always after a y/n with
the count. Errors from an action stay in the footer until the next key.

Snapshots are libvirt internal snapshots: the whole VM, memory included when it
is running. libvirt refuses them for VMs with a raw disk (Talos workers'
Longhorn disks), and lazyvirt shows that error rather than falling back to
external snapshots, whose revert leaves overlay files behind.

State changes (start, stop, crash) arrive as libvirt lifecycle events and
refresh the screen at once; the interval only paces the stats.

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
