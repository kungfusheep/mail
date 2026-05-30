package cache

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kungfusheep/mail/provider"
	_ "github.com/mattn/go-sqlite3"
)

type Cache struct {
	db   *sql.DB
	subs pubsub

	// draftsLabel is the IMAP folder ID for the user's Drafts folder. Set
	// once at startup by the app (cache is storage-only, it doesn't know
	// about folder semantics). Draft-mutating methods publish on this
	// label so the mailbox subscriber can refresh the UI — without this,
	// the drafts table would update silently and the view would stay stale.
	draftsLabel string
}

// NewDraftID generates a stable local identifier for a draft. Unlike IMAP
// UIDs (which rotate on every APPEND/EXPUNGE), this value is written once
// and survives every server round-trip for the life of the draft.
func NewDraftID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "draft-" + hex.EncodeToString(b), nil
}

// SetDraftsLabel tells the cache which folder id to publish on when the
// drafts table is mutated. Should be called once at startup, after folders
// have been loaded.
func (c *Cache) SetDraftsLabel(label string) {
	c.draftsLabel = label
}

func (c *Cache) publishDrafts() {
	if c.draftsLabel != "" {
		c.subs.publish(c.draftsLabel)
	}
}

func New() (*Cache, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".config", "mail")
	return NewAt(filepath.Join(dir, "cache.db"))
}

func NewAt(path string) (*Cache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	c := &Cache{db: db}
	if err := c.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration: %w", err)
	}
	return c, nil
}

func NewMemory() (*Cache, error) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, err
	}
	// :memory: gives each connection its own isolated DB — with a pool,
	// writers and readers can end up on different connections and see
	// different state. Pin to one connection so in-process tests match
	// the file-backed behaviour the production cache relies on.
	db.SetMaxOpenConns(1)
	c := &Cache{db: db}
	if err := c.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration: %w", err)
	}
	return c, nil
}

func (c *Cache) Close() error {
	return c.db.Close()
}

