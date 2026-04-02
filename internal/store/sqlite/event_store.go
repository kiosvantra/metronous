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

// EventStore is a SQLite-backed implementation of store.EventStore.
// Writes are intended to flow through a single-writer EventQueue; reads
// use a separate connection pool (WAL mode supports concurrent readers).
type EventStore struct {
	writeDB *sql.DB
	readDB  *sql.DB
	path    string
}

// Compile-time interface check.
var _ store.EventStore = (*EventStore)(nil)

// NewEventStore opens (or creates) the SQLite database at path, applies WAL
// pragmas, and runs schema migrations. Returns a ready-to-use EventStore.
func NewEventStore(path string) (*EventStore, error) {
	writeDB, err := openDB(path)
	if err != nil {
		return nil, fmt.Errorf("open write connection: %w", err)
	}

	// Apply WAL pragmas on the write connection.
	if err := applyPragmas(writeDB); err != nil {
		_ = writeDB.Close()
		return nil, err
	}

	// Apply schema migrations.
	if err := ApplyTrackingMigrations(context.Background(), writeDB); err != nil {
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
			return nil, fmt.Errorf("open read connection: %w", err)
		}
	}

	return &EventStore{
		writeDB: writeDB,
		readDB:  readDB,
		path:    path,
	}, nil
}

// InsertEvent persists a single event. If event.ID is empty, a new UUID is generated.
// Returns the persisted event ID.
func (es *EventStore) InsertEvent(ctx context.Context, event store.Event) (string, error) {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	// Serialize metadata to JSON.
	metaJSON := store.MetadataToJSON(event.Metadata)

	const q = `
		INSERT INTO events (
			id, agent_id, session_id, event_type, model, timestamp,
			duration_ms, prompt_tokens, completion_tokens,
			cost_usd, quality_score, rework_count,
			tool_name, tool_success, metadata
		) VALUES (
			?, ?, ?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?, ?
		)`

	var toolSuccessInt *int
	if event.ToolSuccess != nil {
		v := 0
		if *event.ToolSuccess {
			v = 1
		}
		toolSuccessInt = &v
	}

	// Use a single transaction for both event insert and summary upsert to ensure consistency.
	tx, txErr := es.writeDB.BeginTx(ctx, nil)
	if txErr != nil {
		return "", fmt.Errorf("start transaction: %w", txErr)
	}

	_, err := tx.ExecContext(ctx, q,
		event.ID,
		event.AgentID,
		event.SessionID,
		event.EventType,
		event.Model,
		event.Timestamp.UTC().UnixMilli(),
		event.DurationMs,
		event.PromptTokens,
		event.CompletionTokens,
		event.CostUSD,
		event.QualityScore,
		event.ReworkCount,
		event.ToolName,
		toolSuccessInt,
		nullableString(metaJSON),
	)
	if err != nil {
		_ = tx.Rollback()
		return "", fmt.Errorf("insert event: %w", err)
	}

	if err := es.upsertAgentSummaryTx(ctx, tx, event); err != nil {
		_ = tx.Rollback()
		return "", fmt.Errorf("upsert agent summary: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit transaction: %w", err)
	}

	return event.ID, nil
}

