package compose

import "github.com/kungfusheep/glyph"

// stubs for wed types that depend on file browser, project indexing, and persistence

type BrowserEntry struct {
	Path         string
	AbsPath      string
	Title        string
	DisplayLabel string
	IsDir        bool
	IsSection    bool
	Section      *Section
	Level        int
	FileEntry    *FileEntry
	Expanded     bool
}

type Section struct {
	Heading string
	Line    int
	Level   int
}

type ProjectIndex struct{}

type FileEntry struct {
	Path    string
	RelPath string
	Title   string
	Name    string
}

type Config struct {
	FocusScope string
}

func LoadConfig() *Config { return &Config{FocusScope: "line"} }

func (c *Config) Save() error { return nil }

func FocusScopeFromString(s string) FocusScope {
	switch s {
	case "sentence":
		return FocusScopeSentence
	case "paragraph":
		return FocusScopeParagraph
	default:
		return FocusScopeLine
	}
}

// buildView stub - the real implementation lives in the main app
var buildView = func(ed *Editor) any { return nil }

// enterInsertMode stub - wired up by the main app via SetEnterInsertMode
var enterInsertMode = func(app *glyph.App, ed *Editor) {}

// SetEnterInsertMode allows the main app to wire the insert mode function
func SetEnterInsertMode(fn func(*glyph.App, *Editor)) {
	enterInsertMode = fn
}
