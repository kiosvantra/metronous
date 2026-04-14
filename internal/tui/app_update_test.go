package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAppBlockedSelfUpdateShowsGuidance(t *testing.T) {
	m := NewAppModel(nil, nil, "", "", "", "test")
	m.SelfUpdateAllowed = false
	m.SelfUpdateHint = "installed in /usr/local/bin without write permission"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	if cmd == nil {
		t.Fatal("expected update guidance command")
	}
	msg := cmd()
	ud, ok := msg.(updateDoneMsg)
	if !ok {
		t.Fatalf("expected updateDoneMsg, got %T", msg)
	}
	if !ud.isError {
		t.Fatal("expected blocked self-update to be reported as error guidance")
	}
	if !strings.Contains(ud.msg, "/usr/local/bin") {
		t.Fatalf("expected install path guidance, got %q", ud.msg)
	}

	app := updated.(*AppModel)
	updated, _ = app.Update(ud)
	app = updated.(*AppModel)
	if !strings.Contains(app.StatusMsg, "Update unavailable here") {
		t.Fatalf("expected status guidance after processing updateDoneMsg, got %q", app.StatusMsg)
	}
}

func TestAppViewHidesInlineUpdateShortcutWhenSelfUpdateBlocked(t *testing.T) {
	m := NewAppModel(nil, nil, "", "", "", "0.36.4")
	m.UpdateAvailable = true
	m.LatestVersion = "0.36.5"
	m.SelfUpdateAllowed = false
	m.SelfUpdateHint = "installed in /usr/local/bin without write permission"
	m.showLanding = false
	m.CurrentTab = TabBenchmarkSummary
	m.Width = 120
	m.Height = 40

	view := m.View()
	if strings.Contains(view, "Press 'u' to update") {
		t.Fatalf("expected blocked install to hide inline update action, got %q", view)
	}
	if !strings.Contains(view, "In-place update disabled for this install") {
		t.Fatalf("expected blocked install banner guidance, got %q", view)
	}
	if strings.Contains(view, "u: update") {
		t.Fatalf("expected blocked install status bar to omit update shortcut, got %q", view)
	}
}
