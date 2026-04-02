package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kiosvantra/metronous/internal/store"
)

// benchmarkSchema defines the DDL for benchmark.db.
const benchmarkSchema = `
-- Core benchmark run table (one row per run per agent)
CREATE TABLE IF NOT EXISTS benchmark_runs (
    id                TEXT PRIMARY KEY,
    run_at            INTEGER NOT NULL,
    window_days       INTEGER NOT NULL DEFAULT 7,
    agent_id          TEXT NOT NULL,
    model             TEXT NOT NULL,
    accuracy          REAL NOT NULL DEFAULT 0.0,
    avg_latency_ms    REAL NOT NULL DEFAULT 0.0,
    p50_latency_ms    REAL NOT NULL DEFAULT 0.0,
    p95_latency_ms    REAL NOT NULL DEFAULT 0.0,
    p99_latency_ms    REAL NOT NULL DEFAULT 0.0,
    tool_success_rate REAL NOT NULL DEFAULT 0.0,
    roi_score         REAL NOT NULL DEFAULT 0.0,
    total_cost_usd    REAL NOT NULL DEFAULT 0.0,
    sample_size       INTEGER NOT NULL DEFAULT 0,
    verdict           TEXT NOT NULL,
    recommended_model TEXT NOT NULL DEFAULT '',
    decision_reason   TEXT NOT NULL DEFAULT '',
    artifact_path     TEXT NOT NULL DEFAULT '',
    avg_quality_score REAL NOT NULL DEFAULT 0.0
);

-- Indexes for common queries
CREATE INDEX IF NOT EXISTS idx_benchmark_agent_ts ON benchmark_runs(agent_id, run_at DESC);
CREATE INDEX IF NOT EXISTS idx_benchmark_run_at ON benchmark_runs(run_at DESC);
CREATE INDEX IF NOT EXISTS idx_benchmark_verdict ON benchmark_runs(verdict, run_at DESC);
`

// addAvgQualityScoreColumn migrates existing databases that predate avg_quality_score.
const addAvgQualityScoreColumn = `ALTER TABLE benchmark_runs ADD COLUMN avg_quality_score REAL NOT NULL DEFAULT 0.0`

// addRunKindColumn migrates existing databases to add the run_kind discriminator.
// Default 'weekly' preserves backward compatibility for all pre-existing rows.
const addRunKindColumn = `ALTER TABLE benchmark_runs ADD COLUMN run_kind TEXT NOT NULL DEFAULT 'weekly'`

// addWindowStartColumn migrates existing databases to add the window start timestamp (ms UTC).
const addWindowStartColumn = `ALTER TABLE benchmark_runs ADD COLUMN window_start INTEGER NOT NULL DEFAULT 0`

// addWindowEndColumn migrates existing databases to add the window end timestamp (ms UTC).
const addWindowEndColumn = `ALTER TABLE benchmark_runs ADD COLUMN window_end INTEGER NOT NULL DEFAULT 0`

// BenchmarkStore is a SQLite-backed implementation of store.BenchmarkStore.
type BenchmarkStore struct {
	writeDB *sql.DB
	readDB  *sql.DB
	path    string
}

// Compile-time interface check.
var _ store.BenchmarkStore = (*BenchmarkStore)(nil)

// NewBenchmarkStore opens (or creates) the benchmark SQLite database at path,
// applies WAL pragmas, and runs schema migrations.
func NewBenchmarkStore(path string) (*BenchmarkStore, error) {
	writeDB, err := openDB(path)
	if err != nil {
		return nil, fmt.Errorf("open benchmark write connection: %w", err)
	}

	// Apply schema migrations.
	if err := ApplyBenchmarkMigrations(context.Background(), writeDB); err != nil {
		_ = writeDB.Close()
		return nil, err
	}

	// Open a separate read-pool connection.
	// For in-memory databases (:memory:) used in tests, share the write connection.
	var readDB *sql.DB
	if path == ":memory:" {
		readDB = writeDB
	} else {
		readDB, err = openReadDB(path)
		if err != nil {
			_ = writeDB.Close()
			return nil, fmt.Errorf("open benchmark read connection: %w", err)
		}
	}

	return &BenchmarkStore{
		writeDB: writeDB,
		readDB:  readDB,
		path:    path,
	}, nil
}

// applyAddColumnMigration executes a single ALTER TABLE ADD COLUMN statement and
// silently ignores "duplicate column name" errors (idempotent — safe on fresh DBs
// where the CREATE TABLE already includes the column, and on re-runs).
func applyAddColumnMigration(ctx context.Context, db *sql.DB, stmt, colName string) error {
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		if strings.Contains(err.Error(), "duplicate column name") {
			return nil // column already exists — idempotent
		}
		return fmt.Errorf("apply %s migration: %w", colName, err)
	}
	return nil
}

