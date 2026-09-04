// Package store is the relational side of shedit: items, attachments,
// extractions, moves, and relation proposals in a single SQLite file.
// Raw attachment bytes live on disk (see package blob); the store only keeps
// their paths and hashes.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

const (
	StatusOpen     = "open"
	StatusDone     = "done"
	StatusArchived = "archived"

	Pile2Min       = "2min"
	PileDeadline   = "deadline"
	PileEventually = "eventually"
	PileUnsorted   = "unsorted"

	ActorUser   = "user"
	ActorSystem = "system"

	KindText  = "text"
	KindFile  = "file"
	KindImage = "image"
	KindURL   = "url"
)

var Piles = []string{Pile2Min, PileDeadline, PileEventually, PileUnsorted}

func ValidPile(p string) bool {
	for _, x := range Piles {
		if x == p {
			return true
		}
	}
	return false
}

func validStatus(s string) bool {
	return s == StatusOpen || s == StatusDone || s == StatusArchived
}

type Item struct {
	ID              string
	Status          string
	Pile            string
	Reason          string
	TitleOverride   string
	SummaryOverride string
	Deadline        *time.Time
	AllDay          bool
	Estimate        string
	Tags            []string
	Error           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DoneAt          *time.Time
}

type Attachment struct {
	ID        string
	ItemID    string
	Kind      string
	Name      string
	Path      string
	SHA256    string
	Size      int64
	CreatedAt time.Time
}

type Extraction struct {
	ID        string
	ItemID    string
	Version   int
	Model     string
	JSON      string
	CreatedAt time.Time
}

type Move struct {
	ID        string
	ItemID    string
	From      string
	To        string
	Actor     string
	Reason    string
	CreatedAt time.Time
}

type Relation struct {
	ItemID    string
	OtherID   string
	Reason    string
	CreatedAt time.Time
}

type Sorting struct {
	Estimate string
	Tags     []string
}

