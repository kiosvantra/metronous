package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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
	AgentID          string   `json:"agent_id"`
	Model            string   `json:"model"`
	Type             string   `json:"type"`
	CompositeScore   float64  `json:"composite_score"`
	Accuracy         float64  `json:"accuracy"`
	P95LatencyMs     float64  `json:"p95_latency_ms"`
	ToolSuccessRate  float64  `json:"tool_success_rate"`
	ROIScore         float64  `json:"roi_score"`
	TotalCostUSD     float64  `json:"total_cost_usd"`
	SampleSize       int      `json:"sample_size"`
	Verdict          string   `json:"verdict"`
	RecommendedModel string   `json:"recommended_model"`
	DecisionReason   string   `json:"decision_reason"`
	RunAt            int64    `json:"run_at"`
	Context          string   `json:"context"`
	Recommendation   string   `json:"recommendation"`
	TrendVerdicts    []string `json:"trend_verdicts"`
	TrendDirection   string   `json:"trend_direction"`
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
			trendVerdicts, _ := bs.GetVerdictTrendByModel(ctx, agentID, model, 8)
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
				Context:          evaluateAgentContext(*run),
				Recommendation:   verdictRecommendation(*run),
				TrendVerdicts:    trendVerdicts,
				TrendDirection:   trendDirection(trendVerdicts),
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
	ROIScore        float64 `json:"roi_score"`
	Context         string  `json:"context"`
	Recommendation  string  `json:"recommendation"`
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
				Rank:           i + 1,
				Model:          run.Model,
				Score:          run.CompositeScore,
				Verdict:        string(run.Verdict),
				Accuracy:       run.Accuracy,
				P95LatencyMs:   run.P95LatencyMs,
				ToolSuccessRate: run.ToolSuccessRate,
				Cost:           run.TotalCostUSD,
				Samples:        run.SampleSize,
				Label:          rankLabel(i+1, run.Verdict),
				ROIScore:       run.ROIScore,
				Context:        evaluateAgentContext(run),
				Recommendation: verdictRecommendation(run),
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

// evaluateAgentContext returns a qualitative assessment based on the agent's role and metrics.
func evaluateAgentContext(run store.BenchmarkRun) string {
	switch run.AgentID {
	case "sdd-orchestrator":
		if run.ToolSuccessRate >= 0.9 {
			return "Coordinating effectively — delegations succeeding at expected rate"
		} else if run.ToolSuccessRate >= 0.7 {
			return "Some delegation failures detected — may be attempting inline work"
		}
		return "High failure rate — orchestrator may be bypassing delegation pattern"
	case "sdd-apply":
		if run.ToolSuccessRate >= 0.9 {
			return "Implementations landing correctly — code changes applied successfully"
		} else if run.ToolSuccessRate >= 0.7 {
			return "Some implementation failures — review task definitions for clarity"
		}
		return "High implementation failure rate — task definitions may be incomplete"
	case "sdd-explore":
		if run.SampleSize >= 50 && run.ToolSuccessRate >= 0.9 {
			return "Deep exploration with high read success — investigations thorough"
		} else if run.ToolSuccessRate >= 0.8 {
			return "Adequate exploration — consider deeper codebase analysis"
		}
		return "Shallow exploration detected — may be missing critical context"
	case "sdd-verify":
		if run.ToolSuccessRate >= 0.9 {
			return "Validation passing — spec compliance checks executing correctly"
		} else if run.ToolSuccessRate >= 0.7 {
			return "Some validation failures — specs may need clarification"
		}
		return "Validation failing frequently — implementation may not match specs"
	case "sdd-spec":
		if run.ToolSuccessRate >= 0.9 {
			return "Spec writing succeeding — requirements captured correctly"
		}
		return "Spec generation issues — proposal inputs may be incomplete"
	case "sdd-design":
		if run.ToolSuccessRate >= 0.9 {
			return "Design artifacts generated successfully"
		}
		return "Design generation issues — proposal may need more detail"
	case "sdd-propose":
		if run.ToolSuccessRate >= 0.9 {
			return "Proposals being created from explorations correctly"
		}
		return "Proposal failures — exploration output may be insufficient"
	case "sdd-tasks":
		if run.ToolSuccessRate >= 0.9 {
			return "Task breakdown succeeding — specs and designs well-structured"
		}
		return "Task breakdown failures — specs may be ambiguous"
	case "sdd-init":
		if run.ToolSuccessRate >= 0.9 {
			return "Bootstrap executing correctly"
		}
		return "Bootstrap failures — check project configuration"
	case "sdd-archive":
		if run.ToolSuccessRate >= 0.9 {
			return "Archiving completing correctly"
		}
		return "Archive failures — verify change artifacts are complete"
	default:
		if run.ToolSuccessRate >= 0.9 {
			return "Agent performing within normal parameters"
		}
		return "Performance below expected thresholds for this agent role"
	}
}

// verdictRecommendation returns a verdict-specific recommendation sentence.
func verdictRecommendation(run store.BenchmarkRun) string {
	switch run.Verdict {
	case store.VerdictKeep:
		return "This agent+model combination is performing well. No action needed."
	case store.VerdictSwitch:
		if run.RecommendedModel != "" {
			return fmt.Sprintf("Consider switching to %s for better performance.", run.RecommendedModel)
		}
		return "Performance degradation detected. Consider evaluating alternative models."
	case store.VerdictUrgentSwitch:
		if run.RecommendedModel != "" {
			return fmt.Sprintf("Urgent: switch to %s immediately — critical thresholds breached.", run.RecommendedModel)
		}
		return "Urgent: critical performance thresholds breached. Immediate action required."
	case store.VerdictInsufficientData:
		return fmt.Sprintf("Not enough data yet (%d/%d samples). Keep using to gather more.", run.SampleSize, 50)
	default:
		return ""
	}
}

