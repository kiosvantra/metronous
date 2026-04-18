package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTrackingPopupClosedOnEscapeToLanding(t *testing.T) {
	m := NewAppModel(nil, nil, "", "", "", "test")
	m.CurrentTab = TabTracking
	m.showLanding = false

	// Simulate an open popup.
	m.tracking.popupOpen = true
	m.tracking.popupSessionID = "sess"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	app := updated.(*AppModel)

	if !app.showLanding {
		t.Fatalf("expected showLanding=true")
	}
	if app.tracking.popupOpen {
		t.Fatalf("expected tracking popup to be closed when returning to landing")
	}

	// Re-enter tracking.
	updated, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	app = updated.(*AppModel)
	if app.CurrentTab != TabTracking {
		t.Fatalf("expected CurrentTab=TabTracking")
	}
	if app.tracking.popupOpen {
		t.Fatalf("expected tracking popup to remain closed after re-entering")
	}
}

func TestTrackingPopupClosedOnTabSwitch(t *testing.T) {
	m := NewAppModel(nil, nil, "", "", "", "test")
	m.CurrentTab = TabTracking
	m.showLanding = false

	// Simulate an open popup.
	m.tracking.popupOpen = true
	m.tracking.popupSessionID = "sess"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	app := updated.(*AppModel)

	if app.CurrentTab != TabBenchmarkSummary {
		t.Fatalf("expected CurrentTab=TabBenchmarkSummary")
	}
	if app.tracking.popupOpen {
		t.Fatalf("expected tracking popup to be closed on tab switch")
	}
}