type ItemUpdate struct {
	Title         *string
	Summary       *string
	Deadline      *time.Time
	AllDay        *bool
	ClearDeadline bool
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS items (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  pile TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  title_override TEXT NOT NULL DEFAULT '',
  summary_override TEXT NOT NULL DEFAULT '',
  deadline TEXT,
  all_day INTEGER NOT NULL DEFAULT 0,
  estimate TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '[]',
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  done_at TEXT
);
CREATE TABLE IF NOT EXISTS attachments (
  id TEXT PRIMARY KEY,
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS attachments_item ON attachments(item_id);
CREATE TABLE IF NOT EXISTS extractions (
  id TEXT PRIMARY KEY,
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  model TEXT NOT NULL,
  json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(item_id, version)
);
CREATE TABLE IF NOT EXISTS moves (
  id TEXT PRIMARY KEY,
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  from_pile TEXT NOT NULL,
  to_pile TEXT NOT NULL,
  actor TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS moves_item ON moves(item_id);
CREATE TABLE IF NOT EXISTS relations (
  item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  other_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  PRIMARY KEY(item_id, other_id)
);
`

// schemaVersion is stored in PRAGMA user_version so a future column change
// can be detected and migrated instead of silently mismatching.
const schemaVersion = 1

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection turns lock errors into queueing.
	db.SetMaxOpenConns(1)
	var have int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&have); err != nil {
		db.Close()
		return nil, err
	}
	if have > schemaVersion {
		db.Close()
		return nil, fmt.Errorf("database schema v%d is newer than this build (v%d)", have, schemaVersion)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion)); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func newID() string {
	var b [8]byte
	rand.Read(b[:])
	return fmt.Sprintf("%x%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

func now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// fmtLocal keeps the offset so the wall-clock date survives storage.
func fmtLocal(t time.Time) string { return t.Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func parseTimePtr(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t := parseTime(ns.String)
	return &t
}

func (s *Store) CreateItem() (Item, error) {
	it := Item{ID: newID(), Status: StatusOpen, Pile: PileUnsorted, Tags: []string{}, CreatedAt: now()}
	it.UpdatedAt = it.CreatedAt
	_, err := s.db.Exec(`INSERT INTO items (id,status,pile,created_at,updated_at) VALUES (?,?,?,?,?)`,
		it.ID, it.Status, it.Pile, fmtTime(it.CreatedAt), fmtTime(it.UpdatedAt))
	return it, err
}

const itemCols = `id,status,pile,reason,title_override,summary_override,deadline,all_day,estimate,tags,error,created_at,updated_at,done_at`

func scanItem(sc interface{ Scan(...any) error }) (Item, error) {
	var it Item
	var deadline, doneAt sql.NullString
	var tags, created, updated string
	err := sc.Scan(&it.ID, &it.Status, &it.Pile, &it.Reason, &it.TitleOverride, &it.SummaryOverride,
		&deadline, &it.AllDay, &it.Estimate, &tags, &it.Error, &created, &updated, &doneAt)
	if err != nil {
		return it, err
	}
	it.Deadline = parseTimePtr(deadline)
	it.DoneAt = parseTimePtr(doneAt)
	it.CreatedAt = parseTime(created)
	it.UpdatedAt = parseTime(updated)
	it.Tags = []string{}
	json.Unmarshal([]byte(tags), &it.Tags)
	return it, nil
}

func (s *Store) GetItem(id string) (Item, error) {
	it, err := scanItem(s.db.QueryRow(`SELECT `+itemCols+` FROM items WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

func (s *Store) ListItems(status string) ([]Item, error) {
	q := `SELECT ` + itemCols + ` FROM items`
	var args []any
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY rowid`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) touch(id string, set string, args ...any) error {
	args = append(args, fmtTime(now()), id)
	res, err := s.db.Exec(`UPDATE items SET `+set+`, updated_at=? WHERE id=?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetStatus(id, status string) error {
	if !validStatus(status) {
		return fmt.Errorf("invalid status %q", status)
	}
	if status == StatusDone {
		return s.touch(id, `status=?, done_at=?`, status, fmtTime(now()))
	}
	return s.touch(id, `status=?`, status)
}

func (s *Store) SetError(id, msg string) error { return s.touch(id, `error=?`, msg) }

func (s *Store) SetSorting(id string, so Sorting) error {
	if so.Tags == nil {
		so.Tags = []string{}
	}
	tags, _ := json.Marshal(so.Tags)
	return s.touch(id, `estimate=?, tags=?`, so.Estimate, string(tags))
}

func (s *Store) UpdateItem(id string, u ItemUpdate) error {
	var sets []string
	var args []any
	if u.Title != nil {
		sets = append(sets, "title_override=?")
		args = append(args, *u.Title)
	}
	if u.Summary != nil {
		sets = append(sets, "summary_override=?")
		args = append(args, *u.Summary)
	}
	if u.ClearDeadline {
		sets = append(sets, "deadline=NULL", "all_day=0")
	} else if u.Deadline != nil {
		sets = append(sets, "deadline=?")
		args = append(args, fmtLocal(*u.Deadline))
	}
	if u.AllDay != nil && !u.ClearDeadline {
		sets = append(sets, "all_day=?")
		args = append(args, *u.AllDay)
	}
	if len(sets) == 0 {
		_, err := s.GetItem(id)
		return err
	}
	return s.touch(id, strings.Join(sets, ", "), args...)
}

func (s *Store) DeleteItem(id string) error {
	res, err := s.db.Exec(`DELETE FROM items WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddAttachment(itemID string, a Attachment) (Attachment, error) {
	a.ID = newID()
	a.ItemID = itemID
	a.CreatedAt = now()
	_, err := s.db.Exec(`INSERT INTO attachments (id,item_id,kind,name,path,sha256,size,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		a.ID, a.ItemID, a.Kind, a.Name, a.Path, a.SHA256, a.Size, fmtTime(a.CreatedAt))
	return a, err
}

func (s *Store) GetAttachment(id string) (Attachment, error) {
	var a Attachment
	var created string
	err := s.db.QueryRow(`SELECT id,item_id,kind,name,path,sha256,size,created_at FROM attachments WHERE id=?`, id).
		Scan(&a.ID, &a.ItemID, &a.Kind, &a.Name, &a.Path, &a.SHA256, &a.Size, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.CreatedAt = parseTime(created)
	return a, err
}

func (s *Store) ListAttachments(itemID string) ([]Attachment, error) {
	rows, err := s.db.Query(`SELECT id,item_id,kind,name,path,sha256,size,created_at FROM attachments WHERE item_id=? ORDER BY rowid`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		var created string
		if err := rows.Scan(&a.ID, &a.ItemID, &a.Kind, &a.Name, &a.Path, &a.SHA256, &a.Size, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) RemoveAttachment(id string) error {
	res, err := s.db.Exec(`DELETE FROM attachments WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddExtraction(itemID string, e Extraction) (Extraction, error) {
	e.ID = newID()
	e.ItemID = itemID
	e.CreatedAt = now()
	// Version is assigned inside the INSERT so concurrent extractions of one
	// item cannot pick the same number.
	err := s.db.QueryRow(`INSERT INTO extractions (id,item_id,version,model,json,created_at)
		SELECT ?, ?, COALESCE(MAX(version),0)+1, ?, ?, ? FROM extractions WHERE item_id=?
		RETURNING version`,
		e.ID, e.ItemID, e.Model, e.JSON, fmtTime(e.CreatedAt), itemID).Scan(&e.Version)
	return e, err
}

func (s *Store) LatestExtraction(itemID string) (Extraction, error) {
	var e Extraction
	var created string
	err := s.db.QueryRow(`SELECT id,item_id,version,model,json,created_at FROM extractions WHERE item_id=? ORDER BY version DESC LIMIT 1`, itemID).
		Scan(&e.ID, &e.ItemID, &e.Version, &e.Model, &e.JSON, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	e.CreatedAt = parseTime(created)
	return e, err
}

func (s *Store) ListExtractions(itemID string) ([]Extraction, error) {
	rows, err := s.db.Query(`SELECT id,item_id,version,model,json,created_at FROM extractions WHERE item_id=? ORDER BY version`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Extraction
	for rows.Next() {
		var e Extraction
		var created string
		if err := rows.Scan(&e.ID, &e.ItemID, &e.Version, &e.Model, &e.JSON, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) Move(itemID, to, actor, reason string) (Move, error) {
	if !ValidPile(to) {
		return Move{}, fmt.Errorf("invalid pile %q", to)
	}
	if actor != ActorUser && actor != ActorSystem {
		return Move{}, fmt.Errorf("invalid actor %q", actor)
	}
	it, err := s.GetItem(itemID)
	if err != nil {
		return Move{}, err
	}
	m := Move{ID: newID(), ItemID: itemID, From: it.Pile, To: to, Actor: actor, Reason: reason, CreatedAt: now()}
	tx, err := s.db.Begin()
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO moves (id,item_id,from_pile,to_pile,actor,reason,created_at) VALUES (?,?,?,?,?,?,?)`,
		m.ID, m.ItemID, m.From, m.To, m.Actor, m.Reason, fmtTime(m.CreatedAt)); err != nil {
		return m, err
	}
	if _, err := tx.Exec(`UPDATE items SET pile=?, reason=?, updated_at=? WHERE id=?`, to, reason, fmtTime(now()), itemID); err != nil {
		return m, err
	}
	return m, tx.Commit()
}

func (s *Store) SetMoveReason(moveID, reason string) error {
	res, err := s.db.Exec(`UPDATE moves SET reason=? WHERE id=?`, reason, moveID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMoves(rows *sql.Rows) ([]Move, error) {
	defer rows.Close()
	var out []Move
	for rows.Next() {
		var m Move
		var created string
		if err := rows.Scan(&m.ID, &m.ItemID, &m.From, &m.To, &m.Actor, &m.Reason, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = parseTime(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListMoves(itemID string) ([]Move, error) {
	rows, err := s.db.Query(`SELECT id,item_id,from_pile,to_pile,actor,reason,created_at FROM moves WHERE item_id=? ORDER BY rowid`, itemID)
	if err != nil {
		return nil, err
	}
	return scanMoves(rows)
}

func (s *Store) RecentUserMoves(limit int) ([]Move, error) {
	rows, err := s.db.Query(`SELECT id,item_id,from_pile,to_pile,actor,reason,created_at FROM moves WHERE actor=? ORDER BY rowid DESC LIMIT ?`, ActorUser, limit)
	if err != nil {
		return nil, err
	}
	return scanMoves(rows)
}

func (s *Store) ProposeRelation(itemID, otherID, reason string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO relations (item_id,other_id,reason,created_at) VALUES (?,?,?,?)`,
		itemID, otherID, reason, fmtTime(now()))
	return err
}

func (s *Store) ListRelations(itemID string) ([]Relation, error) {
	rows, err := s.db.Query(`SELECT item_id,other_id,reason,created_at FROM relations WHERE item_id=? ORDER BY created_at`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		var r Relation
		var created string
		if err := rows.Scan(&r.ItemID, &r.OtherID, &r.Reason, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}
