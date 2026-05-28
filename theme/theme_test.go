package theme

import (
	"testing"

	"github.com/kungfusheep/glyph"
)

func TestMFDThemesUseTrueRGBColors(t *testing.T) {
	blackout, ok := ByName("mfd-blackout")
	if !ok {
		t.Fatal("mfd-blackout theme missing")
	}

	if blackout.BG.Mode != glyph.ColorRGB {
		t.Fatalf("blackout bg mode = %v, want true RGB so terminal ANSI black remaps cannot leak in", blackout.BG.Mode)
	}
	if blackout.BG.R != 0 || blackout.BG.G != 0 || blackout.BG.B != 0 {
		t.Fatalf("blackout bg = rgb(%d,%d,%d), want rgb(0,0,0)", blackout.BG.R, blackout.BG.G, blackout.BG.B)
	}

	for _, named := range All() {
		if !isMFDThemeName(named.Name) {
			continue
		}
		if named.Palette.BG.Mode != glyph.ColorRGB {
			t.Fatalf("%s bg mode = %v, want true RGB", named.Name, named.Palette.BG.Mode)
		}
		if named.Palette.FG.Mode != glyph.ColorRGB {
			t.Fatalf("%s fg mode = %v, want true RGB", named.Name, named.Palette.FG.Mode)
		}
		if named.Palette.SelBG.Mode != glyph.ColorRGB {
			t.Fatalf("%s selection bg mode = %v, want true RGB", named.Name, named.Palette.SelBG.Mode)
		}
	}
}

func isMFDThemeName(name string) bool {
	return name == "mfd" || len(name) > 4 && name[:4] == "mfd-"
}