// ApplyBenchmarkMigrations creates all tables and indexes for benchmark.db,
// then applies any additive column migrations for existing databases.
// It is idempotent and safe to call at startup.
func ApplyBenchmarkMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, benchmarkSchema); err != nil {
		return fmt.Errorf("apply benchmark schema: %w", err)
	}

	migrations := []struct {
		stmt    string
		colName string
	}{
		{addAvgQualityScoreColumn, "avg_quality_score"},
		{addRunKindColumn, "run_kind"},
		{addWindowStartColumn, "window_start"},
		{addWindowEndColumn, "window_end"},
	}
	for _, m := range migrations {
		if err := applyAddColumnMigration(ctx, db, m.stmt, m.colName); err != nil {
			return err
		}
	}
	return nil
}

// SaveRun persists a benchmark run. If run.ID is empty, a UUID is generated.
// If RunKind is empty it defaults to RunKindWeekly for backward compatibility.
func (bs *BenchmarkStore) SaveRun(ctx context.Context, run store.BenchmarkRun) error {
	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	if run.RunKind == "" {
		run.RunKind = store.RunKindWeekly
	}

	const q = `
		INSERT INTO benchmark_runs (
			id, run_at, window_days, agent_id, model,
			accuracy, avg_latency_ms, p50_latency_ms, p95_latency_ms, p99_latency_ms,
			tool_success_rate, roi_score, total_cost_usd, sample_size,
			verdict, recommended_model, decision_reason, artifact_path, avg_quality_score,
			run_kind, window_start, window_end
		) VALUES (
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?
		)`

	_, err := bs.writeDB.ExecContext(ctx, q,
		run.ID,
		run.RunAt.UTC().UnixMilli(),
		run.WindowDays,
		run.AgentID,
		run.Model,
		run.Accuracy,
		run.AvgLatencyMs,
		run.P50LatencyMs,
		run.P95LatencyMs,
		run.P99LatencyMs,
		run.ToolSuccessRate,
		run.ROIScore,
		run.TotalCostUSD,
		run.SampleSize,
		string(run.Verdict),
		run.RecommendedModel,
		run.DecisionReason,
		run.ArtifactPath,
		run.AvgQualityScore,
		string(run.RunKind),
		run.WindowStart.UTC().UnixMilli(),
		run.WindowEnd.UTC().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("save benchmark run: %w", err)
	}
	return nil
}

// MaxQueryLimit prevents OOM crashes from unbounded queries
const MaxQueryLimit = 10000

// GetRuns returns up to limit benchmark runs for the given agent, ordered by run_at DESC.
// If agentID is empty, runs for all agents are returned.
// Pass limit=0 for default cap (MaxQueryLimit), enforced to prevent OOM.
func (bs *BenchmarkStore) GetRuns(ctx context.Context, agentID string, limit int) ([]store.BenchmarkRun, error) {
	// Enforce maximum limit to prevent OOM with millions of rows
	if limit == 0 || limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}

	var (
		conditions []string
		args       []interface{}
	)

	if agentID != "" {
		conditions = append(conditions, "agent_id = ?")
		args = append(args, agentID)
	}

	q := `SELECT id, run_at, window_days, agent_id, model,
		accuracy, avg_latency_ms, p50_latency_ms, p95_latency_ms, p99_latency_ms,
		tool_success_rate, roi_score, total_cost_usd, sample_size,
		verdict, recommended_model, decision_reason, artifact_path, avg_quality_score,
		run_kind, window_start, window_end
		FROM benchmark_runs`

	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}
	q += " ORDER BY run_at DESC"
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := bs.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("get benchmark runs: %w", err)
	}
	defer rows.Close()

	return scanBenchmarkRuns(rows)
}

