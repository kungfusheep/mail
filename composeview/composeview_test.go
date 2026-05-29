package composeview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/mail/cache"
	"github.com/kungfusheep/mail/compose"
	"github.com/kungfusheep/mail/mailbox"
	"github.com/kungfusheep/mail/provider"
	"github.com/kungfusheep/mail/theme"
	"github.com/kungfusheep/mail/transition"
)

func TestSetupReplyRepairsBlankDraftHeaders(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.PutDraft(cache.Draft{
		ThreadID: "thread-1",
		Body:     "existing reply body",
	}); err != nil {
		t.Fatalf("PutDraft: %v", err)
	}

	controls := setupTestCompose(t, db)
	controls.SetupReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:        "message-1",
			MessageID: "<message-1@example.test>",
			From:      provider.Address{Name: "Leica Store Manchester", Email: "hello@leicastoremanchester.com"},
			Subject:   "Leica Q343",
			TextBody:  "original message",
		}},
	})

	got, ok, err := db.GetDraft("thread-1")
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	if !ok {
		t.Fatal("draft missing after reply setup")
	}
	if got.To != "Leica Store Manchester <hello@leicastoremanchester.com>" {
		t.Fatalf("draft To = %q", got.To)
	}
	if got.Subject != "Re: Leica Q343" {
		t.Fatalf("draft Subject = %q", got.Subject)
	}
	if got.Body != "existing reply body\n" {
		t.Fatalf("draft Body = %q", got.Body)
	}
}

func TestSetupReplyPreservesExistingDraftHeaders(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.PutDraft(cache.Draft{
		ThreadID: "thread-1",
		To:       "custom@example.test",
		Subject:  "custom subject",
		Body:     "existing reply body",
	}); err != nil {
		t.Fatalf("PutDraft: %v", err)
	}

	controls := setupTestCompose(t, db)
	controls.SetupReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:      "message-1",
			From:    provider.Address{Name: "Leica Store Manchester", Email: "hello@leicastoremanchester.com"},
			Subject: "Leica Q343",
		}},
	})

	got, ok, err := db.GetDraft("thread-1")
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	if !ok {
		t.Fatal("draft missing after reply setup")
	}
	if got.To != "custom@example.test" {
		t.Fatalf("draft To = %q", got.To)
	}
	if got.Subject != "custom subject" {
		t.Fatalf("draft Subject = %q", got.Subject)
	}
}

func TestSetupReplyStartsWithBlankBody(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	palette := theme.Dark()
	app := NewApp()
	ed := compose.NewEditor(compose.NewDocument(), "")
	controls := Setup(
		app,
		ed,
		mailbox.NewState(db, "me@example.test"),
		nil,
		db,
		func(string) {},
		new(int),
		transition.New(time.Millisecond, palette.BG, Hex(0x3a3a3a)),
		palette,
		"",
	)

	controls.SetupReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:       "message-1",
			From:     provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject:  "Hello",
			TextBody: "original message should not be inserted",
		}},
	})

	if got := ed.Markdown(); strings.Contains(got, "original message") {
		t.Fatalf("reply editor body contains original message:\n%s", got)
	}
}

func TestSetupReplyAllStartsWithBlankBody(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	palette := theme.Dark()
	app := NewApp()
	ed := compose.NewEditor(compose.NewDocument(), "")
	controls := Setup(
		app,
		ed,
		mailbox.NewState(db, "me@example.test"),
		nil,
		db,
		func(string) {},
		new(int),
		transition.New(time.Millisecond, palette.BG, Hex(0x3a3a3a)),
		palette,
		"",
	)

	controls.SetupReplyAll(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:       "message-1",
			From:     provider.Address{Name: "Alice", Email: "alice@example.com"},
			To:       []provider.Address{{Email: "me@example.test"}},
			Subject:  "Hello",
			TextBody: "original message should not be inserted",
		}},
	})

	if got := ed.Markdown(); strings.Contains(got, "original message") {
		t.Fatalf("reply-all editor body contains original message:\n%s", got)
	}
}

func TestComposeSignatureSeedsNewDrafts(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	palette := theme.Dark()
	app := NewApp()
	ed := compose.NewEditor(compose.NewDocument(), "")
	controls := Setup(
		app,
		ed,
		mailbox.NewState(db, "me@example.test"),
		nil,
		db,
		func(string) {},
		new(int),
		transition.New(time.Millisecond, palette.BG, Hex(0x3a3a3a)),
		palette,
		"Pete Griffiths",
	)

	controls.Open()

	if got := ed.Markdown(); !strings.Contains(got, "Pete Griffiths") {
		t.Fatalf("new compose body = %q, want configured signature", got)
	}
}