// upsertAgentSummaryTx maintains the materialized summary cache for an agent using the provided transaction.
func (es *EventStore) upsertAgentSummaryTx(ctx context.Context, tx *sql.Tx, event store.Event) error {
	// The running average formula uses the OLD total_events value (before +1).
	// In SQLite ON CONFLICT DO UPDATE SET, unqualified column references use
	// the existing row's values (pre-update), not the new values being set.
	//
	// Correct formula: new_avg = (old_avg * old_count + new_quality) / (old_count + 1)
	const q = `
		INSERT INTO agent_summaries (agent_id, last_event_ts, total_events, total_cost_usd, avg_quality, updated_at)
		VALUES (?, ?, 1, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			last_event_ts  = MAX(last_event_ts, excluded.last_event_ts),
			total_events   = total_events + 1,
			total_cost_usd = total_cost_usd + excluded.total_cost_usd,
			avg_quality    = (avg_quality * total_events + excluded.avg_quality) / (total_events + 1),
			updated_at     = excluded.updated_at
	`

	// Only update cost from complete events — cost_usd is cumulative per session,
	// so we maintain total_cost_usd as the sum of MAX(cost_usd) per session.
	// When the same session emits multiple complete events, cost_usd is
	// cumulative, so we only add the incremental increase in that max.
	costDeltaUSD := 0.0
	if event.EventType == "complete" && event.CostUSD != nil {
		// newMax includes the just-inserted event.
		const qNew = `
			SELECT COALESCE(MAX(cost_usd), 0)
			FROM events
			WHERE session_id = ?
				AND event_type = 'complete'
				AND cost_usd IS NOT NULL
		`
		var newMax float64
		if err := tx.QueryRowContext(ctx, qNew, event.SessionID).Scan(&newMax); err != nil {
			return fmt.Errorf("compute new max cost_usd: %w", err)
		}

		// oldMax excludes the just-inserted event.
		const qOld = `
			SELECT COALESCE(MAX(cost_usd), 0)
			FROM events
			WHERE session_id = ?
				AND event_type = 'complete'
				AND cost_usd IS NOT NULL
				AND id <> ?
		`
		var oldMax float64
		if err := tx.QueryRowContext(ctx, qOld, event.SessionID, event.ID).Scan(&oldMax); err != nil {
			return fmt.Errorf("compute old max cost_usd: %w", err)
		}

		delta := newMax - oldMax
		if delta > 0 {
			costDeltaUSD = delta
		}
	}
	qualityScore := 0.0
	if event.QualityScore != nil {
		qualityScore = *event.QualityScore
	}
	now := time.Now().UTC().UnixMilli()

	_, err := tx.ExecContext(ctx, q,
		event.AgentID,
		event.Timestamp.UTC().UnixMilli(),
		costDeltaUSD,
		qualityScore,
		now,
	)
	return err
}

// QueryEvents retrieves events matching the supplied filter criteria.
func (es *EventStore) QueryEvents(ctx context.Context, query store.EventQuery) ([]store.Event, error) {
	var (
		conditions []string
		args       []interface{}
	)

	if query.AgentID != "" {
		conditions = append(conditions, "agent_id = ?")
		args = append(args, query.AgentID)
	}
	if query.SessionID != "" {
		conditions = append(conditions, "session_id = ?")
		args = append(args, query.SessionID)
	}
	if query.EventType != "" {
		conditions = append(conditions, "event_type = ?")
		args = append(args, query.EventType)
	}
	if !query.Since.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, query.Since.UTC().UnixMilli())
	}
	if !query.Until.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, query.Until.UTC().UnixMilli())
	}

	q := "SELECT id, agent_id, session_id, event_type, model, timestamp, duration_ms, prompt_tokens, completion_tokens, cost_usd, quality_score, rework_count, tool_name, tool_success, metadata FROM events"
	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}
	q += " ORDER BY timestamp DESC"
	if query.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, query.Limit)
		if query.Offset > 0 {
			q += " OFFSET ?"
			args = append(args, query.Offset)
		}
	}

	rows, err := es.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	return scanEvents(rows)
}

// CountEvents returns the total number of events matching the supplied filter criteria.
// It is used by the Tracking TUI for absolute navigation (Home/End).
func (es *EventStore) CountEvents(ctx context.Context, query store.EventQuery) (int, error) {
	var (
		conditions []string
		args       []interface{}
	)

	if query.AgentID != "" {
		conditions = append(conditions, "agent_id = ?")
		args = append(args, query.AgentID)
	}
	if query.SessionID != "" {
		conditions = append(conditions, "session_id = ?")
		args = append(args, query.SessionID)
	}
	if query.EventType != "" {
		conditions = append(conditions, "event_type = ?")
		args = append(args, query.EventType)
	}
	if !query.Since.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, query.Since.UTC().UnixMilli())
	}
	if !query.Until.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, query.Until.UTC().UnixMilli())
	}

	q := "SELECT COUNT(*) FROM events"
	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}

	row := es.readDB.QueryRowContext(ctx, q, args...)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count events: %w", err)
	}
	return count, nil
}

