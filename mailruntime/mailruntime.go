package mailruntime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/contacts"
	"github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/provider"
	"github.com/kungfusheep/mail/senderid"
)

type Config struct {
	Backend bool
	IMAP    imap.Config
}

type Callbacks struct {
	Status            func(string)
	FoldersChanged    func()
	ThreadsChanged    func()
	Render            func()
	DesktopNotifyMail func(title, body string)
}

type Runtime struct {
	db *cache.Cache
	mb *mailbox.State

	cfg Config
	cb  Callbacks

	imapClient      *imap.IMAP
	runtimeCancel   context.CancelFunc
	idleCancel      context.CancelFunc
	inboxIdleCancel context.CancelFunc
	labelUnsub      func()
	inboxUnsub      func()
	knownInbox      map[string]bool
}

func New(db *cache.Cache, mb *mailbox.State, cfg Config, cb Callbacks) *Runtime {
	return &Runtime{
		db:  db,
		mb:  mb,
		cfg: cfg,
		cb:  cb,
	}
}

func (r *Runtime) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.runtimeCancel = cancel
	go r.snoozeLoop(ctx)
	if !r.cfg.Backend {
		r.watchInboxNotifications()
		r.WatchActiveFolder()
		go r.enrichVisibleSenders()
		return
	}

	go func() {
		r.imapClient = imap.New(r.cfg.IMAP)
		if err := r.imapClient.Authenticate(); err != nil {
			r.status(fmt.Sprintf("imap: %v", err))
			r.render()
			return
		}
		r.mb.SetIMAP(r.imapClient)
		log.Println("imap: authenticated")

		if err := r.mb.SyncFolders(); err != nil {
			r.status(fmt.Sprintf("sync: %v", err))
			r.render()
			return
		}
		r.foldersChanged()
		r.render()

		r.watchInboxNotifications()
		r.watchInboxRealtime()
		r.mb.SyncSent()
		r.mb.ProcessPendingCommands()
		r.syncActiveFolderFromBackend()

		go r.cacheContacts()
		go r.enrichVisibleSenders()
		r.WatchActiveFolder()
	}()
}

func (r *Runtime) snoozeLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	r.processDueSnoozes(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.processDueSnoozes(now)
		}
	}
}

func (r *Runtime) processDueSnoozes(now time.Time) {
	if r.mb == nil {
		return
	}
	if restored := r.mb.ProcessDueSnoozes(now); restored > 0 {
		r.status(fmt.Sprintf("restored %d snoozed", restored))
		r.threadsChanged()
		r.render()
		r.FlushPending()
	}
}

func (r *Runtime) Close() {
	if r.runtimeCancel != nil {
		r.runtimeCancel()
		r.runtimeCancel = nil
	}
	r.closeActiveFolderWatch()
	if r.inboxUnsub != nil {
		r.inboxUnsub()
		r.inboxUnsub = nil
	}
	if r.inboxIdleCancel != nil {
		r.inboxIdleCancel()
		r.inboxIdleCancel = nil
	}
}

func (r *Runtime) closeActiveFolderWatch() {
	if r.idleCancel != nil {
		r.idleCancel()
		r.idleCancel = nil
	}
	if r.labelUnsub != nil {
		r.labelUnsub()
		r.labelUnsub = nil
	}
}