func (c *Cache) migrate() error {
	_, err := c.db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL,
			data TEXT NOT NULL,
			date INTEGER NOT NULL,
			read INTEGER NOT NULL DEFAULT 0,
			starred INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_id);
		CREATE INDEX IF NOT EXISTS idx_messages_date ON messages(date);

		CREATE TABLE IF NOT EXISTS threads (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL,
			date INTEGER NOT NULL,
			unread INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_threads_date ON threads(date);

		CREATE TABLE IF NOT EXISTS thread_labels (
			thread_id TEXT NOT NULL,
			label TEXT NOT NULL,
			PRIMARY KEY (thread_id, label)
		);
		CREATE INDEX IF NOT EXISTS idx_thread_labels_label ON thread_labels(label);

		CREATE TABLE IF NOT EXISTS message_labels (
			message_id TEXT NOT NULL,
			label TEXT NOT NULL,
			PRIMARY KEY (message_id, label)
		);
		CREATE INDEX IF NOT EXISTS idx_message_labels_label ON message_labels(label);

		CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS sync_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS commands (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			target_id TEXT NOT NULL,
			params TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending',
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			synced_at INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_commands_status ON commands(status);

		CREATE TABLE IF NOT EXISTS sent_messages (
			message_id TEXT PRIMARY KEY,
			data TEXT NOT NULL,
			date INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS conversation_messages (
			conversation_key TEXT NOT NULL,
			message_key TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT '',
			thread_id TEXT NOT NULL DEFAULT '',
			data TEXT NOT NULL,
			date INTEGER NOT NULL,
			PRIMARY KEY (conversation_key, message_key, source)
		);
		CREATE INDEX IF NOT EXISTS idx_conversation_messages_key_date ON conversation_messages(conversation_key, date);

		CREATE TABLE IF NOT EXISTS contacts (
			email TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			last_contacted INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS sender_identities (
			domain TEXT PRIMARY KEY,
			display_name TEXT NOT NULL DEFAULT '',
			icon_url TEXT NOT NULL DEFAULT '',
			theme_color TEXT NOT NULL DEFAULT '',
			bimi_logo_url TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			confidence INTEGER NOT NULL DEFAULT 0,
			color_checked_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS journal (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			action TEXT NOT NULL,
			target_id TEXT NOT NULL,
			folder_from TEXT NOT NULL DEFAULT '',
			folder_to TEXT NOT NULL DEFAULT '',
			params TEXT NOT NULL DEFAULT '{}',
			result TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS drafts (
			thread_id TEXT PRIMARY KEY,
			to_addrs TEXT NOT NULL DEFAULT '',
			cc_addrs TEXT NOT NULL DEFAULT '',
			bcc_addrs TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			remote_uid TEXT NOT NULL DEFAULT '',
			synced_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS snoozes (
			thread_id TEXT PRIMARY KEY,
			original_folder TEXT NOT NULL,
			snoozed_folder TEXT NOT NULL,
			wake_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_snoozes_wake ON snoozes(wake_at);

		CREATE TABLE IF NOT EXISTS rules (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
	`)
	if err != nil {
		return err
	}

	c.db.Exec(`ALTER TABLE sender_identities ADD COLUMN color_checked_at INTEGER NOT NULL DEFAULT 0`)
	c.db.Exec(`ALTER TABLE contacts ADD COLUMN last_contacted INTEGER NOT NULL DEFAULT 0`)

	// one-shot migration: existing installs have a `folder` column on threads
	// and messages — lift those values into the new label join tables. errors
	// here mean the column doesn't exist (fresh install), so we swallow them.
	c.db.Exec(`INSERT OR IGNORE INTO thread_labels (thread_id, label) SELECT id, folder FROM threads WHERE folder != ''`)
	c.db.Exec(`INSERT OR IGNORE INTO message_labels (message_id, label) SELECT id, folder FROM messages WHERE folder != ''`)

	// one-shot migration: earlier drafts keyed by raw IMAP UID are now
	// unreachable — the drafts folder projects from this table using stable
	// local ids, and UID-keyed rows both collide with server state and can't
	// be re-found after APPEND/EXPUNGE rotates the UID. Purge them; server
	// state will be re-adopted with stable ids on next sync.
	c.db.Exec(`DELETE FROM drafts WHERE thread_id GLOB '[0-9]*'`)

	// one-shot migration: pre-fix versions of SeedDraft stamped adopted
	// rows with updated_at = time.Now() instead of the server message
	// date, so the Drafts folder showed every draft as "authored now"
	// and out of order. Re-adopt by clearing server-sourced rows
	// (remote_uid set); local-only drafts (never synced) are preserved
	// so any in-flight user edit isn't lost. Gated by sync_state so it
	// only runs once per cache.
	var resetDone string
	_ = c.db.QueryRow(`SELECT value FROM sync_state WHERE key = 'drafts_v2_reset'`).Scan(&resetDone)
	if resetDone == "" {
		c.db.Exec(`DELETE FROM drafts WHERE remote_uid != ''`)
		c.db.Exec(`INSERT OR REPLACE INTO sync_state (key, value) VALUES ('drafts_v2_reset', '1')`)
	}
	return nil
}

// commands

type Command struct {
	ID        string
	Action    string
	TargetID  string
	Params    map[string]string
	Status    string // pending, syncing, synced, failed
	Error     string
	CreatedAt time.Time
}

type Snooze struct {
	ThreadID       string
	OriginalFolder string
	SnoozedFolder  string
	WakeAt         time.Time
	CreatedAt      time.Time
}

func (c *Cache) PutCommand(cmd Command) error {
	params, err := json.Marshal(cmd.Params)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(
		"INSERT OR REPLACE INTO commands (id, action, target_id, params, status, error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		cmd.ID, cmd.Action, cmd.TargetID, string(params), cmd.Status, cmd.Error, cmd.CreatedAt.Unix(),
	)
	return err
}

func (c *Cache) PendingCommands() ([]Command, error) {
	rows, err := c.db.Query("SELECT id, action, target_id, params, status, error, created_at FROM commands WHERE status = 'pending' ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCommands(rows)
}

func (c *Cache) UpdateCommandStatus(id, status, errMsg string) error {
	syncedAt := int64(0)
	if status == "synced" {
		syncedAt = time.Now().Unix()
	}
	_, err := c.db.Exec(
		"UPDATE commands SET status = ?, error = ?, synced_at = ? WHERE id = ?",
		status, errMsg, syncedAt, id,
	)
	return err
}

func (c *Cache) DeleteCommand(id string) error {
	_, err := c.db.Exec("DELETE FROM commands WHERE id = ?", id)
	return err
}

func (c *Cache) DeleteCommands(ids ...string) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec("DELETE FROM commands WHERE id = ?", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Cache) ClearSyncedCommands() error {
	_, err := c.db.Exec("DELETE FROM commands WHERE status = 'synced'")
	return err
}

func (c *Cache) PutSnooze(s Snooze) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	_, err := c.db.Exec(
		`INSERT OR REPLACE INTO snoozes
		 (thread_id, original_folder, snoozed_folder, wake_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		s.ThreadID, s.OriginalFolder, s.SnoozedFolder, s.WakeAt.Unix(), s.CreatedAt.Unix(),
	)
	return err
}

func (c *Cache) DueSnoozes(now time.Time) ([]Snooze, error) {
	rows, err := c.db.Query(
		`SELECT thread_id, original_folder, snoozed_folder, wake_at, created_at
		 FROM snoozes
		 WHERE wake_at <= ?
		 ORDER BY wake_at, created_at`,
		now.Unix(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snooze
	for rows.Next() {
		var s Snooze
		var wakeAt, createdAt int64
		if err := rows.Scan(&s.ThreadID, &s.OriginalFolder, &s.SnoozedFolder, &wakeAt, &createdAt); err != nil {
			return nil, err
		}
		s.WakeAt = time.Unix(wakeAt, 0)
		s.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (c *Cache) DeleteSnooze(threadID string) error {
	_, err := c.db.Exec("DELETE FROM snoozes WHERE thread_id = ?", threadID)
	return err
}

func (c *Cache) Snooze(threadID string) (Snooze, bool, error) {
	var s Snooze
	var wakeAt, createdAt int64
	err := c.db.QueryRow(
		`SELECT thread_id, original_folder, snoozed_folder, wake_at, created_at
		 FROM snoozes
		 WHERE thread_id = ?`,
		threadID,
	).Scan(&s.ThreadID, &s.OriginalFolder, &s.SnoozedFolder, &wakeAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Snooze{}, false, nil
	}
	if err != nil {
		return Snooze{}, false, err
	}
	s.WakeAt = time.Unix(wakeAt, 0)
	s.CreatedAt = time.Unix(createdAt, 0)
	return s, true, nil
}

// journal

func (c *Cache) LogWrite(action, targetID, folderFrom, folderTo, result, errMsg string) {
	c.db.Exec(
		"INSERT INTO journal (action, target_id, folder_from, folder_to, result, error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		action, targetID, folderFrom, folderTo, result, errMsg, time.Now().Unix(),
	)
}

func scanCommands(rows *sql.Rows) ([]Command, error) {
	var cmds []Command
	for rows.Next() {
		var cmd Command
		var params string
		var createdAt int64
		if err := rows.Scan(&cmd.ID, &cmd.Action, &cmd.TargetID, &params, &cmd.Status, &cmd.Error, &createdAt); err != nil {
			return nil, err
		}
		cmd.CreatedAt = time.Unix(createdAt, 0)
		cmd.Params = make(map[string]string)
		json.Unmarshal([]byte(params), &cmd.Params)
		cmds = append(cmds, cmd)
	}
	return cmds, rows.Err()
}

// contacts

func (c *Cache) PutContacts(contacts []provider.Address) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO contacts (email, name, updated_at, last_contacted)
		VALUES (?, ?, ?, 0)
		ON CONFLICT(email) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE contacts.name END,
			updated_at = excluded.updated_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, contact := range contacts {
		contact.Email = normalizedContactEmail(contact.Email)
		if contact.Email == "" {
			continue
		}
		if _, err := stmt.Exec(contact.Email, strings.TrimSpace(contact.Name), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Cache) SearchContacts(query string) ([]provider.Address, error) {
	pattern := "%" + query + "%"
	rows, err := c.db.Query(
		`SELECT name, email
		FROM contacts
		WHERE name LIKE ? OR email LIKE ?
		ORDER BY last_contacted DESC, name COLLATE NOCASE, email COLLATE NOCASE
		LIMIT 10`,
		pattern, pattern,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []provider.Address
	for rows.Next() {
		var a provider.Address
		if err := rows.Scan(&a.Name, &a.Email); err != nil {
			return nil, err
		}
		results = append(results, a)
	}
	return results, rows.Err()
}

type contactCandidate struct {
	provider.Address
	LastContacted int64
}

func (c *Cache) RebuildContactIndex() error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE contacts SET last_contacted = 0"); err != nil {
		return err
	}

	var candidates []contactCandidate
	threadRows, err := tx.Query("SELECT data FROM threads")
	if err != nil {
		return err
	}
	for threadRows.Next() {
		var data string
		if err := threadRows.Scan(&data); err != nil {
			threadRows.Close()
			return err
		}
		var thread provider.Thread
		if err := json.Unmarshal([]byte(data), &thread); err == nil {
			candidates = append(candidates, contactCandidatesFromThread(thread)...)
		}
	}
	if err := threadRows.Close(); err != nil {
		return err
	}
	if err := threadRows.Err(); err != nil {
		return err
	}

	messageRows, err := tx.Query("SELECT data FROM messages")
	if err != nil {
		return err
	}
	for messageRows.Next() {
		var data string
		if err := messageRows.Scan(&data); err != nil {
			messageRows.Close()
			return err
		}
		var msg provider.Message
		if err := json.Unmarshal([]byte(data), &msg); err == nil {
			candidates = append(candidates, contactCandidatesFromMessage(msg)...)
		}
	}
	if err := messageRows.Close(); err != nil {
		return err
	}
	if err := messageRows.Err(); err != nil {
		return err
	}

	sentRows, err := tx.Query("SELECT data FROM sent_messages")
	if err != nil {
		return err
	}
	for sentRows.Next() {
		var data string
		if err := sentRows.Scan(&data); err != nil {
			sentRows.Close()
			return err
		}
		var msg provider.Message
		if err := json.Unmarshal([]byte(data), &msg); err == nil {
			candidates = append(candidates, contactCandidatesFromMessage(msg)...)
		}
	}
	if err := sentRows.Close(); err != nil {
		return err
	}
	if err := sentRows.Err(); err != nil {
		return err
	}

	draftRows, err := tx.Query("SELECT to_addrs, cc_addrs, bcc_addrs, updated_at FROM drafts")
	if err != nil {
		return err
	}
	for draftRows.Next() {
		var to, cc, bcc string
		var updatedAt int64
		if err := draftRows.Scan(&to, &cc, &bcc, &updatedAt); err != nil {
			draftRows.Close()
			return err
		}
		candidates = append(candidates, contactCandidatesFromAddresses(provider.ParseAddressList(to), updatedAt)...)
		candidates = append(candidates, contactCandidatesFromAddresses(provider.ParseAddressList(cc), updatedAt)...)
		candidates = append(candidates, contactCandidatesFromAddresses(provider.ParseAddressList(bcc), updatedAt)...)
	}
	if err := draftRows.Close(); err != nil {
		return err
	}
	if err := draftRows.Err(); err != nil {
		return err
	}

	if err := upsertContactCandidatesTx(tx, candidates); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Cache) upsertContactCandidates(candidates []contactCandidate) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertContactCandidatesTx(tx, candidates); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertContactCandidatesTx(tx *sql.Tx, candidates []contactCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`
		INSERT INTO contacts (email, name, updated_at, last_contacted)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE contacts.name END,
			updated_at = excluded.updated_at,
			last_contacted = MAX(contacts.last_contacted, excluded.last_contacted)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, candidate := range candidates {
		email := normalizedContactEmail(candidate.Email)
		if email == "" {
			continue
		}
		name := strings.TrimSpace(candidate.Name)
		if _, err := stmt.Exec(email, name, now, candidate.LastContacted); err != nil {
			return err
		}
	}
	return nil
}

func contactCandidatesFromThread(thread provider.Thread) []contactCandidate {
	var out []contactCandidate
	for _, msg := range thread.Messages {
		out = append(out, contactCandidatesFromMessage(msg)...)
	}
	return out
}

func contactCandidatesFromMessage(msg provider.Message) []contactCandidate {
	seenAt := msg.Date.Unix()
	if msg.Date.IsZero() {
		seenAt = time.Now().Unix()
	}
	var out []contactCandidate
	out = append(out, contactCandidatesFromAddresses([]provider.Address{msg.From}, seenAt)...)
	out = append(out, contactCandidatesFromAddresses(msg.To, seenAt)...)
	out = append(out, contactCandidatesFromAddresses(msg.CC, seenAt)...)
	out = append(out, contactCandidatesFromAddresses(msg.BCC, seenAt)...)
	return out
}

func contactCandidatesFromDraft(d Draft, seenAt int64) []contactCandidate {
	var out []contactCandidate
	out = append(out, contactCandidatesFromAddresses(provider.ParseAddressList(d.To), seenAt)...)
	out = append(out, contactCandidatesFromAddresses(provider.ParseAddressList(d.Cc), seenAt)...)
	out = append(out, contactCandidatesFromAddresses(provider.ParseAddressList(d.Bcc), seenAt)...)
	return out
}

func contactCandidatesFromAddresses(addrs []provider.Address, seenAt int64) []contactCandidate {
	out := make([]contactCandidate, 0, len(addrs))
	for _, addr := range addrs {
		addr.Email = normalizedContactEmail(addr.Email)
		addr.Name = strings.TrimSpace(addr.Name)
		if addr.Email == "" {
			continue
		}
		out = append(out, contactCandidate{Address: addr, LastContacted: seenAt})
	}
	return out
}

func normalizedContactEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// drafts — auto-saved compose state keyed by thread id (empty = new compose).
// A draft is considered empty if subject+body are whitespace-only, in which
// case PutDraft silently deletes rather than stores. Recipients alone don't
// count — a draft addressed to nobody with no content is still empty.

type Draft struct {
	ThreadID  string
	To        string
	Cc        string
	Bcc       string
	Subject   string
	Body      string
	RemoteUID string // UID in the server-side Drafts folder; "" until first sync
	UpdatedAt time.Time
}

func (d Draft) IsEmpty() bool {
	return strings.TrimSpace(d.Subject) == "" && strings.TrimSpace(d.Body) == ""
}

// PutDraft upserts the draft for its ThreadID. If the draft is empty (nothing
// meaningful to save) it silently deletes instead — no point keeping whitespace.
// A non-empty save also queues a sync_draft command so a subsequent
// ProcessPendingCommands flushes it to the server-side Drafts folder.
// Command IDs are stable per-thread so rapid re-saves coalesce to one.
//
// Uses ON CONFLICT so remote_uid and synced_at are preserved across edits —
// we only want to clobber those after a successful server sync.
func (c *Cache) PutDraft(d Draft) error {
	if d.IsEmpty() {
		return c.DeleteDraft(d.ThreadID)
	}
	if _, err := c.db.Exec(
		`INSERT INTO drafts
		 (thread_id, to_addrs, cc_addrs, bcc_addrs, subject, body, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(thread_id) DO UPDATE SET
		   to_addrs = excluded.to_addrs,
		   cc_addrs = excluded.cc_addrs,
		   bcc_addrs = excluded.bcc_addrs,
		   subject = excluded.subject,
		   body = excluded.body,
		   updated_at = excluded.updated_at`,
		d.ThreadID, d.To, d.Cc, d.Bcc, d.Subject, d.Body, time.Now().Unix(),
	); err != nil {
		return err
	}
	if err := c.upsertContactCandidates(contactCandidatesFromDraft(d, time.Now().Unix())); err != nil {
		return err
	}
	c.publishDrafts()
	return c.PutCommand(Command{
		ID:        "sync_draft-" + d.ThreadID,
		Action:    "sync_draft",
		TargetID:  d.ThreadID,
		Status:    "pending",
		CreatedAt: time.Now(),
	})
}

// SeedDraft creates a cache draft row mirroring a server-side draft we just
// pulled in (e.g. when the user clicks a thread in the Drafts folder). The
// row is inserted only if no local row exists — if the user already has
// unsynced local edits for this thread id, those take precedence. Does NOT
// queue a sync command because the local state matches the server.
func (c *Cache) SeedDraft(d Draft) error {
	// The draft's UpdatedAt is the server message's date — preserve it so
	// the Drafts folder view can order by real message age. Falling back
	// to time.Now() only for genuinely-new local drafts that have no
	// server-provided timestamp yet (the provider.Message zero-value).
	updatedAt := d.UpdatedAt.Unix()
	if d.UpdatedAt.IsZero() {
		updatedAt = time.Now().Unix()
	}
	res, err := c.db.Exec(
		`INSERT OR IGNORE INTO drafts
		 (thread_id, to_addrs, cc_addrs, bcc_addrs, subject, body, remote_uid, synced_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ThreadID, d.To, d.Cc, d.Bcc, d.Subject, d.Body,
		d.RemoteUID, time.Now().Unix(), updatedAt,
	)
	if err != nil {
		return err
	}
	// Only publish if we actually inserted — OR IGNORE turns a conflicting
	// insert into a no-op, and signalling a no-op spams the subscriber.
	if n, _ := res.RowsAffected(); n > 0 {
		if err := c.upsertContactCandidates(contactCandidatesFromDraft(d, updatedAt)); err != nil {
			return err
		}
		c.publishDrafts()
	}
	return nil
}

func (c *Cache) GetDraft(threadID string) (Draft, bool, error) {
	var d Draft
	var updatedAt int64
	err := c.db.QueryRow(
		`SELECT thread_id, to_addrs, cc_addrs, bcc_addrs, subject, body, remote_uid, updated_at
		 FROM drafts WHERE thread_id = ?`, threadID,
	).Scan(&d.ThreadID, &d.To, &d.Cc, &d.Bcc, &d.Subject, &d.Body, &d.RemoteUID, &updatedAt)
	if err == sql.ErrNoRows {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, err
	}
	d.UpdatedAt = time.Unix(updatedAt, 0)
	return d, true, nil
}

// DeleteDraft removes the draft row. If the row had a remote_uid, queues a
// delete_draft command so ProcessPendingCommands can expunge the server-side
// copy too.
func (c *Cache) DeleteDraft(threadID string) error {
	// capture remote_uid before we delete so we can schedule the server
	// expunge — errors (e.g. row doesn't exist) are fine, we just don't
	// queue a command.
	var remoteUID string
	_ = c.db.QueryRow("SELECT remote_uid FROM drafts WHERE thread_id = ?", threadID).Scan(&remoteUID)

	res, err := c.db.Exec("DELETE FROM drafts WHERE thread_id = ?", threadID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.publishDrafts()
	}
	// drop any pending sync for this thread — it's moot now
	_ = c.DeleteCommand("sync_draft-" + threadID)

	if remoteUID != "" {
		return c.PutCommand(Command{
			ID:        "delete_draft-" + remoteUID,
			Action:    "delete_draft",
			TargetID:  remoteUID,
			Status:    "pending",
			CreatedAt: time.Now(),
		})
	}
	return nil
}

// UpdateDraftRemoteUID records the server UID assigned to a synced draft.
// Also bumps synced_at so subsequent sync cycles can skip this row until it
// changes again.
func (c *Cache) UpdateDraftRemoteUID(threadID, remoteUID string) error {
	_, err := c.db.Exec(
		"UPDATE drafts SET remote_uid = ?, synced_at = ? WHERE thread_id = ?",
		remoteUID, time.Now().Unix(), threadID,
	)
	return err
}

// BackfillDraftContent updates the local draft row with content pulled
// from the server (reconciliation path). Unlike PutDraft this does NOT
// queue a sync command — the server IS the truth here, we're just
// mirroring it locally. Used to fill in body/recipients on drafts that
// were previously adopted with only headers. Publishes so the UI picks
// up the now-visible body.
//
// Also rewrites updated_at to the server's message date so the Drafts
// folder view orders by real age rather than "when we last touched the
// row." Zero date is treated as "leave it alone".
func (c *Cache) BackfillDraftContent(threadID, to, cc, bcc, subject, body string, date time.Time) error {
	var res sql.Result
	var err error
	if date.IsZero() {
		res, err = c.db.Exec(
			`UPDATE drafts
			 SET to_addrs = ?, cc_addrs = ?, bcc_addrs = ?, subject = ?, body = ?
			 WHERE thread_id = ?`,
			to, cc, bcc, subject, body, threadID,
		)
	} else {
		res, err = c.db.Exec(
			`UPDATE drafts
			 SET to_addrs = ?, cc_addrs = ?, bcc_addrs = ?, subject = ?, body = ?, updated_at = ?
			 WHERE thread_id = ?`,
			to, cc, bcc, subject, body, date.Unix(), threadID,
		)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.publishDrafts()
	}
	return nil
}

// HasDraft returns true if a draft exists for this thread. Cheaper than
// GetDraft when the caller only needs the boolean (e.g. thread-list indicator).
func (c *Cache) HasDraft(threadID string) bool {
	var one int
	err := c.db.QueryRow("SELECT 1 FROM drafts WHERE thread_id = ? LIMIT 1", threadID).Scan(&one)
	return err == nil
}

// GCEmptyDrafts deletes any draft rows whose subject and body are blank after
// trimming. Safety net for leftover rows from older writes.
func (c *Cache) GCEmptyDrafts() error {
	_, err := c.db.Exec(
		"DELETE FROM drafts WHERE TRIM(subject) = '' AND TRIM(body) = ''",
	)
	return err
}

// ListDrafts returns every draft row ordered by most-recently-updated first.
// The Drafts folder view projects directly from this, so every save is
// immediately reflected without needing to round-trip through the threads
// table or IMAP.
func (c *Cache) ListDrafts() ([]Draft, error) {
	rows, err := c.db.Query(
		`SELECT thread_id, to_addrs, cc_addrs, bcc_addrs, subject, body, remote_uid, updated_at
		 FROM drafts ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Draft
	for rows.Next() {
		var d Draft
		var updatedAt int64
		if err := rows.Scan(&d.ThreadID, &d.To, &d.Cc, &d.Bcc, &d.Subject, &d.Body, &d.RemoteUID, &updatedAt); err != nil {
			return nil, err
		}
		d.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// FindDraftByRemoteUID looks up a draft by its current server-side UID.
// Used during Drafts-folder sync to tell "server draft we already have a
// local row for" from "server draft we've never seen and must adopt".
func (c *Cache) FindDraftByRemoteUID(remoteUID string) (Draft, bool, error) {
	if remoteUID == "" {
		return Draft{}, false, nil
	}
	var d Draft
	var updatedAt int64
	err := c.db.QueryRow(
		`SELECT thread_id, to_addrs, cc_addrs, bcc_addrs, subject, body, remote_uid, updated_at
		 FROM drafts WHERE remote_uid = ?`, remoteUID,
	).Scan(&d.ThreadID, &d.To, &d.Cc, &d.Bcc, &d.Subject, &d.Body, &d.RemoteUID, &updatedAt)
	if err == sql.ErrNoRows {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, err
	}
	d.UpdatedAt = time.Unix(updatedAt, 0)
	return d, true, nil
}

// DraftRemoteUIDs returns the set of remote_uid values currently held by
// local draft rows. Sync uses this to detect local rows whose server copy
// has gone away (deleted in another client) so they can be pruned.
func (c *Cache) DraftRemoteUIDs() (map[string]bool, error) {
	rows, err := c.db.Query(`SELECT remote_uid FROM drafts WHERE remote_uid != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out[uid] = true
	}
	return out, rows.Err()
}

// DeleteDraftByRemoteUID removes the local row whose remote_uid matches.
// Used when reconciliation notices the server-side copy is gone. Does NOT
// queue a delete_draft command — the server copy is already absent.
func (c *Cache) DeleteDraftByRemoteUID(remoteUID string) error {
	if remoteUID == "" {
		return nil
	}
	res, err := c.db.Exec(`DELETE FROM drafts WHERE remote_uid = ?`, remoteUID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.publishDrafts()
	}
	return nil
}

// GetLastDraft returns the most recently updated draft across all threads.
// Used by the "resume last draft" action — works for new-compose and replies.
func (c *Cache) GetLastDraft() (Draft, bool, error) {
	var d Draft
	var updatedAt int64
	err := c.db.QueryRow(
		`SELECT thread_id, to_addrs, cc_addrs, bcc_addrs, subject, body, updated_at
		 FROM drafts ORDER BY updated_at DESC LIMIT 1`,
	).Scan(&d.ThreadID, &d.To, &d.Cc, &d.Bcc, &d.Subject, &d.Body, &updatedAt)
	if err == sql.ErrNoRows {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, err
	}
	d.UpdatedAt = time.Unix(updatedAt, 0)
	return d, true, nil
}

// sent messages — stored locally for threading

func (c *Cache) PutSentMessage(msg provider.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO sent_messages (message_id, data, date) VALUES (?, ?, ?)",
		msg.MessageID, string(data), msg.Date.Unix(),
	); err != nil {
		return err
	}
	if err := indexConversationMessagesTx(tx, "sent", "", []provider.Message{msg}); err != nil {
		return err
	}
	if err := upsertContactCandidatesTx(tx, contactCandidatesFromMessage(msg)); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Cache) GetSentMessages(limit int) ([]provider.Message, error) {
	rows, err := c.db.Query(
		"SELECT data FROM sent_messages ORDER BY date DESC LIMIT ?", limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []provider.Message
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var msg provider.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}

// sync state

func (c *Cache) GetSyncToken(key string) (string, error) {
	var val string
	err := c.db.QueryRow("SELECT value FROM sync_state WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func (c *Cache) SetSyncToken(key, value string) error {
	_, err := c.db.Exec(
		"INSERT OR REPLACE INTO sync_state (key, value) VALUES (?, ?)",
		key, value,
	)
	return err
}

// folders

func (c *Cache) PutFolders(folders []provider.Folder) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM folders")
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO folders (id, data) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range folders {
		data, err := json.Marshal(f)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(f.ID, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Cache) GetFolders() ([]provider.Folder, error) {
	rows, err := c.db.Query("SELECT data FROM folders")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []provider.Folder
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var f provider.Folder
		if err := json.Unmarshal([]byte(data), &f); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

// threads

// PutThread upserts the thread row. It does not touch label associations —
// use AddThreadToLabel/RemoveThreadFromLabel or ReplaceThreads for those.
// Publishes to every label the thread currently belongs to so subscribers
// of any affected view get notified.
func (c *Cache) PutThread(t provider.Thread) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(
		"INSERT OR REPLACE INTO threads (id, data, date, unread) VALUES (?, ?, ?, ?)",
		t.ID, string(data), t.Date.Unix(), t.Unread,
	); err != nil {
		return err
	}
	if err := indexConversationMessagesTx(tx, "thread", t.ID, t.Messages); err != nil {
		return err
	}
	if err := upsertContactCandidatesTx(tx, contactCandidatesFromThread(t)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, label := range c.labelsForThread(t.ID) {
		c.subs.publish(label)
	}
	return nil
}

// ReplaceThreads replaces the set of threads associated with a label. Any
// thread that was previously associated with `label` and isn't in the new
// list loses the association (thread row stays, to keep any other labels).
func (c *Cache) ReplaceThreads(label string, threads []provider.Thread) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM thread_labels WHERE label = ?", label); err != nil {
		return err
	}

	threadStmt, err := tx.Prepare("INSERT OR REPLACE INTO threads (id, data, date, unread) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer threadStmt.Close()

	labelStmt, err := tx.Prepare("INSERT OR IGNORE INTO thread_labels (thread_id, label) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer labelStmt.Close()

	for _, t := range threads {
		data, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if _, err := threadStmt.Exec(t.ID, string(data), t.Date.Unix(), t.Unread); err != nil {
			return err
		}
		if _, err := labelStmt.Exec(t.ID, label); err != nil {
			return err
		}
		if err := indexConversationMessagesTx(tx, label, t.ID, t.Messages); err != nil {
			return err
		}
		if err := upsertContactCandidatesTx(tx, contactCandidatesFromThread(t)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.subs.publish(label)
	return nil
}

// DeleteThread removes the thread row and all its label associations.
func (c *Cache) DeleteThread(id string) error {
	// capture labels before deleting so we can notify their subscribers
	affected := c.labelsForThread(id)
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM thread_labels WHERE thread_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM threads WHERE id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM conversation_messages WHERE thread_id = ?", id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, label := range affected {
		c.subs.publish(label)
	}
	return nil
}

// AddThreadToLabel associates a thread with a label.
func (c *Cache) AddThreadToLabel(threadID, label string) error {
	if _, err := c.db.Exec(
		"INSERT OR IGNORE INTO thread_labels (thread_id, label) VALUES (?, ?)",
		threadID, label,
	); err != nil {
		return err
	}
	c.subs.publish(label)
	return nil
}

// RemoveThreadFromLabel removes the thread→label association. The thread row
// stays in place in case it's still associated with other labels.
func (c *Cache) RemoveThreadFromLabel(threadID, label string) error {
	if _, err := c.db.Exec(
		"DELETE FROM thread_labels WHERE thread_id = ? AND label = ?",
		threadID, label,
	); err != nil {
		return err
	}
	c.subs.publish(label)
	return nil
}

// MoveThreadLabel moves a thread between labels as one visible cache update.
// Subscribers see only the final state, avoiding refreshes against the
// intermediate "removed from source but not yet added to destination" view.
func (c *Cache) MoveThreadLabel(threadID, source, dest string) error {
	if source == dest {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if source != "" {
		if _, err := tx.Exec(
			"DELETE FROM thread_labels WHERE thread_id = ? AND label = ?",
			threadID, source,
		); err != nil {
			return err
		}
	}
	if dest != "" {
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO thread_labels (thread_id, label) VALUES (?, ?)",
			threadID, dest,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if source != "" {
		c.subs.publish(source)
	}
	if dest != "" {
		c.subs.publish(dest)
	}
	return nil
}

func (c *Cache) GetThreads(label string, limit int) ([]provider.Thread, error) {
	rows, err := c.db.Query(
		`SELECT t.data FROM threads t
		 JOIN thread_labels tl ON tl.thread_id = t.id
		 WHERE tl.label = ?
		 ORDER BY t.date DESC LIMIT ?`,
		label, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var threads []provider.Thread
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var t provider.Thread
		if err := json.Unmarshal([]byte(data), &t); err != nil {
			return nil, err
		}
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

func (c *Cache) GetThread(id string) (provider.Thread, error) {
	var data string
	err := c.db.QueryRow("SELECT data FROM threads WHERE id = ?", id).Scan(&data)
	if err != nil {
		return provider.Thread{}, err
	}
	var t provider.Thread
	if err := json.Unmarshal([]byte(data), &t); err != nil {
		return provider.Thread{}, err
	}
	return t, nil
}

func (c *Cache) GetConversationMessages(seed provider.Thread, limit int) ([]provider.Message, error) {
	keys := conversationKeysForThread(seed, true)
	if len(keys) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	args := make([]any, 0, len(keys)+1)
	placeholders := make([]string, 0, len(keys))
	for _, key := range keys {
		placeholders = append(placeholders, "?")
		args = append(args, key)
	}
	args = append(args, limit*4)

	rows, err := c.db.Query(
		`SELECT source, thread_id, data
		 FROM conversation_messages
		 WHERE conversation_key IN (`+strings.Join(placeholders, ",")+`)
		 ORDER BY date ASC
		 LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seedKeys := conversationSeedKeys(seed)
	messages := make(map[string]provider.Message)
	for rows.Next() {
		var source, rowThreadID, data string
		if err := rows.Scan(&source, &rowThreadID, &data); err != nil {
			return nil, err
		}
		var msg provider.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return nil, err
		}
		if !conversationMessageRelates(seedKeys, seed.Date, msg, rowThreadID, source == "sent") {
			continue
		}
		key := conversationMessageKey(msg)
		if key == "" {
			continue
		}
		if existing, ok := messages[key]; !ok || richerConversationMessage(msg, existing) {
			messages[key] = msg
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]provider.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg)
	}
	sortMessagesByDate(out)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (c *Cache) RebuildConversationIndex() error {
	rows, err := c.db.Query("SELECT id, data FROM threads")
	if err != nil {
		return err
	}
	defer rows.Close()

	type indexedThread struct {
		id     string
		thread provider.Thread
	}
	var threads []indexedThread
	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			return err
		}
		var thread provider.Thread
		if err := json.Unmarshal([]byte(data), &thread); err != nil {
			return err
		}
		threads = append(threads, indexedThread{id: id, thread: thread})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	sent, err := c.GetSentMessages(1000)
	if err != nil {
		return err
	}

	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM conversation_messages"); err != nil {
		return err
	}
	for _, item := range threads {
		if err := indexConversationMessagesTx(tx, "thread", item.id, item.thread.Messages); err != nil {
			return err
		}
	}
	for _, msg := range sent {
		if err := indexConversationMessagesTx(tx, "sent", "", []provider.Message{msg}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Cache) ThreadLabels(threadID string) []string {
	return c.labelsForThread(threadID)
}

func indexConversationMessagesTx(tx *sql.Tx, source, threadID string, messages []provider.Message) error {
	if source != "message" && threadID != "" {
		if _, err := tx.Exec(
			"DELETE FROM conversation_messages WHERE source = ? AND thread_id = ?",
			source, threadID,
		); err != nil {
			return err
		}
	}

	stmt, err := tx.Prepare(
		`INSERT OR REPLACE INTO conversation_messages
		 (conversation_key, message_key, source, thread_id, data, date)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, msg := range messages {
		messageKey := conversationMessageKey(msg)
		if messageKey == "" {
			continue
		}
		if source == "message" || source == "sent" {
			if _, err := tx.Exec(
				"DELETE FROM conversation_messages WHERE source = ? AND message_key = ?",
				source, messageKey,
			); err != nil {
				return err
			}
		}
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		for _, key := range conversationKeysForMessage(msg, threadID, true) {
			if _, err := stmt.Exec(key, messageKey, source, threadID, string(data), msg.Date.Unix()); err != nil {
				return err
			}
		}
	}
	return nil
}

type conversationKeys struct {
	messageIDs map[string]bool
	threadIDs  map[string]bool
	subjects   map[string]bool
	senders    map[string]bool
	recipients map[string]bool
}

func conversationSeedKeys(thread provider.Thread) conversationKeys {
	keys := conversationKeys{
		messageIDs: make(map[string]bool),
		threadIDs:  make(map[string]bool),
		subjects:   make(map[string]bool),
		senders:    make(map[string]bool),
		recipients: make(map[string]bool),
	}
	if thread.ID != "" {
		keys.threadIDs[thread.ID] = true
	}
	if subject := normalizedConversationSubject(thread.Subject); subject != "" {
		keys.subjects[subject] = true
	}
	for _, msg := range thread.Messages {
		if msg.ThreadID != "" {
			keys.threadIDs[msg.ThreadID] = true
		}
		if id := normalizedMessageID(msg.MessageID); id != "" {
			keys.messageIDs[id] = true
		}
		if id := normalizedMessageID(msg.InReplyTo); id != "" {
			keys.messageIDs[id] = true
		}
		for _, ref := range msg.References {
			if id := normalizedMessageID(ref); id != "" {
				keys.messageIDs[id] = true
			}
		}
		if subject := normalizedConversationSubject(msg.Subject); subject != "" {
			keys.subjects[subject] = true
		}
		if sender := normalizedEmail(msg.From); sender != "" {
			keys.senders[sender] = true
		}
		for _, email := range conversationRecipientEmails(msg) {
			keys.recipients[email] = true
		}
	}
	return keys
}

func conversationKeysForThread(thread provider.Thread, includeSubject bool) []string {
	seen := make(map[string]bool)
	var keys []string
	if thread.ID != "" {
		keys = appendConversationKey(keys, seen, "thread:"+thread.ID)
	}
	if includeSubject {
		if subject := normalizedConversationSubject(thread.Subject); subject != "" {
			keys = appendConversationKey(keys, seen, "subject:"+subject)
		}
	}
	for _, msg := range thread.Messages {
		keys = append(keys, conversationKeysForMessage(msg, thread.ID, includeSubject)...)
	}
	return compactConversationKeys(keys)
}

func conversationKeysForMessage(msg provider.Message, fallbackThreadID string, includeSubject bool) []string {
	seen := make(map[string]bool)
	var keys []string
	if msg.ThreadID != "" {
		keys = appendConversationKey(keys, seen, "thread:"+msg.ThreadID)
	} else if fallbackThreadID != "" {
		keys = appendConversationKey(keys, seen, "thread:"+fallbackThreadID)
	}
	if id := normalizedMessageID(msg.MessageID); id != "" {
		keys = appendConversationKey(keys, seen, "msgid:"+id)
	}
	if id := normalizedMessageID(msg.InReplyTo); id != "" {
		keys = appendConversationKey(keys, seen, "msgid:"+id)
	}
	for _, ref := range msg.References {
		if id := normalizedMessageID(ref); id != "" {
			keys = appendConversationKey(keys, seen, "msgid:"+id)
		}
	}
	if includeSubject {
		if subject := normalizedConversationSubject(msg.Subject); subject != "" {
			keys = appendConversationKey(keys, seen, "subject:"+subject)
		}
	}
	return keys
}

func compactConversationKeys(keys []string) []string {
	seen := make(map[string]bool)
	out := keys[:0]
	for _, key := range keys {
		out = appendConversationKey(out, seen, key)
	}
	return out
}

func appendConversationKey(keys []string, seen map[string]bool, key string) []string {
	if key == "" || seen[key] {
		return keys
	}
	seen[key] = true
	return append(keys, key)
}

func conversationMessageRelates(seed conversationKeys, seedDate time.Time, msg provider.Message, rowThreadID string, sent bool) bool {
	if id := normalizedMessageID(msg.MessageID); id != "" && seed.messageIDs[id] {
		return true
	}
	if id := normalizedMessageID(msg.InReplyTo); id != "" && seed.messageIDs[id] {
		return true
	}
	for _, ref := range msg.References {
		if id := normalizedMessageID(ref); id != "" && seed.messageIDs[id] {
			return true
		}
	}
	if msg.ThreadID != "" && seed.threadIDs[msg.ThreadID] {
		return true
	}
	if rowThreadID != "" && seed.threadIDs[rowThreadID] {
		return true
	}
	subject := normalizedConversationSubject(msg.Subject)
	if subject != "" && seed.subjects[subject] && withinConversationWindow(seedDate, msg.Date) {
		return conversationSharesCounterparty(seed, msg, sent)
	}
	return false
}

func conversationMessageKey(msg provider.Message) string {
	if id := normalizedMessageID(msg.MessageID); id != "" {
		return "message-id:" + id
	}
	if msg.ID != "" {
		return "id:" + msg.ID
	}
	return ""
}

func normalizedMessageID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return strings.ToLower(id)
}

func normalizedConversationSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	for {
		lower := strings.ToLower(subject)
		switch {
		case strings.HasPrefix(lower, "re:"):
			subject = strings.TrimSpace(subject[3:])
		case strings.HasPrefix(lower, "fwd:"):
			subject = strings.TrimSpace(subject[4:])
		case strings.HasPrefix(lower, "fw:"):
			subject = strings.TrimSpace(subject[3:])
		default:
			return strings.ToLower(subject)
		}
	}
}

func conversationSharesCounterparty(seed conversationKeys, msg provider.Message, sent bool) bool {
	if sent {
		for _, email := range conversationRecipientEmails(msg) {
			if seed.senders[email] {
				return true
			}
		}
		return false
	}
	sender := normalizedEmail(msg.From)
	return sender != "" && seed.recipients[sender]
}

func normalizedEmail(addr provider.Address) string {
	return strings.ToLower(strings.TrimSpace(addr.Email))
}

func conversationRecipientEmails(msg provider.Message) []string {
	seen := make(map[string]bool)
	var emails []string
	add := func(addr provider.Address) {
		email := normalizedEmail(addr)
		if email == "" || seen[email] {
			return
		}
		seen[email] = true
		emails = append(emails, email)
	}
	for _, addr := range msg.To {
		add(addr)
	}
	for _, addr := range msg.CC {
		add(addr)
	}
	for _, addr := range msg.BCC {
		add(addr)
	}
	return emails
}

func withinConversationWindow(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= 30*24*time.Hour
}

func richerConversationMessage(candidate, existing provider.Message) bool {
	candidateBody := candidate.TextBody != "" || candidate.HTMLBody != ""
	existingBody := existing.TextBody != "" || existing.HTMLBody != ""
	if candidateBody != existingBody {
		return candidateBody
	}
	if len(candidate.Attachments) != len(existing.Attachments) {
		return len(candidate.Attachments) > len(existing.Attachments)
	}
	return false
}

func sortMessagesByDate(messages []provider.Message) {
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Date.Before(messages[j].Date)
	})
}

// messages

func (c *Cache) PutMessage(msg provider.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	read := 0
	if msg.Read {
		read = 1
	}
	starred := 0
	if msg.Starred {
		starred = 1
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO messages (id, thread_id, data, date, read, starred) VALUES (?, ?, ?, ?, ?, ?)",
		msg.ID, msg.ThreadID, string(data), msg.Date.Unix(), read, starred,
	); err != nil {
		return err
	}
	if err := indexConversationMessagesTx(tx, "message", msg.ThreadID, []provider.Message{msg}); err != nil {
		return err
	}
	if err := upsertContactCandidatesTx(tx, contactCandidatesFromMessage(msg)); err != nil {
		return err
	}
	// replace label associations from msg.Labels
	if _, err := tx.Exec("DELETE FROM message_labels WHERE message_id = ?", msg.ID); err != nil {
		return err
	}
	for _, label := range msg.Labels {
		if label == "" {
			continue
		}
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO message_labels (message_id, label) VALUES (?, ?)",
			msg.ID, label,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Cache) GetMessage(id string) (provider.Message, error) {
	var data string
	err := c.db.QueryRow("SELECT data FROM messages WHERE id = ?", id).Scan(&data)
	if err != nil {
		return provider.Message{}, err
	}
	var msg provider.Message
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		return provider.Message{}, err
	}
	return msg, nil
}

// search cached messages
func (c *Cache) Search(query string, limit int) ([]provider.Thread, error) {
	parsed := parseSearchQuery(query)
	if !parsed.structured {
		pattern := "%" + query + "%"
		rows, err := c.db.Query(
			"SELECT data FROM threads WHERE data LIKE ? ORDER BY date DESC LIMIT ?",
			pattern, limit,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanThreadRows(rows)
	}

	rows, err := c.db.Query("SELECT data FROM threads ORDER BY date DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates, err := scanThreadRows(rows)
	if err != nil {
		return nil, err
	}
	var threads []provider.Thread
	for _, thread := range candidates {
		if parsed.matches(thread) {
			threads = append(threads, thread)
			if limit > 0 && len(threads) >= limit {
				break
			}
		}
	}
	return threads, nil
}

func scanThreadRows(rows *sql.Rows) ([]provider.Thread, error) {
	var threads []provider.Thread
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var thread provider.Thread
		if err := json.Unmarshal([]byte(data), &thread); err != nil {
			return nil, err
		}
		threads = append(threads, thread)
	}
	return threads, rows.Err()
}

type searchQuery struct {
	plain      []string
	from       []string
	to         []string
	subject    []string
	structured bool
}

func parseSearchQuery(query string) searchQuery {
	var parsed searchQuery
	for _, token := range strings.Fields(strings.TrimSpace(query)) {
		key, value, ok := strings.Cut(token, ":")
		if !ok || value == "" {
			parsed.plain = append(parsed.plain, strings.ToLower(token))
			continue
		}
		value = strings.ToLower(value)
		switch strings.ToLower(key) {
		case "from":
			parsed.from = append(parsed.from, value)
			parsed.structured = true
		case "to":
			parsed.to = append(parsed.to, value)
			parsed.structured = true
		case "subject":
			parsed.subject = append(parsed.subject, value)
			parsed.structured = true
		default:
			parsed.plain = append(parsed.plain, strings.ToLower(token))
		}
	}
	return parsed
}

func (q searchQuery) matches(thread provider.Thread) bool {
	haystack := strings.ToLower(searchThreadText(thread))
	for _, term := range q.plain {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	for _, term := range q.subject {
		if !strings.Contains(strings.ToLower(thread.Subject), term) && !messagesContainSubject(thread.Messages, term) {
			return false
		}
	}
	for _, term := range q.from {
		if !messagesContainSender(thread.Messages, term) {
			return false
		}
	}
	for _, term := range q.to {
		if !messagesContainRecipient(thread.Messages, term) {
			return false
		}
	}
	return true
}

func searchThreadText(thread provider.Thread) string {
	var b strings.Builder
	b.WriteString(thread.Subject)
	b.WriteByte(' ')
	b.WriteString(thread.Snippet)
	for _, msg := range thread.Messages {
		b.WriteByte(' ')
		b.WriteString(msg.Subject)
		b.WriteByte(' ')
		b.WriteString(msg.From.String())
		for _, addr := range append(append([]provider.Address{}, msg.To...), msg.CC...) {
			b.WriteByte(' ')
			b.WriteString(addr.String())
		}
		b.WriteByte(' ')
		b.WriteString(msg.TextBody)
	}
	return b.String()
}

func messagesContainSubject(messages []provider.Message, term string) bool {
	for _, msg := range messages {
		if strings.Contains(strings.ToLower(msg.Subject), term) {
			return true
		}
	}
	return false
}

func messagesContainSender(messages []provider.Message, term string) bool {
	for _, msg := range messages {
		if strings.Contains(strings.ToLower(msg.From.String()), term) {
			return true
		}
	}
	return false
}

func messagesContainRecipient(messages []provider.Message, term string) bool {
	for _, msg := range messages {
		for _, addr := range append(append([]provider.Address{}, msg.To...), msg.CC...) {
			if strings.Contains(strings.ToLower(addr.String()), term) {
				return true
			}
		}
	}
	return false
}

// stats
func (c *Cache) LastSync() (time.Time, error) {
	val, err := c.GetSyncToken("last_sync")
	if err != nil || val == "" {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, val)
}

func (c *Cache) SetLastSync(t time.Time) error {
	return c.SetSyncToken("last_sync", t.Format(time.RFC3339))
}
