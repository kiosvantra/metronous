package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
)

// mockBS is a configurable mock BenchmarkStore for handler tests.
type mockBS struct {
	agentModels [][2]string
	runsByKey   map[string]*store.BenchmarkRun // "agentID\tmodel" → run
	trendByKey  map[string][]string            // "agentID\tmodel" → verdicts
}

func newMockBS() *mockBS {
	return &mockBS{
		runsByKey:  make(map[string]*store.BenchmarkRun),
		trendByKey: make(map[string][]string),
	}
}

func (m *mockBS) addRun(run store.BenchmarkRun) {
	key := run.AgentID + "\t" + run.Model
	m.agentModels = append(m.agentModels, [2]string{run.AgentID, run.Model})
	m.runsByKey[key] = &run
}

func (m *mockBS) SaveRun(context.Context, store.BenchmarkRun) error        { return nil }
func (m *mockBS) GetRuns(context.Context, string, int) ([]store.BenchmarkRun, error) {
	return nil, nil
}
func (m *mockBS) QueryRuns(context.Context, store.BenchmarkQuery) ([]store.BenchmarkRun, error) {
	return nil, nil
}
func (m *mockBS) CountRuns(context.Context, store.BenchmarkQuery) (int, error) { return 0, nil }
func (m *mockBS) GetLatestRun(context.Context, string) (*store.BenchmarkRun, error) {
	return nil, nil
}
func (m *mockBS) ListAgents(_ context.Context) ([]string, error) {
	seen := map[string]bool{}
	var agents []string
	for _, p := range m.agentModels {
		if !seen[p[0]] {
			seen[p[0]] = true
			agents = append(agents, p[0])
		}
	}
	return agents, nil
}
func (m *mockBS) ListAgentModels(_ context.Context) ([][2]string, error) {
	return m.agentModels, nil
}
func (m *mockBS) GetLatestRunByAgentModel(_ context.Context, agentID, model string) (*store.BenchmarkRun, error) {
	key := agentID + "\t" + model
	return m.runsByKey[key], nil
}
func (m *mockBS) GetVerdictTrend(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (m *mockBS) GetVerdictTrendByModel(_ context.Context, agentID, model string, _ int) ([]string, error) {
	key := agentID + "\t" + model
	return m.trendByKey[key], nil
}
func (m *mockBS) Close() error { return nil }

// helper: make a BenchmarkRun with common defaults.
func makeRun(agentID, model string, score float64, verdict store.VerdictType) store.BenchmarkRun {
	return store.BenchmarkRun{
		AgentID:        agentID,
		Model:          model,
		CompositeScore: score,
		Accuracy:       1.0,
		P95LatencyMs:   1000,
		ToolSuccessRate: 1.0,
		TotalCostUSD:   0.50,
		SampleSize:     100,
		Verdict:        verdict,
		RunAt:          time.Now(),
	}
}

func TestOverview_ReturnsRuns(t *testing.T) {
	bs := newMockBS()
	bs.addRun(makeRun("agent-a", "model-1", 0.95, store.VerdictKeep))
	bs.addRun(makeRun("agent-a", "model-2", 0.80, store.VerdictInsufficientData))

	handler := handleOverview(bs, "")
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var items []overviewItem
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	// Should be sorted by score DESC within same agent.
	if items[0].CompositeScore < items[1].CompositeScore {
		t.Errorf("items not sorted by score DESC: %.2f < %.2f", items[0].CompositeScore, items[1].CompositeScore)
	}
}

func TestOverview_Empty(t *testing.T) {
	bs := newMockBS()
	handler := handleOverview(bs, "")
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var items []overviewItem
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty array, got %d items", len(items))
	}
}

