package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cwatch/internal/hooks"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// DBFile is the database file name inside the state directory.
const DBFile = "cwatch.db"

// BusyTimeout bounds the time that a writer waits for the database lock.
const BusyTimeout = 3 * time.Second

// Retention limits for local history. Only the list and dashboard commands
// prune. The hook path never prunes.
const (
	EventRetention = 14 * 24 * time.Hour
	EndedRetention = 30 * 24 * time.Hour
	MaxEvents      = 50000
)

// ErrNotFound means that no instance matches an identifier.
var ErrNotFound = errors.New("instance not found")

// ErrAmbiguous means that an identifier prefix matches more than one instance.
var ErrAmbiguous = errors.New("instance identifier is ambiguous")

// Observation holds the process and terminal facts that the hook found for
// one event.
type Observation struct {
	OwnerPID        int
	OwnerStart      int64
	OwnerMethod     string
	TTY             string
	TerminalKind    string
	TerminalProgram string
	ITermSessionID  string
}

// OwnerKnown reports whether the observation identifies the owning process.
func (o Observation) OwnerKnown() bool { return o.OwnerPID > 0 && o.OwnerStart > 0 }

// Store is the SQLite state store.
type Store struct {
	db   *sql.DB
	Path string
}

// Open opens or creates the store in dir. It creates dir with mode 0700 and
// the database with mode 0600.
func Open(ctx context.Context, dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	path := filepath.Join(dir, DBFile)
	if f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600); err == nil {
		f.Close()
	}
	return openPath(ctx, path)
}

// OpenExisting opens the store only when the database file exists. It
// returns os.ErrNotExist when the file does not exist. It never creates
// files.
func OpenExisting(ctx context.Context, dir string) (*Store, error) {
	path := filepath.Join(dir, DBFile)
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return openPath(ctx, path)
}

func dsn(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", BusyTimeout.Milliseconds()))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_txlock", "immediate")
	return u.String() + "?" + q.Encode()
}

func openPath(ctx context.Context, path string) (*Store, error) {
	var s *Store
	err := retryBusy(ctx, func() error {
		db, err := sql.Open("sqlite", dsn(path))
		if err != nil {
			return err
		}
		db.SetMaxOpenConns(1)
		if err := (&Store{db: db}).migrate(ctx); err != nil {
			db.Close()
			return err
		}
		s = &Store{db: db, Path: path}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+suffix, 0o600)
	}
	return s, nil
}

// isBusy reports whether err is an SQLite lock error.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "SQLITE_LOCKED")
}

