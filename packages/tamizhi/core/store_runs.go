package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

func (s *Store) CreateRun(ctx context.Context, owner string, request c.RunRequest, parent string) (c.RunInfo, error) {
	run := c.RunInfo{ID: newID(), ConversationID: request.ConversationID, Status: "running", ParentID: parent, CreatedAt: now(), UpdatedAt: now()}
	data, _ := json.Marshal(run)
	body, _ := json.Marshal(request)
	_, err := s.db.ExecContext(ctx, s.query(`INSERT INTO runs(id,owner,conversation_id,status,request,data,last_sequence,created_at,updated_at) VALUES(?,?,?,?,?,?,0,?,?)`), run.ID, owner, run.ConversationID, run.Status, string(body), string(data), run.CreatedAt, run.UpdatedAt)
	return run, err
}
func (s *Store) Run(ctx context.Context, owner, id string) (c.RunInfo, c.RunRequest, error) {
	var data, body, status string
	var sequence int64
	err := s.db.QueryRowContext(ctx, s.query(`SELECT data,request,status,last_sequence FROM runs WHERE owner=? AND id=?`), owner, id).Scan(&data, &body, &status, &sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return c.RunInfo{}, c.RunRequest{}, ErrNotFound
	}
	var run c.RunInfo
	var request c.RunRequest
	if err == nil {
		err = json.Unmarshal([]byte(data), &run)
	}
	if err == nil {
		err = json.Unmarshal([]byte(body), &request)
	}
	run.Status = status
	run.LastSequence = sequence
	return run, request, err
}
func (s *Store) AppendRunEvent(ctx context.Context, owner string, event c.StreamEvent) (c.StreamEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return event, err
	}
	defer tx.Rollback()
	var seq int64
	err = tx.QueryRowContext(ctx, s.query(`SELECT last_sequence FROM runs WHERE owner=? AND id=?`), owner, event.RunID).Scan(&seq)
	if err != nil {
		return event, err
	}
	event.Sequence = seq + 1
	raw, err := json.Marshal(event)
	if err != nil {
		return event, err
	}
	_, err = tx.ExecContext(ctx, s.query(`INSERT INTO run_events(run_id,sequence,payload) VALUES(?,?,?)`), event.RunID, event.Sequence, string(raw))
	if err != nil {
		return event, err
	}
	_, err = tx.ExecContext(ctx, s.query(`UPDATE runs SET last_sequence=?,updated_at=? WHERE id=?`), event.Sequence, now(), event.RunID)
	if err != nil {
		return event, err
	}
	return event, tx.Commit()
}
func (s *Store) RunEvents(ctx context.Context, owner, id string, after int64) ([]c.StreamEvent, error) {
	if _, _, err := s.Run(ctx, owner, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, s.query(`SELECT payload FROM run_events WHERE run_id=? AND sequence>? ORDER BY sequence LIMIT 256`), id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []c.StreamEvent{}
	for rows.Next() {
		var raw string
		var event c.StreamEvent
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
func (s *Store) FinishRun(ctx context.Context, owner, id, status string, usage c.Usage) error {
	run, _, err := s.Run(ctx, owner, id)
	if err != nil {
		return err
	}
	run.Status = status
	run.Usage = usage
	run.UpdatedAt = now()
	raw, _ := json.Marshal(run)
	_, err = s.db.ExecContext(ctx, s.query(`UPDATE runs SET status=?,data=?,updated_at=? WHERE owner=? AND id=?`), status, string(raw), run.UpdatedAt, owner, id)
	return err
}
func (s *Store) RecoverRuns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT owner,id,conversation_id FROM runs WHERE status IN ('running','waiting','queued')`)
	if err != nil {
		return err
	}
	type record struct{ owner, id, conversation string }
	items := []record{}
	for rows.Next() {
		var item record
		if err = rows.Scan(&item.owner, &item.id, &item.conversation); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		if _, err = s.AppendRunEvent(ctx, item.owner, c.StreamEvent{Type: "error", RunID: item.id, ConversationID: item.conversation, Message: "The backend restarted. Review the partial response and retry; tool actions were not automatically repeated."}); err != nil {
			return err
		}
		if err = s.FinishRun(ctx, item.owner, item.id, "interrupted", c.Usage{}); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) BeginTool(ctx context.Context, run, key string) (json.RawMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var status, output string
	err := s.db.QueryRowContext(ctx, s.query(`SELECT status,output FROM tool_executions WHERE run_id=? AND call_key=?`), run, key).Scan(&status, &output)
	if err == nil {
		if status == "complete" {
			return json.RawMessage(output), true, nil
		}
		return nil, false, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	_, err = s.db.ExecContext(ctx, s.query(`INSERT INTO tool_executions(run_id,call_key,status,output) VALUES(?,?,'running','null')`), run, key)
	return nil, false, err
}
func (s *Store) EndTool(ctx context.Context, run, key string, output any) error {
	raw, err := json.Marshal(output)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.query(`UPDATE tool_executions SET status='complete',output=? WHERE run_id=? AND call_key=?`), string(raw), run, key)
	return err
}
func (s *Store) PutAttachment(ctx context.Context, owner string, item c.Attachment, content []byte) error {
	raw, _ := json.Marshal(item)
	_, err := s.db.ExecContext(ctx, s.query(`INSERT INTO attachments(owner,id,data,content) VALUES(?,?,?,?)`), owner, item.ID, string(raw), content)
	return err
}
func (s *Store) Attachment(ctx context.Context, owner, id string) (c.Attachment, []byte, error) {
	var raw string
	var data []byte
	err := s.db.QueryRowContext(ctx, s.query(`SELECT data,content FROM attachments WHERE owner=? AND id=?`), owner, id).Scan(&raw, &data)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	var item c.Attachment
	if err == nil {
		err = json.Unmarshal([]byte(raw), &item)
	}
	return item, data, err
}
func (s *Store) AddMemory(ctx context.Context, owner string, request c.MemoryRequest) (c.Memory, error) {
	item := c.Memory{ID: newID(), Text: request.Text, Tags: request.Tags, CreatedAt: now()}
	raw, _ := json.Marshal(item)
	_, err := s.db.ExecContext(ctx, s.query(`INSERT INTO memories(owner,id,text,data,created_at) VALUES(?,?,?,?,?)`), owner, item.ID, item.Text, string(raw), item.CreatedAt)
	return item, err
}
func (s *Store) Memories(ctx context.Context, owner, query string) ([]c.Memory, error) {
	rows, err := s.db.QueryContext(ctx, s.query(`SELECT data FROM memories WHERE owner=? AND LOWER(text) LIKE LOWER(?) ORDER BY created_at DESC LIMIT 30`), owner, "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []c.Memory{}
	for rows.Next() {
		var raw string
		var item c.Memory
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) DeleteMemory(ctx context.Context, owner, id string) error {
	_, err := s.db.ExecContext(ctx, s.query(`DELETE FROM memories WHERE owner=? AND id=?`), owner, id)
	return err
}
func (s *Store) Setting(ctx context.Context, owner, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, s.query(`SELECT value FROM settings WHERE owner=? AND key=?`), owner, key).Scan(&value)
	return value, err
}
func (s *Store) SetSetting(ctx context.Context, owner, key, value string) error {
	_, err := s.db.ExecContext(ctx, s.query(`INSERT INTO settings(owner,key,value) VALUES(?,?,?) ON CONFLICT(owner,key) DO UPDATE SET value=excluded.value`), owner, key, value)
	return err
}
