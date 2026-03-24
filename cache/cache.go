package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kungfusheep/mail/provider"
	_ "github.com/mattn/go-sqlite3"
)

type Cache struct {
	db *sql.DB
}

func New() (*Cache, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".config", "mail")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", filepath.Join(dir, "cache.db"))
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

func (c *Cache) Close() error {
	return c.db.Close()
}

func (c *Cache) migrate() error {
	_, err := c.db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL,
			folder TEXT NOT NULL DEFAULT '',
			data TEXT NOT NULL,
			date INTEGER NOT NULL,
			read INTEGER NOT NULL DEFAULT 0,
			starred INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_id);
		CREATE INDEX IF NOT EXISTS idx_messages_folder ON messages(folder);
		CREATE INDEX IF NOT EXISTS idx_messages_date ON messages(date);

		CREATE TABLE IF NOT EXISTS threads (
			id TEXT PRIMARY KEY,
			folder TEXT NOT NULL DEFAULT '',
			data TEXT NOT NULL,
			date INTEGER NOT NULL,
			unread INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_threads_folder ON threads(folder);
		CREATE INDEX IF NOT EXISTS idx_threads_date ON threads(date);

		CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS sync_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	return err
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

func (c *Cache) PutThread(folder string, t provider.Thread) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(
		"INSERT OR REPLACE INTO threads (id, folder, data, date, unread) VALUES (?, ?, ?, ?, ?)",
		t.ID, folder, string(data), t.Date.Unix(), t.Unread,
	)
	return err
}

func (c *Cache) GetThreads(folder string, limit int) ([]provider.Thread, error) {
	rows, err := c.db.Query(
		"SELECT data FROM threads WHERE folder = ? ORDER BY date DESC LIMIT ?",
		folder, limit,
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

// messages

func (c *Cache) PutMessage(msg provider.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	folder := ""
	if len(msg.Labels) > 0 {
		folder = msg.Labels[0]
	}
	read := 0
	if msg.Read {
		read = 1
	}
	starred := 0
	if msg.Starred {
		starred = 1
	}
	_, err = c.db.Exec(
		"INSERT OR REPLACE INTO messages (id, thread_id, folder, data, date, read, starred) VALUES (?, ?, ?, ?, ?, ?, ?)",
		msg.ID, msg.ThreadID, folder, string(data), msg.Date.Unix(), read, starred,
	)
	return err
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
