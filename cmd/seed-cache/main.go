package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/provider"
)

func main() {
	path := "/tmp/mail-display-fixture/cache.db"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	db, err := cache.NewAt(path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := seed(db); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("seeded %s\n", path)
	fmt.Println("run with: MAIL_OFFLINE=1 MAIL_CACHE_PATH=" + path + " go run .")
}

func seed(db *cache.Cache) error {
	folders := []provider.Folder{
		{ID: "INBOX", Name: "Inbox", Unread: 1, Total: 3},
		{ID: "Sent", Name: "Sent", Total: 1},
		{ID: "[Gmail]/Drafts", Name: "Drafts"},
		{ID: "[Gmail]/Trash", Name: "Trash"},
		{ID: "[Gmail]/All Mail", Name: "All Mail", Total: 3},
	}
	if err := db.PutFolders(folders); err != nil {
		return err
	}

	now := time.Date(2026, 5, 6, 9, 30, 0, 0, time.Local)
	threads := []provider.Thread{
		{
			ID:      "fixture-main-body",
			Subject: "Display segmentation review",
			Snippet: "Here is the main thing I wrote.",
			Date:    now,
			Unread:  1,
			Participants: []provider.Address{
				{Name: "Avery Stone", Email: "avery@example.test"},
			},
			Messages: []provider.Message{{
				ID:        "1001",
				ThreadID:  "fixture-main-body",
				From:      provider.Address{Name: "Avery Stone", Email: "avery@example.test"},
				To:        []provider.Address{{Name: "Pete", Email: "me@example.test"}},
				Subject:   "Display segmentation review",
				Date:      now,
				TextBody:  "Here is the main thing I wrote.\n\nIt should stay bright and readable.\n\n-- \nAvery\n\nPrivacy policy: https://example.test/privacy\nTerms & conditions: https://example.test/terms",
				MessageID: "<fixture-main-body@example.test>",
			}},
		},
		{
			ID:      "fixture-quoted-reply",
			Subject: "Re: Earlier question",
			Snippet: "Yes, this is the fresh reply.",
			Date:    now.Add(-2 * time.Hour),
			Participants: []provider.Address{
				{Name: "Mina Carter", Email: "mina@example.test"},
			},
			Messages: []provider.Message{{
				ID:        "1002",
				ThreadID:  "fixture-quoted-reply",
				From:      provider.Address{Name: "Mina Carter", Email: "mina@example.test"},
				To:        []provider.Address{{Name: "Pete", Email: "me@example.test"}},
				Subject:   "Re: Earlier question",
				Date:      now.Add(-2 * time.Hour),
				TextBody:  "Yes, this is the fresh reply.\n\nOn Tue, 5 May 2026 at 16:40, Pete wrote:\n> Could you send over the detail?\n> It helps with the display work.",
				MessageID: "<fixture-quoted-reply@example.test>",
			}},
		},
		{
			ID:      "fixture-html-newsletter",
			Subject: "Your fixture newsletter",
			Snippet: "The rendered HTML should be segmented too.",
			Date:    now.Add(-24 * time.Hour),
			Participants: []provider.Address{
				{Name: "Example News", Email: "news@example.test"},
			},
			Messages: []provider.Message{{
				ID:        "1003",
				ThreadID:  "fixture-html-newsletter",
				From:      provider.Address{Name: "Example News", Email: "news@example.test"},
				To:        []provider.Address{{Name: "Pete", Email: "me@example.test"}},
				Subject:   "Your fixture newsletter",
				Date:      now.Add(-24 * time.Hour),
				HTMLBody:  `<html><body><p>The rendered HTML should be segmented too.</p><p>This paragraph is the useful body.</p><hr><p>Unsubscribe from these emails</p><p>Registered office: Example House</p></body></html>`,
				MessageID: "<fixture-html-newsletter@example.test>",
			}},
		},
	}

	return db.ReplaceThreads("INBOX", threads)
}
