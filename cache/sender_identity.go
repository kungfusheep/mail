package cache

import (
	"database/sql"
	"time"
)

type SenderIdentity struct {
	Domain         string
	DisplayName    string
	IconURL        string
	ThemeColor     string
	BIMILogoURL    string
	Source         string
	Confidence     int
	ColorCheckedAt time.Time
	UpdatedAt      time.Time
}

func (c *Cache) PutSenderIdentity(identity SenderIdentity) error {
	if identity.Domain == "" {
		return nil
	}
	updatedAt := identity.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	colorCheckedAt := identity.ColorCheckedAt
	if colorCheckedAt.IsZero() {
		colorCheckedAt = updatedAt
	}
	_, err := c.db.Exec(
		`INSERT OR REPLACE INTO sender_identities
			(domain, display_name, icon_url, theme_color, bimi_logo_url, source, confidence, color_checked_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		identity.Domain,
		identity.DisplayName,
		identity.IconURL,
		identity.ThemeColor,
		identity.BIMILogoURL,
		identity.Source,
		identity.Confidence,
		colorCheckedAt.Unix(),
		updatedAt.Unix(),
	)
	return err
}

func (c *Cache) SenderIdentity(domain string) (SenderIdentity, bool, error) {
	var identity SenderIdentity
	var updatedAt int64
	var colorCheckedAt int64
	err := c.db.QueryRow(
		`SELECT domain, display_name, icon_url, theme_color, bimi_logo_url, source, confidence, color_checked_at, updated_at
		 FROM sender_identities WHERE domain = ?`,
		domain,
	).Scan(
		&identity.Domain,
		&identity.DisplayName,
		&identity.IconURL,
		&identity.ThemeColor,
		&identity.BIMILogoURL,
		&identity.Source,
		&identity.Confidence,
		&colorCheckedAt,
		&updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return SenderIdentity{}, false, nil
		}
		return SenderIdentity{}, false, err
	}
	if colorCheckedAt > 0 {
		identity.ColorCheckedAt = time.Unix(colorCheckedAt, 0)
	}
	identity.UpdatedAt = time.Unix(updatedAt, 0)
	return identity, true, nil
}

func (c *Cache) DeleteSenderIdentity(domain string) error {
	_, err := c.db.Exec(`DELETE FROM sender_identities WHERE domain = ?`, domain)
	return err
}

func (c *Cache) SenderIdentityFresh(domain string, maxAge time.Duration) (bool, error) {
	identity, ok, err := c.SenderIdentity(domain)
	if err != nil || !ok {
		return false, err
	}
	return time.Since(identity.UpdatedAt) <= maxAge, nil
}
