package cache

import "sync"

// pubsub is a tiny in-process notifier keyed by label. Writers call publish
// after mutating a label's threads; subscribers receive a coalesced signal
// (the channel is buffered to 1 — bursts are collapsed). Zero value is
// ready to use.
type pubsub struct {
	mu    sync.Mutex
	byLbl map[string][]chan struct{}
}

func (p *pubsub) subscribe(label string) (<-chan struct{}, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byLbl == nil {
		p.byLbl = make(map[string][]chan struct{})
	}
	ch := make(chan struct{}, 1)
	p.byLbl[label] = append(p.byLbl[label], ch)
	return ch, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		cs := p.byLbl[label]
		for i, c := range cs {
			if c == ch {
				p.byLbl[label] = append(cs[:i], cs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

func (p *pubsub) publish(label string) {
	p.mu.Lock()
	cs := append([]chan struct{}{}, p.byLbl[label]...)
	p.mu.Unlock()
	for _, ch := range cs {
		// non-blocking: if receiver hasn't drained, the existing signal
		// will cover the update we're about to drop.
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Subscribe registers for change notifications on a label. The returned
// channel receives a send whenever a cache write touches that label. The
// returned unsubscribe function must be called to release the subscription.
func (c *Cache) Subscribe(label string) (<-chan struct{}, func()) {
	return c.subs.subscribe(label)
}

// labelsForThread returns all labels associated with a thread — used so
// writers that don't know the set of affected labels (e.g. PutThread) can
// still fan out notifications correctly.
func (c *Cache) labelsForThread(threadID string) []string {
	rows, err := c.db.Query("SELECT label FROM thread_labels WHERE thread_id = ?", threadID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err == nil {
			out = append(out, l)
		}
	}
	return out
}
