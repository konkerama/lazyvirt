package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/konkerama/lazyvirt/internal/virt"
)

type (
	snapsMsg struct {
		uuid string
		list []virt.Snapshot
		err  error
	}
	snapDoneMsg struct {
		op, name string
		err      error
	}
)

func (m model) snapsCmd() tea.Cmd {
	v := m.target()
	if m.snaps == nil || v.UUID == "" {
		return nil
	}
	return func() tea.Msg {
		list, err := m.snaps(v.UUID)
		return snapsMsg{uuid: v.UUID, list: list, err: err}
	}
}

func (m *model) snapDoCmd(op, name string) tea.Cmd {
	v := m.target()
	m.status = fmt.Sprintf("snapshot %s %s…", op, v.Name)
	return func() tea.Msg {
		name, err := m.snapDo(v.UUID, op, name)
		return snapDoneMsg{op: op, name: name, err: err}
	}
}

// snapKey handles keys while the snapshots panel is open. ok=false passes the
// key on (tab, s, u, S, r, F still work).
func (m model) snapKey(s string) (_ model, _ tea.Cmd, ok bool) {
	n := len(m.snapList)
	sel := ""
	if m.snapCur < n {
		sel = m.snapList[m.snapCur].Name
	}
	v := m.target()
	switch s {
	case "j", "down":
		m.snapCur = min(m.snapCur+1, max(n-1, 0))
	case "k", "up":
		m.snapCur = max(m.snapCur-1, 0)
	case "g", "home":
		m.snapCur = 0
	case "G", "end":
		m.snapCur = max(n-1, 0)
	case "p", "esc":
		m.snapOpen = false
	case "n":
		return m, m.snapDoCmd("create", ""), true
	case "enter", "d":
		if sel == "" {
			return m, nil, true
		}
		if s == "enter" {
			m.confirm = pending{action: "snap-revert", snap: sel, prompt: fmt.Sprintf("revert %s to %s? (y/n)", v.Name, sel)}
		} else {
			m.confirm = pending{action: "snap-delete", snap: sel, prompt: fmt.Sprintf("delete snapshot %s of %s? (y/n)", sel, v.Name)}
		}
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m model) snapPanel(w, h int) string {
	var lines []string
	for i, s := range m.snapList {
		mark := dim.Render("disk only")
		if s.State == "running" {
			mark = fg(colRunning).Render("with memory")
		}
		cur := ""
		if s.Current {
			cur = fg(colAccent).Render("  current")
		}
		row := fmt.Sprintf(" %-28s %s  %s%s", trunc(s.Name, 28), s.Created.Format("2006-01-02 15:04"), mark, cur)
		if i == m.snapCur {
			row = selStyle.Render(ansi.Strip(row) + strings.Repeat(" ", max(w-2-lipgloss.Width(row), 0)))
		}
		lines = append(lines, row)
	}
	if len(lines) == 0 {
		lines = []string{dim.Render(" no snapshots · n to take one")}
	}
	lines = lines[scrollStart(len(lines), m.snapCur, h-2):]
	return panel("snapshots", dim.Render(fmt.Sprintf("%d · %s", len(m.snapList), m.target().Name)), lines, w, h, colAccent)
}
