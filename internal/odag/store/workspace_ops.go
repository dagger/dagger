package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type DerivedWorkspaceOpRecord struct {
	ID               string
	TraceID          string
	WorkspaceRoot    string
	SessionID        string
	ClientID         string
	SpanID           string
	Name             string
	Kind             string
	Direction        string
	CallName         string
	Path             string
	TargetType       string
	Status           string
	StatusCode       string
	StartUnixNano    int64
	EndUnixNano      int64
	ReceiverDagqlID  string
	OutputDagqlID    string
	PipelineClientID string
	PipelineID       string
	PipelineCommand  string
}

type DerivedWorkspaceOpQuery struct {
	TraceID       string
	SessionID     string
	ClientID      string
	WorkspaceRoot string
	FromUnixNano  int64
	ToUnixNano    int64
}

type DerivedWorkspaceTraceRecord struct {
	TraceID           string
	RefreshedUnixNano int64
	WorkspaceCount    int
	WorkspaceOpCount  int
}

func (s *Store) ReplaceDerivedWorkspaceProjection(
	ctx context.Context,
	traceID string,
	items []DerivedWorkspaceOpRecord,
	summary DerivedWorkspaceTraceRecord,
) error {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return fmt.Errorf("trace id is required")
	}
	if strings.TrimSpace(summary.TraceID) == "" {
		summary.TraceID = traceID
	}
	if summary.TraceID != traceID {
		return fmt.Errorf("derived workspace summary trace mismatch: %s != %s", summary.TraceID, traceID)
	}
	if summary.RefreshedUnixNano <= 0 {
		summary.RefreshedUnixNano = time.Now().UnixNano()
	}
	summary.WorkspaceOpCount = len(items)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM derived_workspace_ops WHERE trace_id = ?`, traceID); err != nil {
		return fmt.Errorf("delete derived workspace ops: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM derived_workspace_traces WHERE trace_id = ?`, traceID); err != nil {
		return fmt.Errorf("delete derived workspace trace: %w", err)
	}

	if len(items) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
INSERT INTO derived_workspace_ops (
  id,
  trace_id,
  workspace_root,
  session_id,
  client_id,
  span_id,
  name,
  kind,
  direction,
  call_name,
  path,
  target_type,
  status,
  status_code,
  start_unix_nano,
  end_unix_nano,
  receiver_dagql_id,
  output_dagql_id,
  pipeline_client_id,
  pipeline_id,
  pipeline_command
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)
		if err != nil {
			return fmt.Errorf("prepare insert derived workspace op: %w", err)
		}
		defer stmt.Close()

		for _, item := range items {
			if strings.TrimSpace(item.ID) == "" {
				return fmt.Errorf("derived workspace op has empty id")
			}
			if strings.TrimSpace(item.TraceID) == "" {
				item.TraceID = traceID
			}
			if item.TraceID != traceID {
				return fmt.Errorf("derived workspace op trace mismatch: %s != %s", item.TraceID, traceID)
			}
			if _, err := stmt.ExecContext(ctx,
				item.ID,
				item.TraceID,
				item.WorkspaceRoot,
				item.SessionID,
				item.ClientID,
				item.SpanID,
				item.Name,
				item.Kind,
				item.Direction,
				item.CallName,
				item.Path,
				item.TargetType,
				item.Status,
				item.StatusCode,
				item.StartUnixNano,
				item.EndUnixNano,
				item.ReceiverDagqlID,
				item.OutputDagqlID,
				item.PipelineClientID,
				item.PipelineID,
				item.PipelineCommand,
			); err != nil {
				return fmt.Errorf("insert derived workspace op %s: %w", item.ID, err)
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO derived_workspace_traces (
  trace_id,
  refreshed_unix_nano,
  workspace_count,
  workspace_op_count
)
VALUES (?, ?, ?, ?)
`, summary.TraceID, summary.RefreshedUnixNano, summary.WorkspaceCount, summary.WorkspaceOpCount); err != nil {
		return fmt.Errorf("insert derived workspace trace %s: %w", summary.TraceID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) ClearDerivedWorkspaceProjection(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM derived_workspace_ops`); err != nil {
		return fmt.Errorf("clear derived workspace ops: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM derived_workspace_traces`); err != nil {
		return fmt.Errorf("clear derived workspace traces: %w", err)
	}
	return nil
}

func (s *Store) ListDerivedWorkspaceOps(ctx context.Context, q DerivedWorkspaceOpQuery) ([]DerivedWorkspaceOpRecord, error) {
	args := make([]any, 0, 6)
	var where []string

	if traceID := strings.TrimSpace(q.TraceID); traceID != "" {
		where = append(where, "trace_id = ?")
		args = append(args, traceID)
	}
	if sessionID := strings.TrimSpace(q.SessionID); sessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, sessionID)
	}
	if clientID := strings.TrimSpace(q.ClientID); clientID != "" {
		where = append(where, "client_id = ?")
		args = append(args, clientID)
	}
	if workspaceRoot := strings.TrimSpace(q.WorkspaceRoot); workspaceRoot != "" {
		where = append(where, "workspace_root = ?")
		args = append(args, workspaceRoot)
	}
	if q.FromUnixNano > 0 {
		where = append(where, "(CASE WHEN end_unix_nano > 0 THEN end_unix_nano ELSE start_unix_nano END) >= ?")
		args = append(args, q.FromUnixNano)
	}
	if q.ToUnixNano > 0 {
		where = append(where, "start_unix_nano <= ?")
		args = append(args, q.ToUnixNano)
	}

	query := `
SELECT
  id,
  trace_id,
  workspace_root,
  session_id,
  client_id,
  span_id,
  name,
  kind,
  direction,
  call_name,
  path,
  target_type,
  status,
  status_code,
  start_unix_nano,
  end_unix_nano,
  receiver_dagql_id,
  output_dagql_id,
  pipeline_client_id,
  pipeline_id,
  pipeline_command
FROM derived_workspace_ops
`
	if len(where) > 0 {
		query += "WHERE " + strings.Join(where, " AND ") + "\n"
	}
	query += "ORDER BY start_unix_nano ASC, id ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query derived workspace ops: %w", err)
	}
	defer rows.Close()

	items := make([]DerivedWorkspaceOpRecord, 0)
	for rows.Next() {
		var item DerivedWorkspaceOpRecord
		if err := rows.Scan(
			&item.ID,
			&item.TraceID,
			&item.WorkspaceRoot,
			&item.SessionID,
			&item.ClientID,
			&item.SpanID,
			&item.Name,
			&item.Kind,
			&item.Direction,
			&item.CallName,
			&item.Path,
			&item.TargetType,
			&item.Status,
			&item.StatusCode,
			&item.StartUnixNano,
			&item.EndUnixNano,
			&item.ReceiverDagqlID,
			&item.OutputDagqlID,
			&item.PipelineClientID,
			&item.PipelineID,
			&item.PipelineCommand,
		); err != nil {
			return nil, fmt.Errorf("scan derived workspace op: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate derived workspace ops: %w", err)
	}

	return items, nil
}

