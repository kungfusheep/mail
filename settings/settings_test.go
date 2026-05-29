package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingSettingsReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	got, err := LoadAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != (Settings{}) {
		t.Fatalf("settings = %#v, want empty", got)
	}
}

func TestSaveAndLoadSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail", "settings.json")

	if err := SaveAt(path, Settings{Theme: "mfd-flir-fusion", Signature: "Pete"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != "mfd-flir-fusion" {
		t.Fatalf("theme = %q, want mfd-flir-fusion", got.Theme)
	}
	if got.Signature != "Pete" {
		t.Fatalf("signature = %q, want Pete", got.Signature)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("settings perms = %v, want 0600", got)
	}
}
