package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kiosvantra/metronous/internal/benchmark"
	"github.com/kiosvantra/metronous/internal/config"
	"github.com/kiosvantra/metronous/internal/decision"
	"github.com/kiosvantra/metronous/internal/store"
)

func TestBuildVerdictExplanation_Scenario1_FreeModelQualityGap(t *testing.T) {
	run := store.BenchmarkRun{
		Model:            "gpt-5.4-nano",
		RecommendedModel: "claude-sonnet-4-6",
		Accuracy:         0.75,
		Verdict:          store.VerdictSwitch,
		TotalCostUSD:     0,
		RunAt:            time.Now(),
	}
	pricing := map[string]float64{
		"gpt-5.4-nano":      0,
		"claude-sonnet-4-6": 10,
	}

	ex := buildVerdictExplanation(run, pricing, 0.90, 0.15)
	if ex.FailureType != "quality-gap" {
		t.Fatalf("FailureType: got %q, want quality-gap", ex.FailureType)
	}
	if !ex.IsFreeToPayTransition {
		t.Fatal("IsFreeToPayTransition: got false, want true")
	}
	if ex.CostImpactStr != "was $0 (+$10.00/session)" {
		t.Fatalf("CostImpactStr: got %q", ex.CostImpactStr)
	}

	detail := renderDetailPanel(run, pricing, nil, 0.90, 0.15)
	if !strings.Contains(detail, "⚠ QUALITY INSUFFICIENT (Free Model)") {
		t.Fatalf("detail missing scenario header:\n%s", detail)
	}
	if !strings.Contains(detail, "was $0") {
		t.Fatalf("detail missing free-to-paid cost transition:\n%s", detail)
	}
}

func TestBuildVerdictExplanation_Scenario2_PaidModelQualityGap(t *testing.T) {
	run := store.BenchmarkRun{
		Model:            "claude-sonnet-4-6",
		RecommendedModel: "claude-opus-4-5",
		Accuracy:         0.87,
		Verdict:          store.VerdictSwitch,
		TotalCostUSD:     10,
		RunAt:            time.Now(),
	}
	pricing := map[string]float64{
		"claude-sonnet-4-6": 10,
		"claude-opus-4-5":   75,
	}

	ex := buildVerdictExplanation(run, pricing, 0.90, 0.15)
	if ex.FailureType != "quality-gap" {
		t.Fatalf("FailureType: got %q, want quality-gap", ex.FailureType)
	}
	if ex.RecommendedModel == "" {
		t.Fatal("RecommendedModel is empty")
	}
	if ex.CostImpactStr != "+$65.00/session" {
		t.Fatalf("CostImpactStr: got %q, want +$65.00/session", ex.CostImpactStr)
	}

	detail := renderDetailPanel(run, pricing, nil, 0.90, 0.15)
	if !strings.Contains(detail, "⚠ QUALITY INSUFFICIENT (Paid Model)") {
		t.Fatalf("detail missing paid quality-gap header:\n%s", detail)
	}
	if !strings.Contains(detail, "+$65.00/session") {
		t.Fatalf("detail missing tier-upgrade cost delta:\n%s", detail)
	}
}

func TestBuildVerdictExplanation_Scenario3_CostOptimization(t *testing.T) {
	thresholds := config.DefaultThresholdValues()
	thresholds.Defaults.MinAccuracy = 0.90
	thresholds.Defaults.MinROIScore = 0.15
	thresholds.ModelRecommendations.PerformanceModel = "claude-sonnet-4-6"
	thresholds.ModelPricing.Models = map[string]float64{
		"claude-opus-4-5":   20,
		"claude-sonnet-4-6": 10,
	}

	engine := decision.NewDecisionEngine(&thresholds)
	verdict := engine.Evaluate(context.Background(), benchmark.WindowMetrics{
		AgentID:         "test-agent",
		Model:           "claude-opus-4-5",
		SampleSize:      100,
		Accuracy:        0.94,
		ToolSuccessRate: 0.95,
		ROIScore:        0.08,
		TotalCostUSD:    20,
	})
	if verdict.RecommendedModel != "claude-sonnet-4-6" {
		t.Fatalf("decision recommended model: got %q, want claude-sonnet-4-6", verdict.RecommendedModel)
	}

	run := store.BenchmarkRun{
		Model:            "claude-opus-4-5",
		RecommendedModel: verdict.RecommendedModel,
		Accuracy:         0.94,
		ROIScore:         0.08,
		Verdict:          verdict.Type,
		TotalCostUSD:     20,
		RunAt:            time.Now(),
	}
	pricing := map[string]float64{
		"claude-opus-4-5":   20,
		"claude-sonnet-4-6": 10,
	}

	ex := buildVerdictExplanation(run, pricing, 0.90, 0.15)
	if ex.FailureType != "cost-optimization" {
		t.Fatalf("FailureType: got %q, want cost-optimization", ex.FailureType)
	}
	if !ex.IsQualityConstrained {
		t.Fatal("IsQualityConstrained: got false, want true")
	}

	detail := renderDetailPanel(run, pricing, nil, 0.90, 0.15)
	if !strings.Contains(detail, "✓ QUALITY SUFFICIENT — Cost Optimization Available") {
		t.Fatalf("detail missing optimization header:\n%s", detail)
	}
	if !strings.Contains(detail, "saves $1.75/session") {
		t.Fatalf("detail missing savings message:\n%s", detail)
	}
}