func (r *Runtime) WatchActiveFolder() {
	r.closeActiveFolderWatch()

	label := r.mb.ActiveFolderID()
	if label == "" {
		return
	}

	r.labelUnsub = r.mb.Watch(label, func() {
		r.threadsChanged()
		r.render()
	})

	if !r.cfg.Backend || r.imapClient == nil {
		return
	}
	if !r.shouldStartActiveIdle(label) {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.idleCancel = cancel
	go func() {
		if err := r.imapClient.Idle(ctx, label, func() {
			log.Printf("idle: change on %s, syncing", label)
			if err := r.syncFolderFromBackend(label); err != nil {
				log.Printf("idle sync: %v", err)
			}
		}); err != nil && ctx.Err() == nil {
			log.Printf("idle %s: %v", label, err)
		}
	}()
}

func (r *Runtime) watchInboxRealtime() {
	if !r.cfg.Backend || r.imapClient == nil || r.inboxIdleCancel != nil {
		return
	}
	label := r.inboxLabel()
	if label == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.inboxIdleCancel = cancel
	go func() {
		if err := r.imapClient.Idle(ctx, label, func() {
			log.Printf("idle: inbox change on %s, syncing", label)
			if err := r.syncFolderFromBackend(label); err != nil {
				log.Printf("idle inbox sync: %v", err)
			}
		}); err != nil && ctx.Err() == nil {
			log.Printf("idle inbox %s: %v", label, err)
		}
	}()
}

func (r *Runtime) inboxLabel() string {
	if r.mb == nil {
		return ""
	}
	return r.mb.FolderIDByDisplayName("Inbox")
}

func (r *Runtime) shouldStartActiveIdle(label string) bool {
	return label != "" && (label != r.inboxLabel() || r.inboxIdleCancel == nil)
}

func (r *Runtime) watchInboxNotifications() {
	if r.db == nil || r.mb == nil || r.cb.DesktopNotifyMail == nil || r.inboxUnsub != nil {
		return
	}
	label := r.mb.FolderIDByDisplayName("Inbox")
	if label == "" {
		return
	}
	r.knownInbox = r.inboxSnapshot(label)
	ch, unsub := r.db.Subscribe(label)
	r.inboxUnsub = unsub
	go func() {
		for range ch {
			r.notifyNewInboxThreads(label)
		}
	}()
}

func (r *Runtime) inboxSnapshot(label string) map[string]bool {
	known := make(map[string]bool)
	threads, err := r.db.GetThreads(label, 250)
	if err != nil {
		log.Printf("desktop notifications: inbox snapshot failed: %v", err)
		return known
	}
	for _, thread := range threads {
		if thread.ID != "" {
			known[thread.ID] = true
		}
	}
	return known
}

func (r *Runtime) notifyNewInboxThreads(label string) {
	threads, err := r.db.GetThreads(label, 250)
	if err != nil {
		log.Printf("desktop notifications: inbox read failed: %v", err)
		return
	}
	if r.knownInbox == nil {
		r.knownInbox = make(map[string]bool)
	}
	for _, thread := range threads {
		if thread.ID == "" || r.knownInbox[thread.ID] {
			continue
		}
		r.knownInbox[thread.ID] = true
		title, body := inboxNotificationText(thread)
		r.cb.DesktopNotifyMail(title, body)
	}
}

func inboxNotificationText(thread provider.Thread) (string, string) {
	from := "new email"
	if len(thread.Messages) > 0 {
		msg := thread.Messages[len(thread.Messages)-1]
		if msg.From.Name != "" {
			from = msg.From.Name
		} else if msg.From.Email != "" {
			from = msg.From.Email
		}
	}
	subject := strings.TrimSpace(thread.Subject)
	if subject == "" {
		subject = "(no subject)"
	}
	return from, subject
}

func (r *Runtime) SyncActiveFolder() {
	if !r.cfg.Backend || r.imapClient == nil {
		r.mb.LoadThreads()
		r.threadsChanged()
		r.status("cache refreshed")
		r.render()
		return
	}
	go r.syncActiveFolderFromBackend()
}

func (r *Runtime) FlushPending() {
	if !r.cfg.Backend || r.imapClient == nil {
		return
	}
	go r.mb.ProcessPendingCommands()
}

func (r *Runtime) syncActiveFolderFromBackend() {
	r.mb.ProcessPendingCommands()
	if err := r.syncFolderFromBackend(r.mb.ActiveFolderID()); err != nil {
		r.status(fmt.Sprintf("sync: %v", err))
	} else {
		r.status("synced")
	}
	r.threadsChanged()
	r.render()
	go r.enrichVisibleSenders()
}

func (r *Runtime) syncFolderFromBackend(label string) error {
	if label == "" {
		return nil
	}
	return r.mb.SyncFolder(label)
}

func (r *Runtime) cacheContacts() {
	if r.db == nil {
		return
	}
	log.Println("contacts: loading from macOS...")
	all := contacts.All()
	log.Printf("contacts: loaded %d, caching", len(all))
	if len(all) > 0 {
		if err := r.db.PutContacts(all); err != nil {
			log.Printf("contacts: cache: %v", err)
		}
	}
	if err := r.db.RebuildContactIndex(); err != nil {
		log.Printf("contacts: rebuild index: %v", err)
	}
}

func (r *Runtime) enrichVisibleSenders() {
	if r.db == nil {
		return
	}
	domains := r.mb.SenderDomains(50)
	if len(domains) == 0 {
		return
	}

	enricher := senderid.Enricher{}
	changed := false
	for _, domain := range domains {
		identity, found, err := r.db.SenderIdentity(domain)
		if err != nil {
			log.Printf("sender identity cache: %v", err)
			continue
		}
		if found && senderIdentityFresh(identity, 14*24*time.Hour) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		identity = enricher.Enrich(ctx, domain)
		cancel()
		if identity.Domain == "" {
			continue
		}
		if err := r.db.PutSenderIdentity(identity); err != nil {
			log.Printf("sender identity save %s: %v", domain, err)
			continue
		}
		changed = true
	}
	if changed {
		r.threadsChanged()
		r.render()
	}
}

func senderIdentityFresh(identity cache.SenderIdentity, maxAge time.Duration) bool {
	if time.Since(identity.UpdatedAt) > maxAge {
		return false
	}
	if identity.ThemeColor != "" {
		return true
	}
	return !identity.ColorCheckedAt.IsZero() && time.Since(identity.ColorCheckedAt) <= maxAge
}

func (r *Runtime) status(text string) {
	if r.cb.Status != nil {
		r.cb.Status(text)
	}
}

func (r *Runtime) foldersChanged() {
	if r.cb.FoldersChanged != nil {
		r.cb.FoldersChanged()
	}
}

func (r *Runtime) threadsChanged() {
	if r.cb.ThreadsChanged != nil {
		r.cb.ThreadsChanged()
	}
}

func (r *Runtime) render() {
	if r.cb.Render != nil {
		r.cb.Render()
	}
}
