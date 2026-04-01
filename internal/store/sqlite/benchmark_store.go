package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kiosvantra/metronous/internal/store"
)

// benchmarkSchema defines the DDL for benchmark.db.
const benchmarkSchema = `
-- Core benchmark run table (one row per weekly run per agent)
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

// benchmarkMigrations contains ALTER TABLE statements to apply to existing databases.
// Each migration is guarded by checking for the column's existence first (SQLite
// returns an error on duplicate column add, which we ignore).
const addAvgQualityScoreColumn = `ALTER TABLE benchmark_runs ADD COLUMN avg_quality_score REAL NOT NULL DEFAULT 0.0`

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

// ApplyBenchmarkMigrations creates all tables and indexes for benchmark.db,
// then applies any additive column migrations for existing databases.
// It is idempotent and safe to call at startup.
func ApplyBenchmarkMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, benchmarkSchema); err != nil {
		return fmt.Errorf("apply benchmark schema: %w", err)
	}
	// Apply additive column migration — ignore "duplicate column name" errors from
	// databases that already have the column (e.g. newly created with the full schema).
	if _, err := db.ExecContext(ctx, addAvgQualityScoreColumn); err != nil {
		// SQLite returns "duplicate column name" as an error; this is expected for
		// fresh databases where the CREATE TABLE already includes the column.
		if !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("apply avg_quality_score migration: %w", err)
		}
	}
	return nil
}

// SaveRun persists a benchmark run. If run.ID is empty, a UUID is generated.
func (bs *BenchmarkStore) SaveRun(ctx context.Context, run store.BenchmarkRun) error {
	if run.ID == "" {
		run.ID = uuid.New().String()
	}

	const q = `
		INSERT INTO benchmark_runs (
			id, run_at, window_days, agent_id, model,
			accuracy, avg_latency_ms, p50_latency_ms, p95_latency_ms, p99_latency_ms,
			tool_success_rate, roi_score, total_cost_usd, sample_size,
			verdict, recommended_model, decision_reason, artifact_path, avg_quality_score
		) VALUES (
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?
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
		verdict, recommended_model, decision_reason, artifact_path, avg_quality_score
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
		verdict, recommended_model, decision_reason, artifact_path, avg_quality_score
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
		verdict, recommended_model, decision_reason, artifact_path, avg_quality_score
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
			runAtMs int64
			verdict string
			run     store.BenchmarkRun
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
		)
		if err != nil {
			return nil, fmt.Errorf("scan benchmark run row: %w", err)
		}
		run.RunAt = time.UnixMilli(runAtMs).UTC()
		run.Verdict = store.VerdictType(verdict)
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
		runAtMs int64
		verdict string
		run     store.BenchmarkRun
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
	)
	if err != nil {
		return nil, err
	}
	run.RunAt = time.UnixMilli(runAtMs).UTC()
	run.Verdict = store.VerdictType(verdict)
	return &run, nil
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
