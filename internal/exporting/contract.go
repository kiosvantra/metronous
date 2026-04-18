package exporting

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
)

const SchemaVersion = "metronous.export.v1"

// ExportDisabledByDefault reports whether export egress is disabled unless a user explicitly opts in.
func ExportDisabledByDefault() bool {
	return true
}

type Contract struct {
	SchemaVersion        string                 `json:"schema_version"`
	GeneratedAt          string                 `json:"generated_at"`
	AgentFilter          string                 `json:"agent_filter,omitempty"`
	Provenance           ProvenanceContract     `json:"provenance"`
	BenchmarkRuns        []BenchmarkRunContract `json:"benchmark_runs"`
	SemanticPhaseSummary []PhaseSummaryContract `json:"semantic_phase_summary"`
}

type ProvenanceContract struct {
	Contract            string `json:"contract"`
	ConsentMode         string `json:"consent_mode"`
	Egress              string `json:"egress"`
	SanitizationProfile string `json:"sanitization_profile"`
}

type BenchmarkRunContract struct {
	AgentID          string  `json:"agent_id"`
	Verdict          string  `json:"verdict"`
	Model            string  `json:"model"`
	RecommendedModel string  `json:"recommended_model,omitempty"`
	SampleSize       int     `json:"sample_size"`
	Accuracy         float64 `json:"accuracy"`
	P95LatencyMs     float64 `json:"p95_latency_ms"`
	ToolSuccessRate  float64 `json:"tool_success_rate"`
	ROIScore         float64 `json:"roi_score"`
	TotalCostUSD     float64 `json:"total_cost_usd"`
	RunAt            string  `json:"run_at"`
	// Intentionally omitted by default from serialized output as a sanitization rule.
	DecisionReason string `json:"decision_reason,omitempty"`
	ArtifactPath   string `json:"artifact_path,omitempty"`
}

type PhaseSummaryContract struct {
	Phase         string  `json:"phase"`
	Events        int     `json:"events"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
	AvgQuality    float64 `json:"avg_quality"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
}

func BuildContract(now time.Time, runs []store.BenchmarkRun, events []store.Event, agentFilter string) Contract {
	contract := Contract{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		AgentFilter:   strings.TrimSpace(agentFilter),
		Provenance: ProvenanceContract{
			Contract:            "sharing-leaderboard",
			ConsentMode:         "explicit-opt-in",
			Egress:              "local-file-only",
			SanitizationProfile: "sanitized-v1",
		},
		BenchmarkRuns:        make([]BenchmarkRunContract, 0, len(runs)),
		SemanticPhaseSummary: buildPhaseSummary(events),
	}
	if contract.AgentFilter != "" {
		contract.AgentFilter = sanitizeAgentID(contract.AgentFilter)
	}
	for _, r := range runs {
		contract.BenchmarkRuns = append(contract.BenchmarkRuns, BenchmarkRunContract{
			AgentID:          sanitizeAgentID(r.AgentID),
			Verdict:          string(r.Verdict),
			Model:            r.Model,
			RecommendedModel: r.RecommendedModel,
			SampleSize:       r.SampleSize,
			Accuracy:         r.Accuracy,
			P95LatencyMs:     r.P95LatencyMs,
			ToolSuccessRate:  r.ToolSuccessRate,
			ROIScore:         r.ROIScore,
			TotalCostUSD:     r.TotalCostUSD,
			RunAt:            r.RunAt.UTC().Format(time.RFC3339),
		})
	}
	return contract
}

func sanitizeAgentID(agentID string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(agentID)))
	return "anon-" + hex.EncodeToString(h[:])[:12]
}

func sanitizePhase(raw string) string {
	phase := strings.ToLower(strings.TrimSpace(raw))
	switch phase {
	case "propose", "spec", "design", "implement", "verify", "untagged":
		return phase
	case "":
		return "untagged"
	default:
		return "custom"
	}
}

func phaseSortKey(phase string) string {
	switch phase {
	case "propose":
		return "00-propose"
	case "spec":
		return "01-spec"
	case "design":
		return "02-design"
	case "implement":
		return "03-implement"
	case "verify":
		return "04-verify"
	case "custom":
		return "05-custom"
	case "untagged":
		return "06-untagged"
	default:
		return "99-" + phase
	}
}

func buildPhaseSummary(events []store.Event) []PhaseSummaryContract {
	type agg struct {
		events      int
		durationSum float64
		durationN   int
		qualitySum  float64
		qualityN    int
		costSum     float64
	}
	byPhase := make(map[string]*agg)
	for _, ev := range events {
		phase := "untagged"
		if ev.Metadata != nil {
			if raw, ok := ev.Metadata[store.SemanticPhaseMetaKey].(string); ok {
				phase = raw
			}
		}
		phase = sanitizePhase(phase)
		if _, ok := byPhase[phase]; !ok {
			byPhase[phase] = &agg{}
		}
		a := byPhase[phase]
		a.events++
		if ev.DurationMs != nil {
			a.durationSum += float64(*ev.DurationMs)
			a.durationN++
		}
		if ev.QualityScore != nil {
			a.qualitySum += *ev.QualityScore
			a.qualityN++
		}
		if ev.CostUSD != nil {
			a.costSum += *ev.CostUSD
		}
	}

	phases := make([]string, 0, len(byPhase))
	for phase := range byPhase {
		phases = append(phases, phase)
	}
	sort.Slice(phases, func(i, j int) bool {
		return phaseSortKey(phases[i]) < phaseSortKey(phases[j])
	})

	out := make([]PhaseSummaryContract, 0, len(phases))
	for _, phase := range phases {
		a := byPhase[phase]
		row := PhaseSummaryContract{Phase: phase, Events: a.events, TotalCostUSD: a.costSum}
		if a.durationN > 0 {
			row.AvgDurationMs = a.durationSum / float64(a.durationN)
		}
		if a.qualityN > 0 {
			row.AvgQuality = a.qualitySum / float64(a.qualityN)
		}
		out = append(out, row)
	}
	return out
}

// ValidateContract enforces the sanitized sharing contract before any submission/write path.
func ValidateContract(contract Contract) error {
	if strings.TrimSpace(contract.SchemaVersion) == "" {
		return errors.New("missing schema_version")
	}
	if contract.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", contract.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, contract.GeneratedAt); err != nil {
		return fmt.Errorf("invalid generated_at: %w", err)
	}
	if !isValidProvenance(contract.Provenance) {
		return errors.New("invalid provenance")
	}
	for i, run := range contract.BenchmarkRuns {
		if !strings.HasPrefix(run.AgentID, "anon-") {
			return fmt.Errorf("benchmark_runs[%d].agent_id must be sanitized", i)
		}
		if strings.TrimSpace(run.DecisionReason) != "" {
			return fmt.Errorf("benchmark_runs[%d].decision_reason must be omitted", i)
		}
		if strings.TrimSpace(run.ArtifactPath) != "" {
			return fmt.Errorf("benchmark_runs[%d].artifact_path must be omitted", i)
		}
	}
	for i, row := range contract.SemanticPhaseSummary {
		if sanitizePhase(row.Phase) != row.Phase {
			return fmt.Errorf("semantic_phase_summary[%d].phase is not normalized", i)
		}
	}
	return nil
}

func isValidProvenance(p ProvenanceContract) bool {
	return p.Contract == "sharing-leaderboard" &&
		p.ConsentMode == "explicit-opt-in" &&
		p.Egress == "local-file-only" &&
		p.SanitizationProfile == "sanitized-v1"
}
