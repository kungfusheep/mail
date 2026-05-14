package ui

import "time"

type NotificationKind int

const (
	NotificationInfo NotificationKind = iota
	NotificationSuccess
	NotificationWarning
	NotificationError
	NotificationAction
)

type Notification struct {
	Text    string
	Kind    NotificationKind
	Opacity float64
}

// Feed is used to display the notification feed we show in the bottom
// right corner
type Feed struct {
	items []entry
	view  []Notification
	now   func() time.Time
	ttl   time.Duration
	fade  time.Duration
	limit int
}

type entry struct {
	text string
	kind NotificationKind
	at   time.Time
}

func NewFeed(now func() time.Time) *Feed {
	if now == nil {
		now = time.Now
	}
	return &Feed{
		now:   now,
		ttl:   2800 * time.Millisecond,
		fade:  700 * time.Millisecond,
		limit: 6,
	}
}

func (f *Feed) Push(text string) {
	f.PushKind(NotificationInfo, text)
}

func (f *Feed) PushKind(kind NotificationKind, text string) {
	if text == "" {
		return
	}
	f.items = append(f.items, entry{text: text, kind: kind, at: f.now()})
	if len(f.items) > f.limit {
		copy(f.items, f.items[len(f.items)-f.limit:])
		f.items = f.items[:f.limit]
	}
	f.Update()
}

func (f *Feed) Update() {
	now := f.now()
	keep := f.items[:0]
	f.view = f.view[:0]
	for _, item := range f.items {
		age := now.Sub(item.at)
		if age >= f.ttl {
			continue
		}
		keep = append(keep, item)
		f.view = append(f.view, Notification{
			Text:    item.text,
			Kind:    item.kind,
			Opacity: f.opacity(age),
		})
	}
	f.items = keep
}

func (f *Feed) Items() *[]Notification {
	return &f.view
}

func (f *Feed) opacity(age time.Duration) float64 {
	fadeStart := f.ttl - f.fade
	if age <= fadeStart {
		return 1
	}
	if age >= f.ttl {
		return 0
	}
	return float64(f.ttl-age) / float64(f.fade)
}