// trendDirection computes trend direction from verdict history.
func trendDirection(verdicts []string) string {
	if len(verdicts) < 2 {
		return "stable"
	}
	var first, last string
	for _, v := range verdicts {
		if v != "INSUFFICIENT_DATA" {
			if first == "" {
				first = v
			}
			last = v
		}
	}
	if first == "" || last == "" {
		return "stable"
	}
	firstSev := verdictSeverity(first)
	lastSev := verdictSeverity(last)
	if lastSev < firstSev {
		return "improving"
	}
	if lastSev > firstSev {
		return "degrading"
	}
	return "stable"
}

// verdictSeverity maps a verdict string to a numeric severity for trend comparison.
func verdictSeverity(v string) int {
	switch v {
	case "KEEP":
		return 0
	case "INSUFFICIENT_DATA":
		return 1
	case "SWITCH":
		return 2
	case "URGENT_SWITCH":
		return 3
	default:
		return 1
	}
}

// sessionItem is the JSON shape for a single row in /api/sessions.
type sessionItem struct {
	SessionID        string   `json:"session_id"`
	AgentID          string   `json:"agent_id"`
	Model            string   `json:"model"`
	Timestamp        int64    `json:"timestamp"`
	PromptTokens     *int     `json:"prompt_tokens"`
	CompletionTokens *int     `json:"completion_tokens"`
	CostUSD          *float64 `json:"cost_usd"`
}

// sessionsResponse is the full /api/sessions response shape.
type sessionsResponse struct {
	Sessions []sessionItem `json:"sessions"`
	Total    int           `json:"total"`
	Offset   int           `json:"offset"`
	Limit    int           `json:"limit"`
}

// handleSessions returns a paginated list of session summaries.
func handleSessions(es store.EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if es == nil {
			writeJSON(w, sessionsResponse{Sessions: []sessionItem{}, Total: 0, Offset: 0, Limit: 20})
			return
		}

		q := r.URL.Query()

		offset := 0
		if s := q.Get("offset"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v >= 0 {
				offset = v
			}
		}

		limit := 20
		if s := q.Get("limit"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v > 0 {
				if v > 100 {
					v = 100
				}
				limit = v
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		sessions, err := es.QuerySessions(ctx, store.SessionQuery{Limit: limit, Offset: offset})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "query sessions: "+err.Error())
			return
		}

		total, err := es.CountEvents(ctx, store.EventQuery{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "count events: "+err.Error())
			return
		}

		items := make([]sessionItem, len(sessions))
		for i, s := range sessions {
			items[i] = sessionItem{
				SessionID:        s.SessionID,
				AgentID:          s.AgentID,
				Model:            s.Model,
				Timestamp:        s.Timestamp.UnixMilli(),
				PromptTokens:     s.PromptTokens,
				CompletionTokens: s.CompletionTokens,
				CostUSD:          s.CostUSD,
			}
		}

		writeJSON(w, sessionsResponse{
			Sessions: items,
			Total:    total,
			Offset:   offset,
			Limit:    limit,
		})
	}
}

// eventItem is the JSON shape for a single event in /api/sessions/events.
type eventItem struct {
	ID               string   `json:"id"`
	AgentID          string   `json:"agent_id"`
	SessionID        string   `json:"session_id"`
	EventType        string   `json:"event_type"`
	Model            string   `json:"model"`
	Timestamp        int64    `json:"timestamp"`
	DurationMs       *int     `json:"duration_ms"`
	PromptTokens     *int     `json:"prompt_tokens"`
	CompletionTokens *int     `json:"completion_tokens"`
	CostUSD          *float64 `json:"cost_usd"`
	QualityScore     *float64 `json:"quality_score"`
	ToolName         *string  `json:"tool_name"`
	ToolSuccess      *bool    `json:"tool_success"`
}

// handleSessionEvents returns all events for a given session_id.
func handleSessionEvents(es store.EventStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.URL.Query().Get("session_id")
		if sessionID == "" {
			writeError(w, http.StatusBadRequest, "missing required query param: session_id")
			return
		}

		if es == nil {
			writeJSON(w, []eventItem{})
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		events, err := es.GetSessionEvents(ctx, sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "get session events: "+err.Error())
			return
		}

		items := make([]eventItem, len(events))
		for i, e := range events {
			items[i] = eventItem{
				ID:               e.ID,
				AgentID:          e.AgentID,
				SessionID:        e.SessionID,
				EventType:        e.EventType,
				Model:            e.Model,
				Timestamp:        e.Timestamp.UnixMilli(),
				DurationMs:       e.DurationMs,
				PromptTokens:     e.PromptTokens,
				CompletionTokens: e.CompletionTokens,
				CostUSD:          e.CostUSD,
				QualityScore:     e.QualityScore,
				ToolName:         e.ToolName,
				ToolSuccess:      e.ToolSuccess,
			}
		}

		writeJSON(w, items)
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
