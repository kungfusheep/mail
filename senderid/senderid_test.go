package senderid

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDomainFromEmailNormalizesRegistrableDomain(t *testing.T) {
	tests := map[string]string{
		"hello@email.starlingbank.com":                 "starlingbank.com",
		"communication@mail.insight.business.hsbc.com": "hsbc.com",
		"order-update@amazon.co.uk":                    "amazon.co.uk",
		"bad":                                          "",
	}
	for email, want := range tests {
		if got := DomainFromEmail(email); got != want {
			t.Fatalf("DomainFromEmail(%q) = %q, want %q", email, got, want)
		}
	}
}

func TestBIMILogoExtractsHTTPSLogo(t *testing.T) {
	got := bimiLogo(`v=BIMI1; l=https://example.com/logo.svg; a=https://example.com/cert.pem`)
	if got != "https://example.com/logo.svg" {
		t.Fatalf("bimi logo = %q, want https logo", got)
	}
	if got := bimiLogo(`v=BIMI1; l=http://example.com/logo.svg`); got != "" {
		t.Fatalf("insecure bimi logo = %q, want empty", got)
	}
}

func TestEnrichPrefersBIMIAndFillsHomepageHints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head>
			<title>Example App</title>
			<meta property="og:site_name" content="Example">
			<meta name="theme-color" content="#123456">
			<link rel="icon" href="/favicon.svg">
		</head></html>`))
	}))
	defer server.Close()

	base, _ := url.Parse("https://example.com/")
	client := server.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return nil }
	enricher := Enricher{
		HTTPClient: client,
		LookupTXT: func(ctx context.Context, name string) ([]string, error) {
			return []string{`v=BIMI1; l=https://example.com/bimi.svg`}, nil
		},
		Now: func() time.Time { return time.Unix(100, 0) },
	}
	identity := enricher.Enrich(context.Background(), "example.com")
	if identity.Source != "bimi" || identity.Confidence != ConfidenceHigh {
		t.Fatalf("identity source/confidence = %s/%d, want bimi/high", identity.Source, identity.Confidence)
	}
	if identity.BIMILogoURL != "https://example.com/bimi.svg" || identity.IconURL != "https://example.com/bimi.svg" {
		t.Fatalf("identity bimi/icon = %q/%q, want bimi logo", identity.BIMILogoURL, identity.IconURL)
	}

	home := parseHomepage(strings.NewReader(`<html><head>
		<meta property="og:site_name" content="Example">
		<meta name="theme-color" content="#123456">
		<link rel="icon" href="/favicon.svg">
	</head></html>`), base)
	if home.displayName != "Example" || home.themeColor != "#123456" || home.iconURL != "https://example.com/favicon.svg" {
		t.Fatalf("homepage = %#v, want metadata and absolute icon", home)
	}
}

func TestParseHomepageIgnoresNoopenerLinks(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	home := parseHomepage(strings.NewReader(`<html><head>
		<link rel="noopener" href="https://example.com/not-an-icon">
		<link rel="preconnect" href="https://cdn.example.com">
		<link rel="apple-touch-icon" href="/touch.png">
	</head></html>`), base)

	if home.iconURL != "https://example.com/touch.png" {
		t.Fatalf("iconURL = %q, want apple touch icon and not noopener/preconnect", home.iconURL)
	}
}

func TestDominantIconColorPrefersSaturatedLogoColor(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
		}
	}
	for y := 3; y < 13; y++ {
		for x := 3; x < 13; x++ {
			img.Set(x, y, color.NRGBA{R: 38, G: 132, B: 255, A: 255})
		}
	}

	if got := dominantIconColor(img); got != "#2684ff" {
		t.Fatalf("dominantIconColor = %q, want saturated blue", got)
	}
}

func TestDominantIconColorIgnoresMonochromeIcons(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.NRGBA{R: 20, G: 20, B: 20, A: 255})
		}
	}

	if got := dominantIconColor(img); got != "" {
		t.Fatalf("dominantIconColor = %q, want no useful colour", got)
	}
}

func TestIconColorSamplesRemoteBitmapIcon(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	for y := 1; y < 3; y++ {
		for x := 1; x < 3; x++ {
			img.Set(x, y, color.NRGBA{R: 255, G: 53, B: 51, A: 255})
		}
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		if err := png.Encode(w, img); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	enricher := Enricher{HTTPClient: server.Client()}
	if got := enricher.iconColor(context.Background(), server.URL); got != "#ff3533" {
		t.Fatalf("iconColor = %q, want sampled red", got)
	}
}

func TestDominantSVGColorSamplesPaintColours(t *testing.T) {
	svg := []byte(`<svg viewBox="0 0 32 32">
		<rect width="32" height="32" fill="#ffffff"/>
		<path fill="#533AFD" d="M0 0h16v16H0z"/>
	</svg>`)

	if got := dominantSVGColor(svg); got != "#533afd" {
		t.Fatalf("dominantSVGColor = %q, want stripe purple", got)
	}
}

func TestDominantSVGColorAllowsMonochromeMarks(t *testing.T) {
	svg := []byte(`<svg viewBox="0 0 32 32">
		<rect width="32" height="32" fill="#fff"/>
		<path style="fill:#1d1d1f" d="M0 0h16v16H0z"/>
	</svg>`)

	if got := dominantSVGColor(svg); got != "#1d1d1f" {
		t.Fatalf("dominantSVGColor = %q, want dark mark", got)
	}
}

func TestDominantSVGColorUsesDefaultBlackPaint(t *testing.T) {
	svg := []byte(`<svg viewBox="0 0 32 32">
		<path d="M0 0h16v16H0z"/>
	</svg>`)

	if got := dominantSVGColor(svg); got != "#000000" {
		t.Fatalf("dominantSVGColor = %q, want implicit black paint", got)
	}
}

func TestDominantICOColorSamplesBGRAIcon(t *testing.T) {
	ico := testICO(t, color.NRGBA{R: 255, G: 53, B: 51, A: 255})

	if got := dominantICOColor(ico); got != "#ff3533" {
		t.Fatalf("dominantICOColor = %q, want sampled red", got)
	}
}

func testICO(t *testing.T, c color.NRGBA) []byte {
	t.Helper()
	const w, h = 2, 2
	dib := new(bytes.Buffer)
	if err := binary.Write(dib, binary.LittleEndian, uint32(40)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(dib, binary.LittleEndian, int32(w)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(dib, binary.LittleEndian, int32(h*2)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(dib, binary.LittleEndian, uint16(1)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(dib, binary.LittleEndian, uint16(32)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(dib, binary.LittleEndian, uint32(0)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(dib, binary.LittleEndian, uint32(w*h*4)); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err := binary.Write(dib, binary.LittleEndian, int32(0)); err != nil {
			t.Fatal(err)
		}
	}
	for range w * h {
		dib.Write([]byte{c.B, c.G, c.R, c.A})
	}

	out := new(bytes.Buffer)
	if err := binary.Write(out, binary.LittleEndian, uint16(0)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(out, binary.LittleEndian, uint16(1)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(out, binary.LittleEndian, uint16(1)); err != nil {
		t.Fatal(err)
	}
	out.Write([]byte{w, h, 0, 0})
	if err := binary.Write(out, binary.LittleEndian, uint16(1)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(out, binary.LittleEndian, uint16(32)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(out, binary.LittleEndian, uint32(dib.Len())); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(out, binary.LittleEndian, uint32(22)); err != nil {
		t.Fatal(err)
	}
	out.Write(dib.Bytes())
	return out.Bytes()
}