// QueryRuns retrieves benchmark runs matching the supplied filter criteria.
// Results are ordered by run_at DESC. Supports Offset/Limit for sliding-window pagination.
func (bs *BenchmarkStore) QueryRuns(ctx context.Context, query store.BenchmarkQuery) ([]store.BenchmarkRun, error) {
	limit := query.Limit
	if limit == 0 || limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}

	var (
		conditions []string
		args       []interface{}
	)

	if query.AgentID != "" {
		conditions = append(conditions, "agent_id = ?")
		args = append(args, query.AgentID)
	}

	q := `SELECT id, run_at, window_days, agent_id, model,
		accuracy, avg_latency_ms, p50_latency_ms, p95_latency_ms, p99_latency_ms,
		tool_success_rate, roi_score, total_cost_usd, sample_size,
		verdict, recommended_model, decision_reason, artifact_path, avg_quality_score,
		run_kind, window_start, window_end
		FROM benchmark_runs`

	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}
	q += " ORDER BY run_at DESC"
	q += " LIMIT ?"
	args = append(args, limit)
	if query.Offset > 0 {
		q += " OFFSET ?"
		args = append(args, query.Offset)
	}

	rows, err := bs.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query benchmark runs: %w", err)
	}
	defer rows.Close()

	return scanBenchmarkRuns(rows)
}

// CountRuns returns the total number of benchmark runs matching the supplied filter.
func (bs *BenchmarkStore) CountRuns(ctx context.Context, query store.BenchmarkQuery) (int, error) {
	var (
		conditions []string
		args       []interface{}
	)

	if query.AgentID != "" {
		conditions = append(conditions, "agent_id = ?")
		args = append(args, query.AgentID)
	}

	q := `SELECT COUNT(*) FROM benchmark_runs`
	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}

	var count int
	if err := bs.readDB.QueryRowContext(ctx, q, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count benchmark runs: %w", err)
	}
	return count, nil
}

// GetLatestRun returns the most recent benchmark run for the agent, or nil if none exists.
func (bs *BenchmarkStore) GetLatestRun(ctx context.Context, agentID string) (*store.BenchmarkRun, error) {
	const q = `SELECT id, run_at, window_days, agent_id, model,
		accuracy, avg_latency_ms, p50_latency_ms, p95_latency_ms, p99_latency_ms,
		tool_success_rate, roi_score, total_cost_usd, sample_size,
		verdict, recommended_model, decision_reason, artifact_path, avg_quality_score,
		run_kind, window_start, window_end
		FROM benchmark_runs
		WHERE agent_id = ?
		ORDER BY run_at DESC
		LIMIT 1`

	row := bs.readDB.QueryRowContext(ctx, q, agentID)
	run, err := scanBenchmarkRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest benchmark run for %q: %w", agentID, err)
	}
	return run, nil
}

// ListAgents returns the distinct agent IDs that have at least one benchmark run.
func (bs *BenchmarkStore) ListAgents(ctx context.Context) ([]string, error) {
	const q = `SELECT DISTINCT agent_id FROM benchmark_runs ORDER BY agent_id`

	rows, err := bs.readDB.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list benchmark agents: %w", err)
	}
	defer rows.Close()

	var agents []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return nil, fmt.Errorf("scan agent id: %w", err)
		}
		agents = append(agents, agentID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent rows: %w", err)
	}
	return agents, nil
}

