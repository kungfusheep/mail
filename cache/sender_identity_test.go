package cache

import (
	"testing"
	"time"
)

func TestSenderIdentityRoundTrip(t *testing.T) {
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	identity := SenderIdentity{
		Domain:         "example.com",
		DisplayName:    "Example",
		IconURL:        "https://example.com/icon.svg",
		ThemeColor:     "#123456",
		BIMILogoURL:    "https://example.com/bimi.svg",
		Source:         "bimi",
		Confidence:     80,
		ColorCheckedAt: time.Unix(90, 0),
		UpdatedAt:      time.Unix(100, 0),
	}
	if err := c.PutSenderIdentity(identity); err != nil {
		t.Fatalf("PutSenderIdentity: %v", err)
	}

	got, ok, err := c.SenderIdentity("example.com")
	if err != nil {
		t.Fatalf("SenderIdentity: %v", err)
	}
	if !ok {
		t.Fatal("SenderIdentity found = false, want true")
	}
	if got.Domain != identity.Domain || got.DisplayName != identity.DisplayName ||
		got.IconURL != identity.IconURL || got.ThemeColor != identity.ThemeColor ||
		got.BIMILogoURL != identity.BIMILogoURL || got.Source != identity.Source ||
		got.Confidence != identity.Confidence ||
		!got.ColorCheckedAt.Equal(identity.ColorCheckedAt) ||
		!got.UpdatedAt.Equal(identity.UpdatedAt) {
		t.Fatalf("identity = %#v, want %#v", got, identity)
	}
}

func TestSenderIdentityFresh(t *testing.T) {
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if fresh, err := c.SenderIdentityFresh("missing.test", time.Hour); err != nil || fresh {
		t.Fatalf("missing fresh = %v, err=%v, want false nil", fresh, err)
	}
	if err := c.PutSenderIdentity(SenderIdentity{Domain: "old.test", UpdatedAt: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatalf("PutSenderIdentity: %v", err)
	}
	if fresh, err := c.SenderIdentityFresh("old.test", time.Hour); err != nil || fresh {
		t.Fatalf("old fresh = %v, err=%v, want false nil", fresh, err)
	}
}

func TestDeleteSenderIdentity(t *testing.T) {
	c, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.PutSenderIdentity(SenderIdentity{Domain: "example.com", DisplayName: "Example"}); err != nil {
		t.Fatalf("PutSenderIdentity: %v", err)
	}
	if err := c.DeleteSenderIdentity("example.com"); err != nil {
		t.Fatalf("DeleteSenderIdentity: %v", err)
	}
	if _, ok, err := c.SenderIdentity("example.com"); err != nil || ok {
		t.Fatalf("SenderIdentity after delete found = %v, err=%v, want false nil", ok, err)
	}
}
