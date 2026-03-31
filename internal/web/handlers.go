package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/discovery"
	"github.com/kiosvantra/metronous/internal/store"
)

// writeJSON encodes v as JSON and writes it to w with a 200 status.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a plain-text error with the given status code.
func writeError(w http.ResponseWriter, status int, msg string) {
	http.Error(w, msg, status)
}

// typePriority returns the sort key for an agent type: lower = shown first.
func typePriority(t string) int {
	switch t {
	case "primary":
		return 0
	case "subagent":
		return 1
	case "all":
		return 2
	case "built-in":
		return 3
	default:
		return 4
	}
}

// overviewItem is the JSON shape for a single row in /api/overview.
type overviewItem struct {
	AgentID         string  `json:"agent_id"`
	Model           string  `json:"model"`
	Type            string  `json:"type"`
	CompositeScore  float64 `json:"composite_score"`
	Accuracy        float64 `json:"accuracy"`
	P95LatencyMs    float64 `json:"p95_latency_ms"`
	ToolSuccessRate float64 `json:"tool_success_rate"`
	ROIScore        float64 `json:"roi_score"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
	SampleSize      int     `json:"sample_size"`
	Verdict         string  `json:"verdict"`
	RecommendedModel string `json:"recommended_model"`
	DecisionReason  string  `json:"decision_reason"`
	RunAt           int64   `json:"run_at"`
}

// handleOverview returns all latest runs per (agent, model), sorted by type
// priority then agent_id then composite_score DESC.
func handleOverview(bs store.BenchmarkStore, workDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		pairs, err := bs.ListAgentModels(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list agent models: "+err.Error())
			return
		}

		// Build typeByID map from discovered agents.
		agents := discovery.DiscoverAgents(workDir)
		typeByID := make(map[string]string, len(agents))
		for _, a := range agents {
			typeByID[a.ID] = a.Type
		}

		var items []overviewItem
		for _, pair := range pairs {
			agentID, model := pair[0], pair[1]
			run, err := bs.GetLatestRunByAgentModel(ctx, agentID, model)
			if err != nil || run == nil {
				continue
			}
			agentType, ok := typeByID[agentID]
			if !ok {
				agentType = "primary"
			}
			items = append(items, overviewItem{
				AgentID:          run.AgentID,
				Model:            run.Model,
				Type:             agentType,
				CompositeScore:   run.CompositeScore,
				Accuracy:         run.Accuracy,
				P95LatencyMs:     run.P95LatencyMs,
				ToolSuccessRate:  run.ToolSuccessRate,
				ROIScore:         run.ROIScore,
				TotalCostUSD:     run.TotalCostUSD,
				SampleSize:       run.SampleSize,
				Verdict:          string(run.Verdict),
				RecommendedModel: run.RecommendedModel,
				DecisionReason:   run.DecisionReason,
				RunAt:            run.RunAt.UnixMilli(),
			})
		}

		sort.Slice(items, func(i, j int) bool {
			pi, pj := typePriority(items[i].Type), typePriority(items[j].Type)
			if pi != pj {
				return pi < pj
			}
			if items[i].AgentID != items[j].AgentID {
				return items[i].AgentID < items[j].AgentID
			}
			return items[i].CompositeScore > items[j].CompositeScore
		})

		writeJSON(w, items)
	}
}

// rankItem is one entry in the compare ranking array.
type rankItem struct {
	Rank            int     `json:"rank"`
	Model           string  `json:"model"`
	Score           float64 `json:"score"`
	Verdict         string  `json:"verdict"`
	Accuracy        float64 `json:"accuracy"`
	P95LatencyMs    float64 `json:"p95_latency_ms"`
	ToolSuccessRate float64 `json:"tool_success_rate"`
	Cost            float64 `json:"cost"`
	Samples         int     `json:"samples"`
	Label           string  `json:"label"`
}

// comparisonBlock is the pairwise comparison section.
type comparisonBlock struct {
	Winner         string  `json:"winner"`
	Recommendation string  `json:"recommendation"`
	AccuracyDelta  float64 `json:"accuracy_delta"`
	LatencyDeltaMs float64 `json:"latency_delta_ms"`
	CostDeltaPct   float64 `json:"cost_delta_pct"`
	ScoreDelta     float64 `json:"score_delta"`
}

// compareResponse is the full /api/compare response.
type compareResponse struct {
	AgentID     string           `json:"agent_id"`
	ModelsCount int              `json:"models_count"`
	Ranking     []rankItem       `json:"ranking"`
	Comparison  *comparisonBlock `json:"comparison,omitempty"`
}

// rankLabel derives the display label for a ranking entry.
func rankLabel(rank int, verdict store.VerdictType) string {
	if rank == 1 {
		return "BEST"
	}
	switch verdict {
	case store.VerdictSwitch, store.VerdictUrgentSwitch:
		return "SWITCH"
	case store.VerdictInsufficientData:
		return "INSUFFICIENT"
	case store.VerdictKeep:
		return "KEEP"
	}
	return ""
}

// handleCompare returns a ranked model comparison for a single agent.
func handleCompare(bs store.BenchmarkStore, workDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agent")
		if agentID == "" {
			writeError(w, http.StatusBadRequest, "missing required query param: agent")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		pairs, err := bs.ListAgentModels(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list agent models: "+err.Error())
			return
		}

		// Collect latest runs for this agent only.
		var runs []store.BenchmarkRun
		for _, pair := range pairs {
			if pair[0] != agentID {
				continue
			}
			run, err := bs.GetLatestRunByAgentModel(ctx, pair[0], pair[1])
			if err != nil || run == nil {
				continue
			}
			runs = append(runs, *run)
		}

		// Sort by CompositeScore DESC.
		sort.Slice(runs, func(i, j int) bool {
			return runs[i].CompositeScore > runs[j].CompositeScore
		})

		ranking := make([]rankItem, len(runs))
		for i, run := range runs {
			ranking[i] = rankItem{
				Rank:            i + 1,
				Model:           run.Model,
				Score:           run.CompositeScore,
				Verdict:         string(run.Verdict),
				Accuracy:        run.Accuracy,
				P95LatencyMs:    run.P95LatencyMs,
				ToolSuccessRate: run.ToolSuccessRate,
				Cost:            run.TotalCostUSD,
				Samples:         run.SampleSize,
				Label:           rankLabel(i+1, run.Verdict),
			}
		}

		resp := compareResponse{
			AgentID:     agentID,
			ModelsCount: len(runs),
			Ranking:     ranking,
		}

		// Pairwise comparison when at least 2 models have data.
		if len(runs) >= 2 {
			cmp := benchmark.CompareModels(runs[0], runs[1])
			resp.Comparison = &comparisonBlock{
				Winner:         cmp.BetterModel,
				Recommendation: cmp.Recommendation,
				AccuracyDelta:  cmp.AccuracyDelta,
				LatencyDeltaMs: cmp.LatencyDeltaMs,
				CostDeltaPct:   cmp.CostDeltaPct,
				ScoreDelta:     cmp.ScoreDelta,
			}
		}

		writeJSON(w, resp)
	}
}

// trendResponse is the /api/trend response shape.
type trendResponse struct {
	AgentID  string   `json:"agent_id"`
	Model    string   `json:"model"`
	Verdicts []string `json:"verdicts"`
}

// handleTrend returns the last 12 verdicts for a specific (agent, model) pair.
func handleTrend(bs store.BenchmarkStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agent")
		model := r.URL.Query().Get("model")
		if agentID == "" || model == "" {
			writeError(w, http.StatusBadRequest, "missing required query params: agent, model")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		verdicts, err := bs.GetVerdictTrendByModel(ctx, agentID, model, 12)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get verdict trend: "+err.Error())
			return
		}

		writeJSON(w, trendResponse{
			AgentID:  agentID,
			Model:    model,
			Verdicts: verdicts,
		})
	}
}
