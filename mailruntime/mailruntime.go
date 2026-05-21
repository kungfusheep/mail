package mailruntime

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/contacts"
	"github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/senderid"
)

type Config struct {
	Backend bool
	IMAP    imap.Config
}

type Callbacks struct {
	Status         func(string)
	FoldersChanged func()
	ThreadsChanged func()
	Render         func()
}

type Runtime struct {
	db *cache.Cache
	mb *mailbox.State

	cfg Config
	cb  Callbacks

	imapClient *imap.IMAP
	idleCancel context.CancelFunc
	labelUnsub func()
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
	if !r.cfg.Backend {
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

		r.mb.SyncSent()
		r.mb.ProcessPendingCommands()
		r.syncActiveFolderFromBackend()

		go r.cacheContacts()
		go r.enrichVisibleSenders()
		r.WatchActiveFolder()
	}()
}

func (r *Runtime) Close() {
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
	r.Close()

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

	ctx, cancel := context.WithCancel(context.Background())
	r.idleCancel = cancel
	go func() {
		if err := r.imapClient.Idle(ctx, label, func() {
			log.Printf("idle: change on %s, syncing", label)
			if err := r.mb.SyncThreads(); err != nil {
				log.Printf("idle sync: %v", err)
			}
		}); err != nil && ctx.Err() == nil {
			log.Printf("idle %s: %v", label, err)
		}
	}()
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
	if err := r.mb.SyncThreads(); err != nil {
		r.status(fmt.Sprintf("sync: %v", err))
	} else {
		r.status("synced")
	}
	r.threadsChanged()
	r.render()
	go r.enrichVisibleSenders()
}

func (r *Runtime) cacheContacts() {
	if r.db == nil {
		return
	}
	log.Println("contacts: loading from macOS...")
	all := contacts.All()
	log.Printf("contacts: loaded %d, caching", len(all))
	if len(all) > 0 {
		r.db.PutContacts(all)
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
		r.mb.BuildThreadDisplay()
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
