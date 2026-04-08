package tui

import (
	"testing"

	"github.com/kiosvantra/metronous/internal/config"
)

func TestSeverityStylesForCostAndDurationUsesTrackingUISeverityConfig(t *testing.T) {
	thresholds := config.DefaultThresholdValues()
	duration := 9000

	_, got := severityStylesForCostAndDuration(thresholds.Defaults, thresholds.TrackingDurationSeverity, thresholds.UrgentTriggers.MaxCostSpikeMultiplier, nil, &duration)
	if got.Render("x") != sevGreenStyle.Render("x") {
		t.Fatalf("expected green style under default tracking UI severity config")
	}

	thresholds.TrackingDurationSeverity = config.TrackingDurationSeverityConfig{GoodMaxMs: 6000, WarnMaxMs: 12000}
	_, got = severityStylesForCostAndDuration(thresholds.Defaults, thresholds.TrackingDurationSeverity, thresholds.UrgentTriggers.MaxCostSpikeMultiplier, nil, &duration)
	if got.Render("x") != sevAmberStyle.Render("x") {
		t.Fatalf("expected amber style when duration exceeds configured tracking UI band")
	}
}
