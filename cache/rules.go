package cache

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/kungfusheep/mail/provider"
)

type Rule struct {
	ID              string    `json:"id"`
	Name            string    `json:"name,omitempty"`
	Enabled         bool      `json:"enabled"`
	FromContains    string    `json:"from_contains,omitempty"`
	ToContains      string    `json:"to_contains,omitempty"`
	SubjectContains string    `json:"subject_contains,omitempty"`
	BodyContains    string    `json:"body_contains,omitempty"`
	MoveTo          string    `json:"move_to,omitempty"`
	MarkRead        bool      `json:"mark_read,omitempty"`
	Star            bool      `json:"star,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
}

func (c *Cache) PutRule(rule Rule) error {
	if rule.ID == "" {
		return errors.New("rule id is empty")
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now()
	}
	data, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(
		"INSERT OR REPLACE INTO rules (id, data, created_at) VALUES (?, ?, ?)",
		rule.ID, string(data), rule.CreatedAt.Unix(),
	)
	return err
}

func (c *Cache) Rules() ([]Rule, error) {
	rows, err := c.db.Query("SELECT data FROM rules ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []Rule
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var rule Rule
		if err := json.Unmarshal([]byte(data), &rule); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (c *Cache) DeleteRule(id string) error {
	_, err := c.db.Exec("DELETE FROM rules WHERE id = ?", id)
	return err
}

func (rule Rule) Matches(thread provider.Thread) bool {
	if !rule.Enabled {
		return false
	}
	msgs := thread.Messages
	if len(msgs) == 0 {
		return false
	}
	if !containsIfSet(threadSenderText(msgs), rule.FromContains) {
		return false
	}
	if !containsIfSet(threadRecipientText(msgs), rule.ToContains) {
		return false
	}
	if !containsIfSet(threadSubjectText(thread), rule.SubjectContains) {
		return false
	}
	if !containsIfSet(threadBodyText(msgs), rule.BodyContains) {
		return false
	}
	return true
}

func containsIfSet(haystack, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(haystack), needle)
}

func threadSenderText(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.From.String())
		b.WriteByte(' ')
	}
	return b.String()
}

func threadRecipientText(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		for _, addr := range msg.To {
			b.WriteString(addr.String())
			b.WriteByte(' ')
		}
		for _, addr := range msg.CC {
			b.WriteString(addr.String())
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func threadSubjectText(thread provider.Thread) string {
	var b strings.Builder
	b.WriteString(thread.Subject)
	b.WriteByte(' ')
	for _, msg := range thread.Messages {
		b.WriteString(msg.Subject)
		b.WriteByte(' ')
	}
	return b.String()
}

func threadBodyText(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.TextBody)
		b.WriteByte(' ')
		b.WriteString(msg.HTMLBody)
		b.WriteByte(' ')
	}
	return b.String()
}
