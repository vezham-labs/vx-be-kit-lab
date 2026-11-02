package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("concurrent update or active operation")
var ErrForbidden = errors.New("access denied")

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func now() int64 { return time.Now().UnixMilli() }

type Store struct {
	db      *sql.DB
	dialect string
	mu      sync.Mutex
}

func OpenStore(ctx context.Context, driver, dsn string) (*Store, error) {
	if driver == "" {
		driver = "sqlite"
	}
	if driver == "sqlite" && dsn != ":memory:" && !strings.HasPrefix(dsn, "file:") {
		if err := os.MkdirAll(filepath.Dir(dsn), 0700); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(dsn, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		file.Close()
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, dialect: driver}
	if driver == "sqlite" {
		db.SetMaxOpenConns(1)
		if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
			db.Close()
			return nil, err
		}
	} else {
		db.SetMaxOpenConns(16)
	}
	if err = s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) query(q string) string {
	if s.dialect != "pgx" {
		return q
	}
	i := 0
	var out strings.Builder
	for _, ch := range q {
		if ch == '?' {
			i++
			fmt.Fprintf(&out, "$%d", i)
		} else {
			out.WriteRune(ch)
		}
	}
	return out.String()
}
func (s *Store) migrate(ctx context.Context) error {
	sequence := "INTEGER PRIMARY KEY AUTOINCREMENT"
	blob := "BLOB"
	if s.dialect == "pgx" {
		sequence = "BIGSERIAL PRIMARY KEY"
		blob = "BYTEA"
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS conversations(owner TEXT NOT NULL,id TEXT NOT NULL,title TEXT NOT NULL,data TEXT NOT NULL,version BIGINT NOT NULL,updated_at BIGINT NOT NULL,deleted INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(owner,id))`,
		`CREATE TABLE IF NOT EXISTS session_events(id ` + sequence + `,owner TEXT NOT NULL,conversation_id TEXT NOT NULL,kind TEXT NOT NULL,payload TEXT NOT NULL,created_at BIGINT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS session_events_lookup ON session_events(owner,conversation_id,id)`,
		`CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,owner TEXT NOT NULL,conversation_id TEXT NOT NULL,status TEXT NOT NULL,request TEXT NOT NULL,data TEXT NOT NULL,last_sequence BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS run_events(run_id TEXT NOT NULL,sequence BIGINT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(run_id,sequence))`,
		`CREATE TABLE IF NOT EXISTS tool_executions(run_id TEXT NOT NULL,call_key TEXT NOT NULL,status TEXT NOT NULL,output TEXT NOT NULL,PRIMARY KEY(run_id,call_key))`,
		`CREATE TABLE IF NOT EXISTS checkpoints(owner TEXT NOT NULL,id TEXT NOT NULL,conversation_id TEXT NOT NULL,data TEXT NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(owner,id))`,
		`CREATE TABLE IF NOT EXISTS attachments(owner TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,content ` + blob + ` NOT NULL,PRIMARY KEY(owner,id))`,
		`CREATE TABLE IF NOT EXISTS memories(owner TEXT NOT NULL,id TEXT NOT NULL,text TEXT NOT NULL,data TEXT NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(owner,id))`,
		`CREATE TABLE IF NOT EXISTS auth_sessions(hash TEXT PRIMARY KEY,principal TEXT NOT NULL,expires_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS oauth_states(hash TEXT PRIMARY KEY,data TEXT NOT NULL,expires_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS settings(owner TEXT NOT NULL,key TEXT NOT NULL,value TEXT NOT NULL,PRIMARY KEY(owner,key))`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}
func (s *Store) Conversation(ctx context.Context, owner, id string) (c.Conversation, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, s.query(`SELECT data FROM conversations WHERE owner=? AND id=? AND deleted=0`), owner, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c.Conversation{}, ErrNotFound
	}
	var item c.Conversation
	if err == nil {
		err = json.Unmarshal([]byte(raw), &item)
	}
	return item, err
}
func (s *Store) SaveConversation(ctx context.Context, owner string, item c.Conversation, expected *int64, kind string) (c.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer tx.Rollback()
	var version int64
	var deleted int
	err = tx.QueryRowContext(ctx, s.query(`SELECT version,deleted FROM conversations WHERE owner=? AND id=?`), owner, item.ID).Scan(&version, &deleted)
	if err == nil && deleted != 0 {
		return item, ErrNotFound
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return item, err
	}
	if expected != nil && *expected != version {
		return item, ErrConflict
	}
	item.Version = version + 1
	item.UpdatedAt = now()
	if item.Messages == nil {
		item.Messages = []c.Message{}
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return item, err
	}
	var result sql.Result
	if version == 0 {
		result, err = tx.ExecContext(ctx, s.query(`INSERT INTO conversations(owner,id,title,data,version,updated_at,deleted) VALUES(?,?,?,?,?,?,0) ON CONFLICT(owner,id) DO NOTHING`), owner, item.ID, item.Title, string(raw), item.Version, item.UpdatedAt)
	} else {
		result, err = tx.ExecContext(ctx, s.query(`UPDATE conversations SET title=?,data=?,version=?,updated_at=? WHERE owner=? AND id=? AND version=? AND deleted=0`), item.Title, string(raw), item.Version, item.UpdatedAt, owner, item.ID, version)
	}
	if err != nil {
		return item, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return item, ErrConflict
	}
	_, err = tx.ExecContext(ctx, s.query(`INSERT INTO session_events(owner,conversation_id,kind,payload,created_at) VALUES(?,?,?,?,?)`), owner, item.ID, kind, string(raw), now())
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}
func (s *Store) Conversations(ctx context.Context, owner, search, cursor string, limit int) (c.ConversationPage, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	stamp := int64(0)
	position := ""
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return c.ConversationPage{}, errors.New("invalid cursor")
		}
		parts := strings.SplitN(string(raw), ":", 2)
		if len(parts) != 2 {
			return c.ConversationPage{}, errors.New("invalid cursor")
		}
		stamp, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return c.ConversationPage{}, err
		}
		position = parts[1]
	}
	rows, err := s.db.QueryContext(ctx, s.query(`SELECT data FROM conversations WHERE owner=? AND deleted=0 AND LOWER(title) LIKE LOWER(?) AND (CAST(? AS BIGINT)=0 OR updated_at<? OR (updated_at=? AND id<?)) ORDER BY updated_at DESC,id DESC LIMIT ?`), owner, "%"+search+"%", stamp, stamp, stamp, position, limit+1)
	if err != nil {
		return c.ConversationPage{}, err
	}
	defer rows.Close()
	page := c.ConversationPage{Items: []c.Conversation{}}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return page, err
		}
		var item c.Conversation
		if err = json.Unmarshal([]byte(raw), &item); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(last.UpdatedAt, 10) + ":" + last.ID))
	}
	return page, rows.Err()
}
func (s *Store) DeleteConversation(ctx context.Context, owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, s.query(`UPDATE conversations SET deleted=1,updated_at=? WHERE owner=? AND id=? AND deleted=0`), now(), owner, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	_, err = tx.ExecContext(ctx, s.query(`INSERT INTO session_events(owner,conversation_id,kind,payload,created_at) VALUES(?,?,?,?,?)`), owner, id, "conversation.deleted", "{}", now())
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) MutateConversation(ctx context.Context, owner, id, kind string, update func(*c.Conversation) error) (c.Conversation, error) {
	item, err := s.Conversation(ctx, owner, id)
	if err != nil {
		return item, err
	}
	expected := item.Version
	if err = update(&item); err != nil {
		return item, err
	}
	return s.SaveConversation(ctx, owner, item, &expected, kind)
}
func (s *Store) Branch(ctx context.Context, owner, id string, request c.BranchRequest) (c.Conversation, error) {
	original, err := s.Conversation(ctx, owner, id)
	if err != nil {
		return original, err
	}
	index := -1
	for i, m := range original.Messages {
		if m.ID == request.MessageID {
			index = i
			break
		}
	}
	if index < 0 {
		return original, ErrNotFound
	}
	title := request.Title
	if title == "" {
		title = "Branch: " + original.Title
	}
	if len(title) > 128 {
		title = title[:128]
	}
	branch := c.Conversation{ID: newID(), Title: title, Messages: append([]c.Message{}, original.Messages[:index+1]...), ParentID: original.ID, BranchFrom: request.MessageID}
	return s.SaveConversation(ctx, owner, branch, nil, "conversation.branched")
}
func (s *Store) Checkpoint(ctx context.Context, owner, id, label string) (c.Checkpoint, error) {
	item, err := s.Conversation(ctx, owner, id)
	if err != nil {
		return c.Checkpoint{}, err
	}
	checkpoint := c.Checkpoint{ID: newID(), ConversationID: id, Label: label, Summary: item.Summary, Messages: item.Messages, CreatedAt: now()}
	raw, _ := json.Marshal(checkpoint)
	_, err = s.db.ExecContext(ctx, s.query(`INSERT INTO checkpoints(owner,id,conversation_id,data,created_at) VALUES(?,?,?,?,?)`), owner, checkpoint.ID, id, string(raw), checkpoint.CreatedAt)
	return checkpoint, err
}
func (s *Store) Checkpoints(ctx context.Context, owner, id string) ([]c.Checkpoint, error) {
	rows, err := s.db.QueryContext(ctx, s.query(`SELECT data FROM checkpoints WHERE owner=? AND conversation_id=? ORDER BY created_at DESC`), owner, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []c.Checkpoint{}
	for rows.Next() {
		var raw string
		var item c.Checkpoint
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
func (s *Store) RestoreCheckpoint(ctx context.Context, owner, conversation, id string) (c.Conversation, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, s.query(`SELECT data FROM checkpoints WHERE owner=? AND id=?`), owner, id).Scan(&raw); err != nil {
		return c.Conversation{}, ErrNotFound
	}
	var checkpoint c.Checkpoint
	if err := json.Unmarshal([]byte(raw), &checkpoint); err != nil {
		return c.Conversation{}, err
	}
	if checkpoint.ConversationID != conversation {
		return c.Conversation{}, ErrNotFound
	}
	return s.MutateConversation(ctx, owner, checkpoint.ConversationID, "checkpoint.restored", func(item *c.Conversation) error {
		item.Messages = checkpoint.Messages
		item.Summary = checkpoint.Summary
		return nil
	})
}