func TestBuildVerdictExplanation_AccuracyEqualsThreshold_IsKeep(t *testing.T) {
	run := store.BenchmarkRun{
		Model:        "claude-sonnet-4-6",
		Accuracy:     0.90,
		ROIScore:     0.20,
		Verdict:      store.VerdictKeep,
		TotalCostUSD: 10,
		RunAt:        time.Now(),
	}
	pricing := map[string]float64{"claude-sonnet-4-6": 10}

	ex := buildVerdictExplanation(run, pricing, 0.90, 0.15)
	if ex.FailureType != "keep" {
		t.Fatalf("FailureType: got %q, want keep", ex.FailureType)
	}
}

func TestBuildVerdictExplanation_HighROI_DoesNotOptimizeCost(t *testing.T) {
	run := store.BenchmarkRun{
		Model:            "claude-opus-4-5",
		RecommendedModel: "claude-sonnet-4-6",
		Accuracy:         0.94,
		ROIScore:         0.30,
		Verdict:          store.VerdictSwitch,
		TotalCostUSD:     20,
		RunAt:            time.Now(),
	}
	pricing := map[string]float64{
		"claude-opus-4-5":   20,
		"claude-sonnet-4-6": 10,
	}

	ex := buildVerdictExplanation(run, pricing, 0.90, 0.15)
	if ex.FailureType != "keep" {
		t.Fatalf("FailureType: got %q, want keep", ex.FailureType)
	}
}

func TestBuildVerdictExplanation_ROIFailure_MissingRecommendedPricing_NotKeep(t *testing.T) {
	run := store.BenchmarkRun{
		Model:            "claude-opus-4-5",
		RecommendedModel: "claude-sonnet-4-6",
		Accuracy:         0.94,
		ROIScore:         0.08,
		Verdict:          store.VerdictSwitch,
		TotalCostUSD:     20,
		RunAt:            time.Now(),
	}
	pricing := map[string]float64{
		"claude-opus-4-5": 20,
	}

	ex := buildVerdictExplanation(run, pricing, 0.90, 0.15)
	if ex.FailureType == "keep" {
		t.Fatalf("FailureType: got %q, expected non-keep when recommended pricing is missing", ex.FailureType)
	}
	if ex.FailureType != "cost-data-missing" {
		t.Fatalf("FailureType: got %q, want cost-data-missing", ex.FailureType)
	}
}

func TestRenderDetailPanel_CostDataMissing_NoGarbledRecommendedFields(t *testing.T) {
	run := store.BenchmarkRun{
		Model:            "claude-sonnet-4-6",
		RecommendedModel: "",
		Accuracy:         0.80,
		Verdict:          store.VerdictSwitch,
		TotalCostUSD:     0,
		RunAt:            time.Now(),
	}
	pricing := map[string]float64{
		"claude-sonnet-4-6": 10,
	}

	detail := renderDetailPanel(run, pricing, nil, 0.90, 0.15)
	if strings.Contains(detail, "🎯 ") {
		t.Fatalf("detail contains garbled recommended model field:\n%s", detail)
	}
	if strings.Contains(detail, " ()") {
		t.Fatalf("detail contains garbled recommended cost field:\n%s", detail)
	}
	if !strings.Contains(detail, "N/A") {
		t.Fatalf("detail missing fallback recommended label:\n%s", detail)
	}
}

func TestBuildVerdictExplanation_Scenario4_FreeModelKeep(t *testing.T) {
	run := store.BenchmarkRun{
		Model:        "gpt-5.4-nano",
		Accuracy:     0.92,
		Verdict:      store.VerdictKeep,
		TotalCostUSD: 0,
		RunAt:        time.Now(),
	}
	pricing := map[string]float64{"gpt-5.4-nano": 0}

	ex := buildVerdictExplanation(run, pricing, 0.90, 0.15)
	if ex.FailureType != "keep" {
		t.Fatalf("FailureType: got %q, want keep", ex.FailureType)
	}
	if ex.RecommendedModel != "" {
		t.Fatalf("RecommendedModel: got %q, want empty", ex.RecommendedModel)
	}

	detail := renderDetailPanel(run, pricing, nil, 0.90, 0.15)
	if !strings.Contains(detail, "✅ KEEP") {
		t.Fatalf("detail missing keep decision:\n%s", detail)
	}
}