// QuerySessions returns a page of SessionSummary rows, one per distinct session_id.
// Sessions are ordered by their most recent event timestamp DESC.
// Each summary is populated from the session's "complete" event when present,
// falling back to the latest event otherwise.
func (es *EventStore) QuerySessions(ctx context.Context, query store.SessionQuery) ([]store.SessionSummary, error) {
	// Build optional agent filter.
	var (
		conditions []string
		args       []interface{}
	)
	if query.AgentID != "" {
		conditions = append(conditions, "agent_id = ?")
		args = append(args, query.AgentID)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	// Strategy: for each session, pick the "complete" event if it exists,
	// otherwise the event with the latest timestamp.
	// We use a subquery that ranks events within each session:
	//   rank=1 for the complete event (priority=0), else latest by timestamp (priority=1).
	//
	// COALESCE approach: join the latest event per session with the complete event per session.
	// Simpler CTE-based approach that works on SQLite 3.25+.
	q := `
		WITH session_events AS (
			SELECT
				session_id,
				agent_id,
				model,
				timestamp,
				duration_ms,
				prompt_tokens,
				completion_tokens,
				cost_usd,
				event_type,
				ROW_NUMBER() OVER (
					PARTITION BY session_id
					ORDER BY
						CASE WHEN event_type = 'complete' THEN 0 ELSE 1 END,
						timestamp DESC
				) AS rn,
				MAX(timestamp) OVER (PARTITION BY session_id) AS max_ts
			FROM events` + whereClause + `
		)
			SELECT session_id, agent_id, model, timestamp, prompt_tokens, completion_tokens, cost_usd, duration_ms
			FROM session_events
			WHERE rn = 1
			ORDER BY max_ts DESC`

	if query.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, query.Limit)
		if query.Offset > 0 {
			q += " OFFSET ?"
			args = append(args, query.Offset)
		}
	}

	rows, err := es.readDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var summaries []store.SessionSummary
	for rows.Next() {
		var (
			s           store.SessionSummary
			timestampMs int64
		)
		if err := rows.Scan(
			&s.SessionID,
			&s.AgentID,
			&s.Model,
			&timestampMs,
			&s.PromptTokens,
			&s.CompletionTokens,
			&s.CostUSD,
			&s.DurationMs,
		); err != nil {
			return nil, fmt.Errorf("scan session row: %w", err)
		}
		s.Timestamp = time.UnixMilli(timestampMs).UTC()
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session rows: %w", err)
	}
	return summaries, nil
}

