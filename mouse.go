package main

import (
	"github.com/charmbracelet/x/ansi"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// listTop is the first list line: under the tabs, a blank, the filter and
// another blank.
const listTop = 4

// renderFooter draws the keys along the bottom, the one under the pointer
// lit so it reads as clickable.
func (m model) renderFooter(hs []hint) string {
	hot := ""
	if m.mouseY == m.height-1 {
		hot = hintAt(hs, m.mouseX, m.width)
	}
	return footerLine(hs, hot, m.width)
}

// handleMouse: hovering highlights, one click opens, the wheel scrolls. Footer
// hints and the tabs are buttons.
func (m model) handleMouse(ev tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeSignedOut || m.mode == modeSigningIn || m.mode == modeChooseWorkspace {
		return m, nil
	}
	m.mouseX, m.mouseY = ev.X, ev.Y
	if ev.Action == tea.MouseActionMotion {
		// Hover highlights, like a launcher: the click then opens what you see.
		if m.mode == modeBusy || m.mode == modeLoading {
			return m, nil
		}
		if i, ok := m.rowAt(ev.Y); ok {
			// Within what's shown: the list never scrolls under the pointer.
			m.cursor, m.chose = i, true
		} else if i, ok := m.stateAt(ev.Y); ok {
			m.stateCursor = i
		}
		return m, nil
	}
	if ev.Action != tea.MouseActionPress {
		return m, nil
	}
	switch ev.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		delta := 1
		if ev.Button == tea.MouseButtonWheelUp {
			delta = -1
		}
		switch m.screen {
		case screenList:
			m.move(delta)
		case screenIssue, screenProject:
			m.scrollBody(delta)
		case screenStatus:
			if n := len(m.states[m.cur.Team.ID]); n > 0 {
				m.stateCursor = min(max(0, m.stateCursor+delta), n-1)
			}
		}
		return m, nil
	case tea.MouseButtonLeft:
	default:
		return m, nil
	}
	if m.mode == modeBusy {
		return m, nil
	}
	if ev.Y == 0 {
		return m.clickTab(ev.X)
	}
	if m.mode == modeLoading {
		return m, nil
	}

	if ev.Y == m.height-1 {
		hs := m.footer()
		if m.screen != screenList {
			hs = m.detailFooter()
		}
		if k := hintAt(hs, ev.X, m.width); k != "" {
			return m.handleKey(keyMsg(k))
		}
		return m, nil
	}

	if i, ok := m.fileRowAt(ev.Y); ok {
		m.form.setFocus(fieldAttach)
		m.form.fileCursor, m.err = i, ""
		if i == len(m.form.files) {
			return m.attach()
		}
		return m, nil
	}
	if i, ok := m.rowAt(ev.Y); ok {
		m.cursor, m.chose, m.flash = i, true, ""
		return m.activate(false)
	}
	if i, ok := m.stateAt(ev.Y); ok {
		m.stateCursor = i
		return m.handleDetailKey(keyMsg("enter"))
	}
	return m, nil
}

// fileRowAt is the new issue's Files row on screen line y, if any: a file,
// or len(files) for "Add new…".
func (m model) fileRowAt(y int) (int, bool) {
	if m.screen != screenCreate || m.form.picking {
		return 0, false
	}
	lines := strings.Split(ansi.Strip(m.view()), "\n")
	for i, l := range lines {
		if strings.Contains(l, addNew) {
			row := y - (i - len(m.form.files))
			return row, row >= 0 && row <= len(m.form.files)
		}
	}
	return 0, false
}

// stateAt is the status picker entry on screen line y, if any.
func (m model) stateAt(y int) (int, bool) {
	if m.screen != screenStatus {
		return 0, false
	}
	header := m.issueHeader()
	room := m.issueBodyRoom(header)
	start := m.pickerStart(room)
	// tabs, blank, header, then the "Move to" heading.
	top := 2 + strings.Count(header, "\n") + 1
	i := start + y - top
	if y < top || i >= len(m.states[m.cur.Team.ID]) || i-start >= max(1, room-1) {
		return 0, false
	}
	return i, true
}

// clickTab switches to the clicked tab, leaving any detail screen or project
// drill-down: the tabs are the way home.
func (m model) clickTab(x int) (tea.Model, tea.Cmd) {
	want := tab(-1)
	_, cells := m.tabBar()
	for _, c := range cells {
		if x >= c.x0 && x < c.x1 {
			want = c.tab
		}
	}
	if want < 0 {
		return m, nil
	}
	m.screen, m.err, m.flash, m.direct = screenList, "", "", false
	if want != m.tab {
		return m.switchTab(want)
	}
	if m.drilled != nil {
		m.drilled, m.projIss = nil, nil
		m.cursor, m.offset, m.chose = 0, 0, false
		m.clampCursor()
	}
	return m, nil
}