// QueryModelSummaries returns one aggregated row per model across all benchmark runs.
// It reuses the existing benchmark run query path and applies the same weighting
// and verdict selection rules used by the benchmark summary view.
func (bs *BenchmarkStore) QueryModelSummaries(ctx context.Context) ([]store.BenchmarkModelSummary, error) {
	runs, err := bs.QueryRuns(ctx, store.BenchmarkQuery{Limit: MaxQueryLimit})
	if err != nil {
		return nil, fmt.Errorf("query benchmark model summaries: %w", err)
	}

	type agg struct {
		runs         int
		totalSamples int
		sumAccuracy  float64
		sumP95       float64
		totalCost    float64
		lastVerdict  store.VerdictType
		lastRunAt    time.Time
	}
	aggMap := make(map[string]*agg)

	for _, r := range runs {
		if r.RunAt.IsZero() {
			continue
		}
		a := aggMap[r.Model]
		if a == nil {
			a = &agg{}
			aggMap[r.Model] = a
		}

		isInsufficient := r.Verdict == store.VerdictInsufficientData || r.SampleSize < 50
		if !isInsufficient {
			samples := r.SampleSize
			if samples <= 0 {
				samples = 1
			}
			a.totalSamples += samples
			a.sumAccuracy += r.Accuracy * float64(samples)
			a.sumP95 += r.P95LatencyMs * float64(samples)
		}
		a.runs++
		a.totalCost += r.TotalCostUSD

		if r.RunAt.After(a.lastRunAt) {
			if !isInsufficient {
				a.lastRunAt = r.RunAt
				a.lastVerdict = r.Verdict
			} else if a.lastVerdict == "" || a.lastVerdict == store.VerdictInsufficientData {
				a.lastRunAt = r.RunAt
				a.lastVerdict = r.Verdict
			}
		}
	}

	rows := make([]store.BenchmarkModelSummary, 0, len(aggMap))
	for model, a := range aggMap {
		avgAcc := 0.0
		avgP95 := 0.0
		if a.totalSamples > 0 {
			avgAcc = a.sumAccuracy / float64(a.totalSamples)
			avgP95 = a.sumP95 / float64(a.totalSamples)
		}
		rows = append(rows, store.BenchmarkModelSummary{
			Model:        model,
			Runs:         a.runs,
			AvgAccuracy:  avgAcc,
			AvgP95Ms:     avgP95,
			TotalCostUSD: a.totalCost,
			LastVerdict:  a.lastVerdict,
			LastRunAt:    a.lastRunAt,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Model != rows[j].Model {
			return rows[i].Model < rows[j].Model
		}
		return rows[i].LastRunAt.After(rows[j].LastRunAt)
	})

	return rows, nil
}

// Checkpoint performs a WAL checkpoint to prevent unbounded WAL file growth.
// This should be called before Close during graceful shutdown.
func (bs *BenchmarkStore) Checkpoint() error {
	if bs.writeDB == nil {
		return nil
	}
	_, err := bs.writeDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// Close releases all database connections.
func (bs *BenchmarkStore) Close() error {
	var errs []string
	if bs.readDB != nil && bs.readDB != bs.writeDB {
		if err := bs.readDB.Close(); err != nil {
			errs = append(errs, "read db: "+err.Error())
		}
	}
	if bs.writeDB != nil {
		if err := bs.writeDB.Close(); err != nil {
			errs = append(errs, "write db: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close benchmark store: %s", strings.Join(errs, "; "))
	}
	return nil
}

// scanBenchmarkRuns reads rows from a query into a slice of BenchmarkRun structs.
func scanBenchmarkRuns(rows *sql.Rows) ([]store.BenchmarkRun, error) {
	var runs []store.BenchmarkRun
	for rows.Next() {
		var (
			runAtMs       int64
			verdict       string
			runKind       string
			windowStartMs int64
			windowEndMs   int64
			run           store.BenchmarkRun
		)
		err := rows.Scan(
			&run.ID,
			&runAtMs,
			&run.WindowDays,
			&run.AgentID,
			&run.Model,
			&run.Accuracy,
			&run.AvgLatencyMs,
			&run.P50LatencyMs,
			&run.P95LatencyMs,
			&run.P99LatencyMs,
			&run.ToolSuccessRate,
			&run.ROIScore,
			&run.TotalCostUSD,
			&run.SampleSize,
			&verdict,
			&run.RecommendedModel,
			&run.DecisionReason,
			&run.ArtifactPath,
			&run.AvgQualityScore,
			&runKind,
			&windowStartMs,
			&windowEndMs,
		)
		if err != nil {
			return nil, fmt.Errorf("scan benchmark run row: %w", err)
		}
		run.RunAt = time.UnixMilli(runAtMs).UTC()
		run.Verdict = store.VerdictType(verdict)
		run.RunKind = store.RunKindType(runKind)
		if run.RunKind == "" {
			run.RunKind = store.RunKindWeekly
		}
		run.WindowStart = time.UnixMilli(windowStartMs).UTC()
		run.WindowEnd = time.UnixMilli(windowEndMs).UTC()
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate benchmark run rows: %w", err)
	}
	return runs, nil
}

// rowScanner is a common interface for *sql.Row and *sql.Rows to allow reuse of scan logic.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// scanBenchmarkRun reads a single row into a BenchmarkRun.
func scanBenchmarkRun(row rowScanner) (*store.BenchmarkRun, error) {
	var (
		runAtMs       int64
		verdict       string
		runKind       string
		windowStartMs int64
		windowEndMs   int64
		run           store.BenchmarkRun
	)
	err := row.Scan(
		&run.ID,
		&runAtMs,
		&run.WindowDays,
		&run.AgentID,
		&run.Model,
		&run.Accuracy,
		&run.AvgLatencyMs,
		&run.P50LatencyMs,
		&run.P95LatencyMs,
		&run.P99LatencyMs,
		&run.ToolSuccessRate,
		&run.ROIScore,
		&run.TotalCostUSD,
		&run.SampleSize,
		&verdict,
		&run.RecommendedModel,
		&run.DecisionReason,
		&run.ArtifactPath,
		&run.AvgQualityScore,
		&runKind,
		&windowStartMs,
		&windowEndMs,
	)
	if err != nil {
		return nil, err
	}
	run.RunAt = time.UnixMilli(runAtMs).UTC()
	run.Verdict = store.VerdictType(verdict)
	run.RunKind = store.RunKindType(runKind)
	if run.RunKind == "" {
		run.RunKind = store.RunKindWeekly
	}
	run.WindowStart = time.UnixMilli(windowStartMs).UTC()
	run.WindowEnd = time.UnixMilli(windowEndMs).UTC()
	return &run, nil
}

// ListRunCycles returns the distinct week-start timestamps (Sunday 00:00 in loc, stored as UTC)
// for all benchmark runs, ordered newest first.
// limit=0 returns all; offset skips the first N cycle rows.
func (bs *BenchmarkStore) ListRunCycles(ctx context.Context, loc *time.Location, limit, offset int) ([]time.Time, error) {
	if loc == nil {
		loc = time.Local
	}

	// Pull all distinct run_at values (milliseconds UTC).
	// We compute week-start grouping in Go so we can use the caller's local timezone
	// (SQLite has no timezone support, and strftime('%w') is UTC-only).
	const q = `SELECT DISTINCT run_at FROM benchmark_runs ORDER BY run_at DESC`
	rows, err := bs.readDB.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list run_at for cycles: %w", err)
	}
	defer rows.Close()

	// Collect unique week-start times in loc.
	seen := make(map[time.Time]struct{})
	var ordered []time.Time // insertion order = newest first (since query is DESC)
	for rows.Next() {
		var ms int64
		if err := rows.Scan(&ms); err != nil {
			return nil, fmt.Errorf("scan run_at: %w", err)
		}
		t := time.UnixMilli(ms).In(loc)
		ws := weekStartInLoc(t)
		if _, ok := seen[ws]; !ok {
			seen[ws] = struct{}{}
			ordered = append(ordered, ws)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run_at rows: %w", err)
	}

	// Apply offset and limit.
	if offset >= len(ordered) {
		return nil, nil
	}
	ordered = ordered[offset:]
	if limit > 0 && limit < len(ordered) {
		ordered = ordered[:limit]
	}
	return ordered, nil
}

// weekStartInLoc returns midnight Sunday of the week containing t, in the same location as t.
// Sunday is weekday 0 in Go's time.Weekday().
func weekStartInLoc(t time.Time) time.Time {
	// Shift back to Sunday.
	daysBack := int(t.Weekday()) // Sunday=0, Monday=1, … Saturday=6
	d := t.AddDate(0, 0, -daysBack)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())
}

// QueryRunsInWindow returns all benchmark runs whose run_at falls within [since, until),
// ordered by run_at DESC.
func (bs *BenchmarkStore) QueryRunsInWindow(ctx context.Context, since, until time.Time) ([]store.BenchmarkRun, error) {
	const q = `SELECT id, run_at, window_days, agent_id, model,
		accuracy, avg_latency_ms, p50_latency_ms, p95_latency_ms, p99_latency_ms,
		tool_success_rate, roi_score, total_cost_usd, sample_size,
		verdict, recommended_model, decision_reason, artifact_path, avg_quality_score,
		run_kind, window_start, window_end
		FROM benchmark_runs
		WHERE run_at >= ? AND run_at < ?
		ORDER BY run_at DESC`

	rows, err := bs.readDB.QueryContext(ctx, q, since.UTC().UnixMilli(), until.UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("query runs in window: %w", err)
	}
	defer rows.Close()
	return scanBenchmarkRuns(rows)
}

// GetVerdictTrend returns the last N weekly verdicts for the given agent, ordered oldest first.
// Returns an empty slice if the agent has no runs or fewer than requested.
func (bs *BenchmarkStore) GetVerdictTrend(ctx context.Context, agentID string, weeks int) ([]string, error) {
	if weeks <= 0 {
		return nil, nil
	}
	// Fetch newest-first, then reverse for oldest-first order.
	const q = `SELECT verdict FROM benchmark_runs
		WHERE agent_id = ?
		ORDER BY run_at DESC
		LIMIT ?`

	rows, err := bs.readDB.QueryContext(ctx, q, agentID, weeks)
	if err != nil {
		return nil, fmt.Errorf("get verdict trend for %q: %w", agentID, err)
	}
	defer rows.Close()

	var verdicts []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan verdict: %w", err)
		}
		verdicts = append(verdicts, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate verdict rows: %w", err)
	}

	// Reverse to get oldest-first order.
	for i, j := 0, len(verdicts)-1; i < j; i, j = i+1, j-1 {
		verdicts[i], verdicts[j] = verdicts[j], verdicts[i]
	}
	return verdicts, nil
}