func (s *Store) GetDerivedWorkspaceTrace(ctx context.Context, traceID string) (DerivedWorkspaceTraceRecord, error) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return DerivedWorkspaceTraceRecord{}, fmt.Errorf("trace id is required")
	}
	var rec DerivedWorkspaceTraceRecord
	if err := s.db.QueryRowContext(ctx, `
SELECT
  trace_id,
  refreshed_unix_nano,
  workspace_count,
  workspace_op_count
FROM derived_workspace_traces
WHERE trace_id = ?
`, traceID).Scan(
		&rec.TraceID,
		&rec.RefreshedUnixNano,
		&rec.WorkspaceCount,
		&rec.WorkspaceOpCount,
	); err != nil {
		if err == sql.ErrNoRows {
			return DerivedWorkspaceTraceRecord{}, ErrNotFound
		}
		return DerivedWorkspaceTraceRecord{}, fmt.Errorf("query derived workspace trace %s: %w", traceID, err)
	}
	return rec, nil
}

func (s *Store) MissingDerivedWorkspaceTraceIDs(ctx context.Context, traceIDs []string) ([]string, error) {
	normalized := make([]string, 0, len(traceIDs))
	seen := make(map[string]struct{}, len(traceIDs))
	for _, raw := range traceIDs {
		traceID := strings.TrimSpace(raw)
		if traceID == "" {
			continue
		}
		if _, ok := seen[traceID]; ok {
			continue
		}
		seen[traceID] = struct{}{}
		normalized = append(normalized, traceID)
	}
	if len(normalized) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(normalized)), ",")
	args := make([]any, 0, len(normalized))
	for _, traceID := range normalized {
		args = append(args, traceID)
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT trace_id
FROM derived_workspace_traces
WHERE trace_id IN (`+placeholders+`)
`, args...)
	if err != nil {
		return nil, fmt.Errorf("query derived workspace traces: %w", err)
	}
	defer rows.Close()

	found := make(map[string]struct{}, len(normalized))
	for rows.Next() {
		var traceID string
		if err := rows.Scan(&traceID); err != nil {
			return nil, fmt.Errorf("scan derived workspace trace id: %w", err)
		}
		found[traceID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate derived workspace traces: %w", err)
	}

	missing := make([]string, 0, len(normalized))
	for _, traceID := range normalized {
		if _, ok := found[traceID]; ok {
			continue
		}
		missing = append(missing, traceID)
	}
	return missing, nil
}
