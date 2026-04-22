// dedupe-drafts scans the IMAP Drafts folder, groups messages by (subject,
// body hash), and expunges duplicates keeping the newest by date. Useful
// when prior client code appended new draft versions without successfully
// expunging the previous ones.
//
// Usage:
//
//	go run ./cmd/dedupe-drafts
//
// Prompts for confirmation before deleting anything.
package main

import (
	"bufio"
	"crypto/sha1"
	"fmt"
	"os"
	"sort"
	"strings"

	imapprov "github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/provider"
)

func main() {
	cfg, err := imapprov.LoadConfig()
	if err != nil {
		fatal("loading config: %v", err)
	}
	im := imapprov.New(cfg)
	if err := im.Authenticate(); err != nil {
		fatal("authenticate: %v", err)
	}
	defer im.Close()

	draftsFolder, err := resolveDraftsFolder(im)
	if err != nil {
		fatal("resolve drafts folder: %v", err)
	}
	fmt.Printf("scanning %s\n", draftsFolder)

	res, err := im.ListThreads(provider.ListOptions{
		Folder:     draftsFolder,
		MaxResults: 1000,
	})
	if err != nil {
		fatal("list: %v", err)
	}

	var msgs []provider.Message
	for _, t := range res.Threads {
		for _, stub := range t.Messages {
			full, ferr := im.GetMessage(stub.ID)
			if ferr != nil {
				// keep the envelope-only version — we lose body-based
				// matching but can still match on subject.
				msgs = append(msgs, stub)
				continue
			}
			msgs = append(msgs, full)
		}
	}
	fmt.Printf("found %d messages\n\n", len(msgs))

	// group by (subject, body hash). Bodies hash over the text form —
	// different MIME representations of the same draft end up equal.
	type key struct {
		subject  string
		bodyHash string
	}
	groups := make(map[key][]provider.Message)
	for _, m := range msgs {
		body := m.TextBody
		if body == "" {
			body = m.HTMLBody
		}
		h := sha1.Sum([]byte(body))
		k := key{
			subject:  strings.TrimSpace(m.Subject),
			bodyHash: fmt.Sprintf("%x", h),
		}
		groups[k] = append(groups[k], m)
	}

	type dup struct {
		subject string
		keep   provider.Message
		remove []provider.Message
	}
	var dups []dup
	for k, ms := range groups {
		if len(ms) < 2 {
			continue
		}
		sort.Slice(ms, func(i, j int) bool {
			return ms[i].Date.After(ms[j].Date)
		})
		dups = append(dups, dup{
			subject: k.subject,
			keep:   ms[0],
			remove: ms[1:],
		})
	}
	sort.Slice(dups, func(i, j int) bool {
		// show oldest groups first so the eye tracks from "stale" to "recent"
		return dups[i].keep.Date.Before(dups[j].keep.Date)
	})

	if len(dups) == 0 {
		fmt.Println("no duplicates found")
		return
	}

	toDelete := 0
	for _, d := range dups {
		subj := d.subject
		if subj == "" {
			subj = "(no subject)"
		}
		fmt.Printf("%s\n", subj)
		fmt.Printf("  keep   UID=%s  (%s)\n", d.keep.ID, d.keep.Date.Format("2006-01-02"))
		for _, m := range d.remove {
			fmt.Printf("  remove UID=%s  (%s)\n", m.ID, m.Date.Format("2006-01-02"))
			toDelete++
		}
		fmt.Println()
	}
	fmt.Printf("will remove %d duplicate(s), keep %d unique draft(s)\n", toDelete, len(dups))
	fmt.Print("proceed? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	confirm, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
		fmt.Println("aborted")
		return
	}

	deleted := 0
	for _, d := range dups {
		for _, m := range d.remove {
			if err := im.DeleteDraft(draftsFolder, m.ID); err != nil {
				fmt.Printf("failed to delete UID=%s: %v\n", m.ID, err)
				continue
			}
			deleted++
		}
	}
	fmt.Printf("\ndone: deleted %d duplicate(s)\n", deleted)
}

// resolveDraftsFolder picks the best Drafts folder the server advertises.
// Gmail accounts often expose both "[Gmail]/Drafts" and "[Google Mail]/Drafts"
// as aliases; we pick whichever has more messages, falling back to the first
// folder whose ID ends in "/Drafts" or equals "Drafts".
func resolveDraftsFolder(im *imapprov.IMAP) (string, error) {
	folders, err := im.ListFolders()
	if err != nil {
		return "", err
	}
	var bestID string
	bestTotal := -1
	for _, f := range folders {
		if !isDraftsID(f.ID) {
			continue
		}
		if f.Total > bestTotal {
			bestTotal = f.Total
			bestID = f.ID
		}
	}
	if bestID == "" {
		return "", fmt.Errorf("no Drafts folder found")
	}
	return bestID, nil
}

func isDraftsID(id string) bool {
	if id == "Drafts" {
		return true
	}
	return strings.HasSuffix(id, "/Drafts")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
