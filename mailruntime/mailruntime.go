package mailruntime

import (
	"context"
	"fmt"
	"log"

	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/contacts"
	"github.com/kungfusheep/mail/imap"
	"github.com/kungfusheep/mail/mailbox"
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
	mb *mailbox.Mailbox

	cfg Config
	cb  Callbacks

	imapClient *imap.IMAP
	idleCancel context.CancelFunc
	labelUnsub func()
}

func New(db *cache.Cache, mb *mailbox.Mailbox, cfg Config, cb Callbacks) *Runtime {
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