func TestBuildVerdictExplanation_Scenario5_CostDataMissing(t *testing.T) {
	run := store.BenchmarkRun{
		Model:            "claude-sonnet-4-6",
		RecommendedModel: "claude-opus-4-5",
		Accuracy:         0.87,
		Verdict:          store.VerdictSwitch,
		TotalCostUSD:     0,
		RunAt:            time.Now(),
	}
	pricing := map[string]float64{
		"claude-sonnet-4-6": 10,
		"claude-opus-4-5":   75,
	}

	ex := buildVerdictExplanation(run, pricing, 0.90, 0.15)
	if ex.FailureType != "cost-data-missing" {
		t.Fatalf("FailureType: got %q, want cost-data-missing", ex.FailureType)
	}
	if !ex.IsInterimRec {
		t.Fatal("IsInterimRec: got false, want true")
	}

	detail := renderDetailPanel(run, pricing, nil, 0.90, 0.15)
	if !strings.Contains(detail, "SWITCH (temporary)") {
		t.Fatalf("detail missing interim switch message:\n%s", detail)
	}
	if !strings.Contains(detail, "Data pending") {
		t.Fatalf("detail missing data pending note:\n%s", detail)
	}
}

func TestBuildVerdictExplanation_KeepVerdict_PaidModel(t *testing.T) {
	run := store.BenchmarkRun{
		Model:        "claude-sonnet-4-6",
		Accuracy:     0.93,
		ROIScore:     0.30,
		Verdict:      store.VerdictKeep,
		TotalCostUSD: 12,
		RunAt:        time.Now(),
	}
	pricing := map[string]float64{"claude-sonnet-4-6": 10}

	ex := buildVerdictExplanation(run, pricing, 0.90, 0.15)
	if ex.FailureType != "keep" {
		t.Fatalf("FailureType: got %q, want keep", ex.FailureType)
	}
	if ex.IsInterimRec || ex.IsQualityConstrained || ex.IsFreeToPayTransition {
		t.Fatalf("unexpected flags set: %+v", ex)
	}
}

func TestBuildVerdictExplanation_InsufficientData(t *testing.T) {
	run := store.BenchmarkRun{
		Model:        "claude-sonnet-4-6",
		Accuracy:     0.50,
		Verdict:      store.VerdictInsufficientData,
		TotalCostUSD: 5,
		RunAt:        time.Now(),
	}
	ex := buildVerdictExplanation(run, map[string]float64{"claude-sonnet-4-6": 10}, 0.90, 0.15)
	if ex.FailureType != "" {
		t.Fatalf("FailureType: got %q, want empty", ex.FailureType)
	}
	if ex.RecommendedModel != "" {
		t.Fatalf("RecommendedModel: got %q, want empty", ex.RecommendedModel)
	}
}

func TestCostLabel_ActualVsEstimated(t *testing.T) {
	pricing := map[string]float64{"paid": 10, "free": 0}

	actual := store.BenchmarkRun{Model: "paid", TotalCostUSD: 12}
	if got := formatCurrentCostLabel(actual, pricing); got != "$12.00/session (actual)" {
		t.Fatalf("actual label: got %q", got)
	}

	estimated := store.BenchmarkRun{Model: "paid", TotalCostUSD: 0}
	if got := formatCurrentCostLabel(estimated, pricing); got != "$10.00/session (estimated)" {
		t.Fatalf("estimated label: got %q", got)
	}

	free := store.BenchmarkRun{Model: "free", TotalCostUSD: 0}
	if got := formatCurrentCostLabel(free, pricing); got != "free" {
		t.Fatalf("free label: got %q", got)
	}
}

func TestBuildVerdictExplanation_ZeroSamplesEdgeCase(t *testing.T) {
	run := store.BenchmarkRun{
		Model:        "claude-sonnet-4-6",
		Accuracy:     0,
		SampleSize:   0,
		Verdict:      store.VerdictInsufficientData,
		TotalCostUSD: 0,
		RunAt:        time.Now(),
	}
	ex := buildVerdictExplanation(run, map[string]float64{"claude-sonnet-4-6": 10}, 0.90, 0.15)
	if (ex != VerdictExplanation{}) {
		t.Fatalf("expected empty explanation for insufficient data, got %+v", ex)
	}
}