// GetSessionEvents returns all events for the given session_id, ordered
// by timestamp ASC (chronological order for the expand view).
func (es *EventStore) GetSessionEvents(ctx context.Context, sessionID string) ([]store.Event, error) {
	const q = `SELECT id, agent_id, session_id, event_type, model, timestamp,
		duration_ms, prompt_tokens, completion_tokens, cost_usd, quality_score,
		rework_count, tool_name, tool_success, metadata
		FROM events WHERE session_id = ? ORDER BY timestamp ASC`

	rows, err := es.readDB.QueryContext(ctx, q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session events: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// GetAgentEvents returns all events for a specific agent since the given time.
func (es *EventStore) GetAgentEvents(ctx context.Context, agentID string, since time.Time) ([]store.Event, error) {
	return es.QueryEvents(ctx, store.EventQuery{
		AgentID: agentID,
		Since:   since,
	})
}

// GetAgentSummary returns aggregated metrics for the specified agent.
func (es *EventStore) GetAgentSummary(ctx context.Context, agentID string) (store.AgentSummary, error) {
	const q = `
		SELECT agent_id, last_event_ts, total_events, total_cost_usd, avg_quality
		FROM agent_summaries
		WHERE agent_id = ?
	`
	row := es.readDB.QueryRowContext(ctx, q, agentID)

	var (
		summary     store.AgentSummary
		lastEventMs int64
	)
	err := row.Scan(
		&summary.AgentID,
		&lastEventMs,
		&summary.TotalEvents,
		&summary.TotalCostUSD,
		&summary.AvgQuality,
	)
	if err == sql.ErrNoRows {
		return store.AgentSummary{AgentID: agentID}, nil
	}
	if err != nil {
		return store.AgentSummary{}, fmt.Errorf("get agent summary %q: %w", agentID, err)
	}

	summary.LastEventTs = time.UnixMilli(lastEventMs).UTC()
	return summary, nil
}

// QueryDailyCostByModel aggregates total cost (USD) per model per local-day
// for events where event_type='complete'.
//
// The window is treated as [since, until) in UTC instants, while the resulting
// Day buckets are computed in the database using SQLite localtime.
func (es *EventStore) QueryDailyCostByModel(ctx context.Context, since, until time.Time) ([]store.DailyCostByModelRow, error) {
	const q = `
		SELECT
			strftime('%Y-%m-%d', datetime(timestamp/1000, 'unixepoch', 'localtime')) AS day,
			model,
			COALESCE(SUM(cost_usd), 0) AS total_cost_usd
		FROM events
		WHERE event_type = 'complete'
			AND timestamp >= ?
			AND timestamp < ?
			AND cost_usd IS NOT NULL
		GROUP BY day, model
		ORDER BY day ASC, model ASC
	`

	rows, err := es.readDB.QueryContext(ctx, q, since.UTC().UnixMilli(), until.UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("query daily cost by model: %w", err)
	}
	defer rows.Close()

	var out []store.DailyCostByModelRow
	for rows.Next() {
		var (
			dayStr string
			model  string
			total  float64
		)
		if err := rows.Scan(&dayStr, &model, &total); err != nil {
			return nil, fmt.Errorf("scan daily cost row: %w", err)
		}
		d, err := time.ParseInLocation("2006-01-02", dayStr, time.Local)
		if err != nil {
			return nil, fmt.Errorf("parse day %q: %w", dayStr, err)
		}
		out = append(out, store.DailyCostByModelRow{
			Day:          d,
			Model:        model,
			TotalCostUSD: total,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily cost rows: %w", err)
	}
	return out, nil
}

// Checkpoint performs a WAL checkpoint to prevent unbounded WAL file growth.
// This should be called before Close during graceful shutdown.
func (es *EventStore) Checkpoint() error {
	if es.writeDB == nil {
		return nil
	}
	_, err := es.writeDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// Close releases all database connections.
func (es *EventStore) Close() error {
	var errs []string
	if es.readDB != nil && es.readDB != es.writeDB {
		if err := es.readDB.Close(); err != nil {
			errs = append(errs, "read db: "+err.Error())
		}
	}
	if es.writeDB != nil {
		if err := es.writeDB.Close(); err != nil {
			errs = append(errs, "write db: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close event store: %s", strings.Join(errs, "; "))
	}
	return nil
}

// scanEvents reads rows from a query into a slice of Event structs.
func scanEvents(rows *sql.Rows) ([]store.Event, error) {
	var events []store.Event

	for rows.Next() {
		var (
			e              store.Event
			timestampMs    int64
			toolSuccessInt *int
			metaJSON       sql.NullString
		)

		err := rows.Scan(
			&e.ID,
			&e.AgentID,
			&e.SessionID,
			&e.EventType,
			&e.Model,
			&timestampMs,
			&e.DurationMs,
			&e.PromptTokens,
			&e.CompletionTokens,
			&e.CostUSD,
			&e.QualityScore,
			&e.ReworkCount,
			&e.ToolName,
			&toolSuccessInt,
			&metaJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("scan event row: %w", err)
		}

		e.Timestamp = time.UnixMilli(timestampMs).UTC()

		if toolSuccessInt != nil {
			v := *toolSuccessInt != 0
			e.ToolSuccess = &v
		}

		if metaJSON.Valid && metaJSON.String != "" {
			e.Metadata = store.MetadataFromJSON(metaJSON.String)
		}

		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event rows: %w", err)
	}

	return events, nil
}

// nullableString returns nil for empty strings (maps "" to NULL in SQLite).
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