func TestComposeSignatureDoesNotOverwriteExistingReplyDraft(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.PutDraft(cache.Draft{
		ThreadID: "thread-1",
		To:       "alice@example.com",
		Subject:  "Re: Hello",
		Body:     "existing reply body",
	}); err != nil {
		t.Fatal(err)
	}

	palette := theme.Dark()
	app := NewApp()
	ed := compose.NewEditor(compose.NewDocument(), "")
	controls := Setup(
		app,
		ed,
		mailbox.NewState(db, "me@example.test"),
		nil,
		db,
		func(string) {},
		new(int),
		transition.New(time.Millisecond, palette.BG, Hex(0x3a3a3a)),
		palette,
		"Pete Griffiths",
	)

	controls.SetupReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:      "message-1",
			From:    provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject: "Hello",
		}},
	})

	got := ed.Markdown()
	if !strings.Contains(got, "existing reply body") {
		t.Fatalf("reply draft body = %q, want existing body", got)
	}
	if strings.Contains(got, "Pete Griffiths") {
		t.Fatalf("reply draft body = %q, existing draft should not gain duplicate signature", got)
	}
}

func TestReplySubject(t *testing.T) {
	if got := replySubject("Leica Q343"); got != "Re: Leica Q343" {
		t.Fatalf("replySubject = %q", got)
	}
	if got := replySubject("Re: Leica Q343"); got != "Re: Leica Q343" {
		t.Fatalf("replySubject duplicated prefix: %q", got)
	}
}

func TestForwardSubject(t *testing.T) {
	if got := forwardSubject("Invoice"); got != "Fwd: Invoice" {
		t.Fatalf("forwardSubject = %q, want Fwd prefix", got)
	}
	if got := forwardSubject("Fwd: Invoice"); got != "Fwd: Invoice" {
		t.Fatalf("forwardSubject duplicated prefix: %q", got)
	}
}

func TestForwardedBodyIncludesMessageHeaders(t *testing.T) {
	body := forwardedBody(provider.Message{
		From:     provider.Address{Name: "Alice", Email: "alice@example.com"},
		To:       []provider.Address{{Name: "Pete", Email: "pete@example.com"}},
		Subject:  "Invoice",
		Date:     time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC),
		TextBody: "hello",
	})

	for _, want := range []string{
		"---------- Forwarded message ----------",
		"From: Alice <alice@example.com>",
		"Date: 27 May 2026 14:30",
		"Subject: Invoice",
		"To: Pete <pete@example.com>",
		"hello",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("forwardedBody missing %q in:\n%s", want, body)
		}
	}
}

func TestReplyAllHeadersDedupesAndExcludesMe(t *testing.T) {
	to, cc := replyAllHeaders(provider.Message{
		From: provider.Address{Name: "Alice", Email: "alice@example.com"},
		To: []provider.Address{
			{Name: "Me", Email: "me@example.com"},
			{Name: "Bob", Email: "bob@example.com"},
		},
		CC: []provider.Address{
			{Name: "Bob", Email: "bob@example.com"},
			{Name: "Carol", Email: "carol@example.com"},
		},
	}, "me@example.com")

	if to != "Alice <alice@example.com>" {
		t.Fatalf("reply-all to = %q, want Alice", to)
	}
	if cc != "Bob <bob@example.com>, Carol <carol@example.com>" {
		t.Fatalf("reply-all cc = %q, want Bob and Carol", cc)
	}
}

func TestReplyAllHeadersWhenIAmSender(t *testing.T) {
	to, cc := replyAllHeaders(provider.Message{
		From: provider.Address{Name: "Me", Email: "me@example.com"},
		To: []provider.Address{
			{Name: "Alice", Email: "alice@example.com"},
		},
		CC: []provider.Address{
			{Name: "Carol", Email: "carol@example.com"},
		},
	}, "me@example.com")

	if to != "Alice <alice@example.com>" {
		t.Fatalf("reply-all sent-message to = %q, want Alice", to)
	}
	if cc != "Carol <carol@example.com>" {
		t.Fatalf("reply-all sent-message cc = %q, want Carol", cc)
	}
}

func TestLocalAttachmentCapturesFileMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invoice.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4"), 0600); err != nil {
		t.Fatal(err)
	}

	attachment, err := localAttachment(path)
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Filename != "invoice.pdf" {
		t.Fatalf("filename = %q, want invoice.pdf", attachment.Filename)
	}
	if attachment.ContentType != "application/pdf" {
		t.Fatalf("content type = %q, want application/pdf", attachment.ContentType)
	}
	if attachment.Size != 8 {
		t.Fatalf("size = %d, want 8", attachment.Size)
	}
	if attachment.LocalPath != path {
		t.Fatalf("local path = %q, want %q", attachment.LocalPath, path)
	}
}

func TestLocalAttachmentRejectsDirectories(t *testing.T) {
	_, err := localAttachment(t.TempDir())
	if err == nil {
		t.Fatal("localAttachment err = nil, want directory error")
	}
}

func TestComposeAttachmentRowUsesFileTypeIcon(t *testing.T) {
	row := composeAttachmentRow(provider.Attachment{
		Filename:    "invite.ics",
		ContentType: "text/calendar",
	})
	if row != "󰃭 invite.ics" {
		t.Fatalf("composeAttachmentRow = %q, want calendar icon row", row)
	}
}

