package store

import "time"

// CuratedValuationRecord is a local-only manual valuation entry.
// It is additive to automated benchmark decisions and only written/read
// through explicit valuation commands.
type CuratedValuationRecord struct {
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	AgentID       string    `json:"agent_id"`
	SessionID     string    `json:"session_id,omitempty"`
	CriteriaMet   int       `json:"criteria_met"`
	CriteriaTotal int       `json:"criteria_total"`
	KillSwitch    bool      `json:"kill_switch"`
	Score         float64   `json:"score"`
	Note          string    `json:"note,omitempty"`
}

// ComputeCuratedValuationScore calculates a deterministic manual valuation score.
// Semantics:
//   - killSwitch=true => 0 regardless of criteria values.
//   - criteriaTotal<=0 => 0 (deterministic behavior for N=0 and invalid negatives).
//   - criteriaMet is clamped to [0, criteriaTotal] before ratio.
func ComputeCuratedValuationScore(criteriaMet, criteriaTotal int, killSwitch bool) float64 {
	if killSwitch {
		return 0
	}
	if criteriaTotal <= 0 {
		return 0
	}
	if criteriaMet < 0 {
		criteriaMet = 0
	}
	if criteriaMet > criteriaTotal {
		criteriaMet = criteriaTotal
	}
	return float64(criteriaMet) / float64(criteriaTotal)
}