func TestCompare_MissingAgent(t *testing.T) {
	bs := newMockBS()
	handler := handleCompare(bs, "")
	req := httptest.NewRequest(http.MethodGet, "/api/compare", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCompare_SingleModel(t *testing.T) {
	bs := newMockBS()
	bs.addRun(makeRun("test-agent", "model-a", 0.90, store.VerdictKeep))

	handler := handleCompare(bs, "")
	req := httptest.NewRequest(http.MethodGet, "/api/compare?agent=test-agent", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp compareResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ModelsCount != 1 {
		t.Errorf("models_count = %d, want 1", resp.ModelsCount)
	}
	if resp.Comparison != nil {
		t.Error("comparison should be nil with only 1 model")
	}
	if len(resp.Ranking) != 1 || resp.Ranking[0].Label != "BEST" {
		t.Errorf("expected rank 1 with BEST label, got %+v", resp.Ranking)
	}
}

func TestCompare_TwoModels(t *testing.T) {
	bs := newMockBS()
	bs.addRun(makeRun("test-agent", "model-a", 0.92, store.VerdictKeep))
	bs.addRun(makeRun("test-agent", "model-b", 0.75, store.VerdictSwitch))

	handler := handleCompare(bs, "")
	req := httptest.NewRequest(http.MethodGet, "/api/compare?agent=test-agent", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var resp compareResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ModelsCount != 2 {
		t.Errorf("models_count = %d, want 2", resp.ModelsCount)
	}
	if resp.Comparison == nil {
		t.Fatal("comparison should not be nil with 2 models")
	}
	// model-a has higher score, should be rank 1.
	if resp.Ranking[0].Model != "model-a" {
		t.Errorf("rank 1 should be model-a, got %s", resp.Ranking[0].Model)
	}
	if resp.Ranking[0].Label != "BEST" {
		t.Errorf("rank 1 label should be BEST, got %s", resp.Ranking[0].Label)
	}
	if resp.Ranking[1].Label != "SWITCH" {
		t.Errorf("rank 2 label should be SWITCH, got %s", resp.Ranking[1].Label)
	}
}

func TestCompare_UnknownAgent(t *testing.T) {
	bs := newMockBS()
	handler := handleCompare(bs, "")
	req := httptest.NewRequest(http.MethodGet, "/api/compare?agent=nonexistent", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp compareResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ModelsCount != 0 {
		t.Errorf("models_count = %d, want 0", resp.ModelsCount)
	}
}

func TestTrend_MissingParams(t *testing.T) {
	bs := newMockBS()
	handler := handleTrend(bs)

	tests := []struct {
		name string
		url  string
	}{
		{"missing both", "/api/trend"},
		{"missing model", "/api/trend?agent=x"},
		{"missing agent", "/api/trend?model=y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestTrend_ReturnsVerdicts(t *testing.T) {
	bs := newMockBS()
	bs.trendByKey["agent-x\tmodel-y"] = []string{"KEEP", "KEEP", "SWITCH", "KEEP"}

	handler := handleTrend(bs)
	req := httptest.NewRequest(http.MethodGet, "/api/trend?agent=agent-x&model=model-y", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp trendResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AgentID != "agent-x" {
		t.Errorf("agent_id = %q, want agent-x", resp.AgentID)
	}
	if len(resp.Verdicts) != 4 {
		t.Errorf("verdicts length = %d, want 4", len(resp.Verdicts))
	}
}

func TestRankLabel(t *testing.T) {
	tests := []struct {
		rank    int
		verdict store.VerdictType
		want    string
	}{
		{1, store.VerdictKeep, "BEST"},
		{1, store.VerdictSwitch, "BEST"},
		{2, store.VerdictKeep, "KEEP"},
		{2, store.VerdictSwitch, "SWITCH"},
		{3, store.VerdictInsufficientData, "INSUFFICIENT"},
		{2, store.VerdictUrgentSwitch, "SWITCH"},
	}
	for _, tt := range tests {
		got := rankLabel(tt.rank, tt.verdict)
		if got != tt.want {
			t.Errorf("rankLabel(%d, %s) = %q, want %q", tt.rank, tt.verdict, got, tt.want)
		}
	}
}