func TestInlineReplyRendersOverlayControl(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, controls := setupTestComposeApp(t, db)
	controls.OpenInlineReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:      "message-1",
			From:    provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject: "Hello",
		}},
	})

	ref := NodeRef{X: 0, Y: 0, W: 90, H: 24}
	buf := NewBuffer(100, 30)
	Build(VBox(controls.InlineView(&ref))).Execute(buf, 100, 30)

	out := buf.String()
	if !strings.Contains(out, "reply") {
		t.Fatalf("inline reply overlay missing title:\n%s", out)
	}
	if !strings.Contains(out, "Alice <alice@example.com>") {
		t.Fatalf("inline reply overlay missing recipient:\n%s", out)
	}
	if !strings.Contains(out, "c-o full") {
		t.Fatalf("inline reply overlay missing full-compose shortcut:\n%s", out)
	}
}

func TestInlineReplyCursorComesFromOverlayLayer(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	app, controls := setupTestComposeApp(t, db)
	controls.OpenInlineReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:      "message-1",
			From:    provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject: "Hello",
		}},
	})

	ref := NodeRef{X: 30, Y: 0, W: 50, H: 20}
	app.SetView(VBox(controls.InlineView(&ref)))
	app.RenderNow()

	cursor := app.Cursor()
	if !cursor.Visible {
		t.Fatalf("inline reply cursor is not visible")
	}
	if cursor.X <= ref.X {
		t.Fatalf("inline reply cursor x = %d, want it translated into overlay beyond x=%d", cursor.X, ref.X)
	}
}

func TestComposeControlsApplyThemeUpdatesInlineReplyChrome(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, controls := setupTestComposeApp(t, db)
	controls.OpenInlineReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:      "message-1",
			From:    provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject: "Hello",
		}},
	})
	controls.ApplyTheme(theme.Light())

	ref := NodeRef{X: 0, Y: 0, W: 90, H: 24}
	buf := NewBuffer(100, 30)
	Build(VBox(controls.InlineView(&ref))).Execute(buf, 100, 30)

	x, y := findText(buf, "reply")
	if x < 0 {
		t.Fatalf("inline reply overlay missing title:\n%s", buf.String())
	}
	if got := buf.Get(x, y).Style.FG; got != theme.Light().Bright {
		t.Fatalf("reply title fg = %v, want light bright %v\n%s", got, theme.Light().Bright, buf.String())
	}
}

func TestFullReplyCanToggleBackToInlineReply(t *testing.T) {
	db, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	palette := theme.Dark()
	app := NewApp()
	ed := compose.NewEditor(compose.NewDocument(), "")
	controls := Setup(
		app,
		ed,
		mailbox.NewState(db, "me@example.test"),
		nil,
		db,
		func(string) {},
		new(int),
		transition.New(time.Millisecond, palette.BG, Hex(0x3a3a3a)),
		palette,
		"",
	)
	controls.Open()
	controls.SetupReply(provider.Thread{
		ID: "thread-1",
		Messages: []provider.Message{{
			ID:      "message-1",
			From:    provider.Address{Name: "Alice", Email: "alice@example.com"},
			Subject: "Hello",
		}},
	})
	ed.LoadMarkdown("draft reply body")

	controls.ToggleInline()

	ref := NodeRef{X: 0, Y: 0, W: 90, H: 24}
	buf := NewBuffer(100, 30)
	Build(VBox(controls.InlineView(&ref))).Execute(buf, 100, 30)

	out := buf.String()
	if !strings.Contains(out, "reply") {
		t.Fatalf("inline reply did not render after full-compose toggle:\n%s", out)
	}
	if !strings.Contains(out, "Alice <alice@example.com>") {
		t.Fatalf("inline reply lost recipient after full-compose toggle:\n%s", out)
	}
}

func setupTestCompose(t *testing.T, db *cache.Cache) mailbox.ComposeControls {
	_, controls := setupTestComposeApp(t, db)
	return controls
}

func findText(buf *Buffer, text string) (int, int) {
	for y := 0; y < buf.Height(); y++ {
		line := buf.GetLine(y)
		for x := 0; x+len(text) <= len(line); x++ {
			if line[x:x+len(text)] == text {
				return x, y
			}
		}
	}
	return -1, -1
}

func setupTestComposeApp(t *testing.T, db *cache.Cache) (*App, mailbox.ComposeControls) {
	t.Helper()
	palette := theme.Dark()
	app := NewApp()
	ed := compose.NewEditor(compose.NewDocument(), "")
	ed.SetTheme(theme.ComposeTheme(palette))
	return app, Setup(
		app,
		ed,
		mailbox.NewState(db, "me@example.test"),
		nil,
		db,
		func(string) {},
		new(int),
		transition.New(time.Millisecond, palette.BG, Hex(0x3a3a3a)),
		palette,
		"",
	)
}
