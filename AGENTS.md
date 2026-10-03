# AGENTS.md

Notes for coding agents working on lazyvirt. User-facing docs are in
README.md.

## Commands

```bash
make test               # go vet + unit tests; no libvirt needed. Run before every commit.
make bench              # BenchmarkTickParse, BenchmarkView
make vms-up             # cirros test VMs (lazyvirt-test-*) on qemu:///system
make test-integration   # needs make vms-up
make vms-down
```

Check UI changes in a real terminal, not only with tests: run
`./lazyvirt --prefix lazyvirt-test` inside tmux and read the screen with
`tmux capture-pane -p`. tmux sessions do not inherit your shell's
`LIBVIRT_DEFAULT_URI`, so pass `-c qemu:///system`.

## Layout

```
cmd/lazyvirt/      main: flags, dial, start the UI
internal/virt/     libvirt side: no UI imports
internal/ui/       bubbletea side: no go-libvirt imports
scripts/           test VM setup (test-vms.sh)
docs/              README screenshots
```

| File | Owns |
| --- | --- |
| `virt/virt.go` | `Conn`: connection, re-dial, events, actions, domain XML |
| `virt/stats.go` | `VM`, `State`, `Rates`, parsing `ConnectGetAllDomainStats`, rate math |
| `virt/host.go` | host capacity, pools, networks |
| `virt/snapshot.go` | snapshot RPCs and XML |
| `ui/ui.go` | `New`, the model, update loop, keys, footer, palette |
| `ui/group.go` | grouping, sidebar, the charts (`plotLines`, `renderPlot`) |
| `ui/graph.go` | `panel` box drawing, color helpers |
| `ui/host.go`, `ui/snapshot.go` | host view, snapshot panel |
| `ui/shell.go` | `s` key: ssh to the lease IP, console fallback |

`ui.New` wires a `*virt.Conn`'s methods into the model's function fields
(`fetch`, `act`, `snaps`, ...). Unit tests build the model with `newModel`
and fakes instead, so most UI behavior is tested without libvirt. Follow that
pattern, and keep the import direction one way: `ui` imports `virt`, never
the reverse.

## Rules

- Pure Go, no cgo. libvirt is reached through `digitalocean/go-libvirt` (RPC),
  never through libvirt's C library or by shelling out to `virsh`. The
  exceptions are interactive handoffs (`ssh`, `virsh console`).
- One `ConnectGetAllDomainStats` call per tick covers every VM. Do not add
  per-VM RPCs to the tick path. Cache static data (see the host info cache).
- Never change persistent domain config as a side effect of viewing. Use live
  flags (the balloon stats period is live only).
- Keep dependencies minimal: bubbletea, lipgloss, x/ansi, go-libvirt. Ask
  before adding one.
- Tests that need libvirt go behind the `integration` build tag and only
  touch `lazyvirt-test-*` resources.
- Match the existing style: short files, few comments that explain why, no
  interfaces with one implementation.
- Update README.md when keys, flags or visible behavior change.
