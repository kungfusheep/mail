package contacts

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/kungfusheep/mail/provider"
)

// All reads every contact with an email from the macOS AddressBook databases.
// reads directly from SQLite — instant, no Contacts app launch.
func All() []provider.Address {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	sourcesDir := filepath.Join(home, "Library", "Application Support", "AddressBook", "Sources")
	entries, err := os.ReadDir(sourcesDir)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var results []provider.Address

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dbPath := filepath.Join(sourcesDir, entry.Name(), "AddressBook-v22.abcddb")
		contacts := readFromDB(dbPath)
		for _, c := range contacts {
			if !seen[c.Email] {
				seen[c.Email] = true
				results = append(results, c)
			}
		}
	}

	// also check the root database
	rootDB := filepath.Join(home, "Library", "Application Support", "AddressBook", "AddressBook-v22.abcddb")
	for _, c := range readFromDB(rootDB) {
		if !seen[c.Email] {
			seen[c.Email] = true
			results = append(results, c)
		}
	}

	return results
}

func readFromDB(path string) []provider.Address {
	db, err := sql.Open("sqlite3", path+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT COALESCE(r.ZFIRSTNAME,'') || ' ' || COALESCE(r.ZLASTNAME,''), e.ZADDRESS
		FROM ZABCDEMAILADDRESS e
		JOIN ZABCDRECORD r ON e.ZOWNER = r.Z_PK
		WHERE e.ZADDRESS IS NOT NULL
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var results []provider.Address
	for rows.Next() {
		var name, email string
		if err := rows.Scan(&name, &email); err != nil {
			continue
		}
		name = strings.TrimSpace(name)
		email = strings.TrimSpace(email)
		if email != "" {
			results = append(results, provider.Address{Name: name, Email: email})
		}
	}
	return results
}
