package imap

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// Idle opens a second IMAP connection dedicated to IDLE, selects the given
// label (mailbox), and runs an IDLE loop. onChange is invoked whenever the
// server pushes EXISTS (new messages) or EXPUNGE (deletions) updates on the
// selected mailbox. Rapid bursts of updates are debounced to one onChange.
//
// Idle blocks until ctx is canceled. The primary IMAP connection is not
// touched — foreground operations (fetch, move, etc.) continue to run
// against it in parallel.
func (im *IMAP) Idle(ctx context.Context, label string, onChange func()) error {
	notify := make(chan struct{}, 8)
	opts := &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			// Mailbox fires on EXISTS updates (new messages).
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					nonBlockingSignal(notify)
				}
			},
			// Expunge fires on deletions (including moves from this folder).
			Expunge: func(uint32) { nonBlockingSignal(notify) },
			// Fetch fires on flag changes from other clients (read/unread,
			// star, etc.). We must drain the streaming payload via Collect
			// or the IDLE processor blocks.
			Fetch: func(msg *imapclient.FetchMessageData) {
				_, _ = msg.Collect()
				nonBlockingSignal(notify)
			},
		},
	}

	c, err := imapclient.DialTLS(im.config.Server, opts)
	if err != nil {
		return fmt.Errorf("idle dial: %w", err)
	}
	defer c.Close()

	if err := c.Login(im.config.Email, im.config.Password).Wait(); err != nil {
		return fmt.Errorf("idle login: %w", err)
	}
	if _, err := c.Select(label, nil).Wait(); err != nil {
		return fmt.Errorf("idle select %s: %w", label, err)
	}

	idleCmd, err := c.Idle()
	if err != nil {
		return fmt.Errorf("idle start: %w", err)
	}
	defer func() { _ = idleCmd.Close() }()

	log.Printf("idle: watching %s", label)

	for {
		select {
		case <-ctx.Done():
			log.Printf("idle: stop watching %s", label)
			return nil
		case <-notify:
			// debounce bursts — a move produces many EXPUNGEs in quick
			// succession, collapse them into a single onChange.
			t := time.NewTimer(500 * time.Millisecond)
		drain:
			for {
				select {
				case <-notify:
				case <-t.C:
					break drain
				case <-ctx.Done():
					t.Stop()
					log.Printf("idle: stop watching %s", label)
					return nil
				}
			}
			onChange()
		}
	}
}

func nonBlockingSignal(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