// retryBusy runs fn again while it fails with a lock error. SQLite can
// return a lock error without a wait, for example while concurrent
// processes create the database and switch it to WAL mode. A failed
// transaction commits nothing, so a retry is safe. The total wait is
// bounded by BusyTimeout and by ctx.
func retryBusy(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(BusyTimeout)
	delay := 2 * time.Millisecond
	for {
		err := fn()
		if !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		var b [1]byte
		_, _ = rand.Read(b[:])
		wait := delay + time.Duration(b[0])*delay/255
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
		if delay < 50*time.Millisecond {
			delay *= 2
		}
	}
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

const schemaV1 = `
CREATE TABLE IF NOT EXISTS instances (
	instance_id   TEXT PRIMARY KEY,
	session_id    TEXT NOT NULL,
	owner_pid     INTEGER NOT NULL DEFAULT 0,
	owner_start   INTEGER NOT NULL DEFAULT 0,
	tty           TEXT NOT NULL DEFAULT '',
	iterm_session TEXT NOT NULL DEFAULT '',
	state         TEXT NOT NULL,
	last_seq      INTEGER NOT NULL DEFAULT 0,
	updated_at    INTEGER NOT NULL,
	data          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS instances_session ON instances(session_id);
CREATE INDEX IF NOT EXISTS instances_owner ON instances(owner_pid, owner_start);
CREATE TABLE IF NOT EXISTS events (
	seq         INTEGER PRIMARY KEY AUTOINCREMENT,
	instance_id TEXT NOT NULL,
	received_at INTEGER NOT NULL,
	event       TEXT NOT NULL,
	detail      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_instance ON events(instance_id, seq);
CREATE INDEX IF NOT EXISTS events_received ON events(received_at);
`

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if v == SchemaVersion {
		return nil
	}
	if v > SchemaVersion {
		return fmt.Errorf("state schema version %d is newer than this cwatch (%d)", v, SchemaVersion)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Another process can finish the migration first. Read the version again
	// under the write lock.
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v == 0 {
		if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

func scanInstances(ctx context.Context, q querier, query string, args ...any) ([]Instance, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var in Instance
		if err := json.Unmarshal([]byte(data), &in); err != nil {
			// Skip a damaged row. Do not fail the whole listing.
			continue
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func sameTerminal(in Instance, obs Observation) bool {
	switch {
	case obs.ITermSessionID != "":
		return in.ITermSessionID == obs.ITermSessionID
	case obs.TTY != "":
		return in.ITermSessionID == "" && in.TTY == obs.TTY
	default:
		return in.ITermSessionID == "" && in.TTY == ""
	}
}

// selectInstance finds the instance for an event. It never merges two
// different owning processes, so the same session ID in two terminals keeps
// two rows.
func selectInstance(cands []Instance, obs Observation) (Instance, bool) {
	if obs.OwnerKnown() {
		for _, c := range cands {
			if c.OwnerPID == obs.OwnerPID && c.OwnerStart == obs.OwnerStart {
				return c, true
			}
		}
		// Adopt a provisional row from the same terminal.
		for _, c := range cands {
			if c.OwnerPID == 0 && c.State != Ended && sameTerminal(c, obs) {
				return c, true
			}
		}
		return Instance{}, false
	}
	// The owner is unknown. Attach to a live row from the same terminal.
	var best Instance
	found := false
	for _, c := range cands {
		if c.State != Ended && sameTerminal(c, obs) && (!found || c.LastSeq > best.LastSeq) {
			best, found = c, true
		}
	}
	return best, found
}

// Record stores one event and updates its instance in one transaction.
func (s *Store) Record(ctx context.Context, obs Observation, ev hooks.Event, storeExcerpts bool, now time.Time) (Instance, error) {
	var in Instance
	err := retryBusy(ctx, func() error {
		var err error
		in, err = s.record(ctx, obs, ev, storeExcerpts, now)
		return err
	})
	return in, err
}

func (s *Store) record(ctx context.Context, obs Observation, ev hooks.Event, storeExcerpts bool, now time.Time) (Instance, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Instance{}, err
	}
	defer tx.Rollback()

	cands, err := scanInstances(ctx, tx, "SELECT data FROM instances WHERE session_id = ?", ev.SessionID)
	if err != nil {
		return Instance{}, err
	}
	in, found := selectInstance(cands, obs)
	if !found {
		in = Instance{
			SchemaVersion: SchemaVersion,
			InstanceID:    newID(),
			SessionID:     ev.SessionID,
			CreatedAt:     now,
			Liveness:      Unknown,
		}
	}
	if obs.OwnerKnown() && in.OwnerPID == 0 {
		in.OwnerPID, in.OwnerStart, in.OwnerMethod = obs.OwnerPID, obs.OwnerStart, obs.OwnerMethod
	}
	if in.OwnerMethod == "" {
		in.OwnerMethod = obs.OwnerMethod
	}
	if obs.OwnerKnown() && in.OwnerPID == obs.OwnerPID {
		in.Liveness = Alive
	}
	if obs.TTY != "" {
		in.TTY = obs.TTY
	}
	if obs.ITermSessionID != "" {
		in.ITermSessionID = obs.ITermSessionID
	}
	if obs.TerminalKind != "" {
		in.TerminalKind = obs.TerminalKind
	}
	if obs.TerminalProgram != "" {
		in.TerminalProgram = obs.TerminalProgram
	}
	if in.TerminalKind == "" {
		in.TerminalKind = TerminalUnknown
	}

	detail, _ := json.Marshal(ev)
	res, err := tx.ExecContext(ctx,
		"INSERT INTO events (instance_id, received_at, event, detail) VALUES (?, ?, ?, ?)",
		in.InstanceID, now.UnixMicro(), ev.HookEventName, string(detail))
	if err != nil {
		return Instance{}, err
	}
	seq, _ := res.LastInsertId()

	in = Reduce(in, ev, Meta{Seq: seq, At: now, StoreExcerpts: storeExcerpts})
	if in.Project == "" && in.Cwd != "" {
		in.Project = filepath.Base(in.Cwd)
	}
	if err := upsert(ctx, tx, in); err != nil {
		return Instance{}, err
	}

	// One process runs one conversation at a time. When the process moves to
	// a new session (for example after /clear or /resume), end the rows of
	// its earlier sessions.
	if obs.OwnerKnown() && !ev.IsSubagent() && (ev.Kind == hooks.KindSessionStart || ev.Kind == hooks.KindPromptSubmit) {
		others, err := scanInstances(ctx, tx,
			"SELECT data FROM instances WHERE owner_pid = ? AND owner_start = ? AND session_id != ? AND state != ?",
			obs.OwnerPID, obs.OwnerStart, ev.SessionID, string(Ended))
		if err != nil {
			return Instance{}, err
		}
		for _, o := range others {
			o = markEnded(o, ReasonSuperseded, now)
			o.UpdatedAt = now
			if err := upsert(ctx, tx, o); err != nil {
				return Instance{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return Instance{}, err
	}
	return in, nil
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func upsert(ctx context.Context, x execer, in Instance) error {
	// The branch and the token usage are read-time values. Never store them.
	in.Branch, in.Tokens = "", nil
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	_, err = x.ExecContext(ctx, `
INSERT INTO instances (instance_id, session_id, owner_pid, owner_start, tty, iterm_session, state, last_seq, updated_at, data)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(instance_id) DO UPDATE SET
	session_id = excluded.session_id, owner_pid = excluded.owner_pid, owner_start = excluded.owner_start,
	tty = excluded.tty, iterm_session = excluded.iterm_session, state = excluded.state,
	last_seq = excluded.last_seq, updated_at = excluded.updated_at, data = excluded.data`,
		in.InstanceID, in.SessionID, in.OwnerPID, in.OwnerStart, in.TTY, in.ITermSessionID,
		string(in.State), in.LastSeq, in.UpdatedAt.UnixMicro(), string(data))
	return err
}

// List returns all stored instances, most recently updated first.
func (s *Store) List(ctx context.Context) ([]Instance, error) {
	return scanInstances(ctx, s.db, "SELECT data FROM instances ORDER BY updated_at DESC")
}

// Get returns the instance whose ID equals id or starts with id.
func (s *Store) Get(ctx context.Context, id string) (Instance, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "%_") {
		return Instance{}, ErrNotFound
	}
	list, err := scanInstances(ctx, s.db, "SELECT data FROM instances WHERE instance_id = ? OR instance_id LIKE ?", id, id+"%")
	if err != nil {
		return Instance{}, err
	}
	for _, in := range list {
		if in.InstanceID == id {
			return in, nil
		}
	}
	switch len(list) {
	case 0:
		return Instance{}, ErrNotFound
	case 1:
		return list[0], nil
	default:
		return Instance{}, ErrAmbiguous
	}
}

// MarkProcessExit ends an instance when its process is gone. The update
// applies only when no event changed the instance after the caller read it.
func (s *Store) MarkProcessExit(ctx context.Context, in Instance, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var seq int64
	var st string
	err = tx.QueryRowContext(ctx, "SELECT last_seq, state FROM instances WHERE instance_id = ?", in.InstanceID).Scan(&seq, &st)
	if err != nil {
		return false, err
	}
	if seq != in.LastSeq || st == string(Ended) {
		return false, nil
	}
	if err := upsert(ctx, tx, MarkProcessExit(in, now)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// EventRecord is one stored event.
type EventRecord struct {
	Seq        int64       `json:"seq"`
	InstanceID string      `json:"instance_id"`
	ReceivedAt time.Time   `json:"received_at"`
	Event      string      `json:"event"`
	Detail     hooks.Event `json:"detail"`
}

// Events returns the latest events of an instance, oldest first.
func (s *Store) Events(ctx context.Context, instanceID string, limit int) ([]EventRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT seq, instance_id, received_at, event, detail FROM events WHERE instance_id = ? ORDER BY seq DESC LIMIT ?",
		instanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRecord
	for rows.Next() {
		var r EventRecord
		var at int64
		var detail string
		if err := rows.Scan(&r.Seq, &r.InstanceID, &at, &r.Event, &detail); err != nil {
			return nil, err
		}
		r.ReceivedAt = time.UnixMicro(at)
		_ = json.Unmarshal([]byte(detail), &r.Detail)
		out = append(out, r)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// Stats summarizes the store for diagnostics.
type Stats struct {
	Instances   int
	Events      int
	LastEventAt time.Time
}

// Stats returns counts and the time of the latest event.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	var last sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM instances").Scan(&st.Instances); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*), MAX(received_at) FROM events").Scan(&st.Events, &last); err != nil {
		return st, err
	}
	if last.Valid {
		st.LastEventAt = time.UnixMicro(last.Int64)
	}
	return st, nil
}

// Prune deletes old events and old ended instances.
func (s *Store) Prune(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE received_at < ?", now.Add(-EventRetention).UnixMicro()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM events WHERE seq <= (SELECT MAX(seq) FROM events) - ?", MaxEvents); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM instances WHERE state = ? AND updated_at < ?", string(Ended), now.Add(-EndedRetention).UnixMicro()); err != nil {
		return err
	}
	return tx.Commit()
}
