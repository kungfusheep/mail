package cache

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

		CREATE TABLE IF NOT EXISTS contacts (
			email TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
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
	`)
	if err != nil {
		return err
	}

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

	stmt, err := tx.Prepare("INSERT OR REPLACE INTO contacts (email, name, updated_at) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, contact := range contacts {
		if _, err := stmt.Exec(contact.Email, contact.Name, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Cache) SearchContacts(query string) ([]provider.Address, error) {
	pattern := "%" + query + "%"
	rows, err := c.db.Query(
		"SELECT name, email FROM contacts WHERE name LIKE ? OR email LIKE ? ORDER BY name LIMIT 10",
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
	_, err = c.db.Exec(
		"INSERT OR REPLACE INTO sent_messages (message_id, data, date) VALUES (?, ?, ?)",
		msg.MessageID, string(data), msg.Date.Unix(),
	)
	return err
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
	if _, err = c.db.Exec(
		"INSERT OR REPLACE INTO threads (id, data, date, unread) VALUES (?, ?, ?, ?)",
		t.ID, string(data), t.Date.Unix(), t.Unread,
	); err != nil {
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

func (c *Cache) ThreadLabels(threadID string) []string {
	return c.labelsForThread(threadID)
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
	pattern := "%" + query + "%"
	rows, err := c.db.Query(
		"SELECT data FROM threads WHERE data LIKE ? ORDER BY date DESC LIMIT ?",
		pattern, limit,
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
