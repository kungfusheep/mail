package senderid

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kungfusheep/mail/cache"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

const (
	ConfidenceLow    = 20
	ConfidenceMedium = 50
	ConfidenceHigh   = 80
)

type Enricher struct {
	HTTPClient *http.Client
	LookupTXT  func(context.Context, string) ([]string, error)
	Now        func() time.Time
}

func DomainFromEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	domain := strings.ToLower(strings.TrimSpace(email[at+1:]))
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return ""
	}
	registrable, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err == nil && registrable != "" {
		return registrable
	}
	return domain
}

func (e Enricher) Enrich(ctx context.Context, domain string) cache.SenderIdentity {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return cache.SenderIdentity{}
	}

	identity := cache.SenderIdentity{
		Domain:     domain,
		Source:     "fallback",
		Confidence: ConfidenceLow,
		UpdatedAt:  e.now(),
	}

	if bimi := e.lookupBIMI(ctx, domain); bimi != "" {
		identity.BIMILogoURL = bimi
		identity.IconURL = bimi
		identity.Source = "bimi"
		identity.Confidence = ConfidenceHigh
	}

	home := e.homepage(ctx, domain)
	if identity.DisplayName == "" {
		identity.DisplayName = home.displayName
	}
	if identity.IconURL == "" {
		identity.IconURL = home.iconURL
	}
	if identity.ThemeColor == "" {
		identity.ThemeColor = home.themeColor
	}
	if identity.ThemeColor == "" && identity.IconURL != "" {
		identity.ThemeColor = e.iconColor(ctx, identity.IconURL)
	}
	identity.ColorCheckedAt = e.now()
	if identity.Source == "fallback" && (home.displayName != "" || home.iconURL != "") {
		identity.Source = "homepage"
		identity.Confidence = ConfidenceMedium
	}

	if identity.DisplayName == "" {
		identity.DisplayName = fallbackName(domain)
	}
	return identity
}

func (e Enricher) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Enricher) lookupBIMI(ctx context.Context, domain string) string {
	lookup := e.LookupTXT
	if lookup == nil {
		resolver := net.DefaultResolver
		lookup = resolver.LookupTXT
	}
	for _, name := range []string{"default._bimi." + domain, "_bimi." + domain} {
		records, err := lookup(ctx, name)
		if err != nil {
			continue
		}
		for _, record := range records {
			if logo := bimiLogo(record); logo != "" {
				return logo
			}
		}
	}
	return ""
}

var bimiLogoRE = regexp.MustCompile(`(?i)(?:^|;)\s*l\s*=\s*([^;]+)`)
var svgColorRE = regexp.MustCompile(`(?i)(?:\bfill|\bstroke)\s*(?:=|:)\s*["']?\s*(#[0-9a-f]{3,8})`)

func bimiLogo(record string) string {
	if !strings.Contains(strings.ToUpper(record), "V=BIMI1") {
		return ""
	}
	match := bimiLogoRE.FindStringSubmatch(record)
	if len(match) != 2 {
		return ""
	}
	logo := strings.TrimSpace(match[1])
	if logo == "" {
		return ""
	}
	if parsed, err := url.Parse(logo); err == nil && parsed.Scheme == "https" && parsed.Host != "" {
		return logo
	}
	return ""
}

type homepageIdentity struct {
	displayName string
	iconURL     string
	themeColor  string
}

func (e Enricher) homepage(ctx context.Context, domain string) homepageIdentity {
	client := e.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil)
	if err != nil {
		return homepageIdentity{}
	}
	req.Header.Set("User-Agent", "mail-sender-enrichment/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return homepageIdentity{}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return homepageIdentity{}
	}
	body := io.LimitReader(resp.Body, 512*1024)
	return parseHomepage(body, resp.Request.URL)
}

func parseHomepage(r io.Reader, base *url.URL) homepageIdentity {
	tokenizer := html.NewTokenizer(r)
	var out homepageIdentity
	var title strings.Builder
	inTitle := false

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			if out.displayName == "" {
				out.displayName = strings.TrimSpace(strings.Join(strings.Fields(title.String()), " "))
			}
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			switch token.Data {
			case "title":
				inTitle = true
			case "meta":
				key, content := metaKeyContent(token)
				switch strings.ToLower(key) {
				case "og:site_name", "application-name":
					if out.displayName == "" {
						out.displayName = strings.TrimSpace(content)
					}
				case "theme-color":
					if out.themeColor == "" {
						out.themeColor = strings.TrimSpace(content)
					}
				}
			case "link":
				if out.iconURL == "" {
					out.iconURL = iconHref(token, base)
				}
			}
		case html.EndTagToken:
			if tokenizer.Token().Data == "title" {
				inTitle = false
			}
		case html.TextToken:
			if inTitle {
				title.Write(tokenizer.Text())
			}
		}
	}
}

func metaKeyContent(token html.Token) (string, string) {
	var key, content string
	for _, attr := range token.Attr {
		switch strings.ToLower(attr.Key) {
		case "property", "name":
			if key == "" {
				key = attr.Val
			}
		case "content":
			content = attr.Val
		}
	}
	return key, content
}

func iconHref(token html.Token, base *url.URL) string {
	var rel, href string
	for _, attr := range token.Attr {
		switch strings.ToLower(attr.Key) {
		case "rel":
			rel = strings.ToLower(attr.Val)
		case "href":
			href = attr.Val
		}
	}
	if href == "" || !isIconRel(rel) {
		return ""
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func isIconRel(rel string) bool {
	for _, token := range strings.Fields(strings.ToLower(rel)) {
		switch token {
		case "icon", "shortcut icon", "apple-touch-icon", "apple-touch-icon-precomposed", "mask-icon":
			return true
		}
	}
	return false
}

func (e Enricher) iconColor(ctx context.Context, iconURL string) string {
	parsed, err := url.Parse(iconURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	client := e.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, iconURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "mail-sender-enrichment/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return ""
	}
	if color := dominantSVGColor(data); color != "" {
		return color
	}
	if color := dominantICOColor(data); color != "" {
		return color
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err == nil {
		return dominantIconColor(img)
	}
	return ""
}

func dominantSVGColor(data []byte) string {
	if !bytes.Contains(bytes.ToLower(data[:min(len(data), 256)]), []byte("<svg")) {
		return ""
	}
	matches := svgColorRE.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		if svgHasPaintShape(data) {
			return "#000000"
		}
		return ""
	}
	type candidate struct {
		color string
		score float64
		count int
	}
	candidates := map[string]*candidate{}
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		r, g, b, ok := parseSVGHexColor(string(match[1]))
		if !ok || isTransparentSVGColor(string(match[1])) || isNearWhite(r, g, b) {
			continue
		}
		key := fmt.Sprintf("#%02x%02x%02x", r, g, b)
		item := candidates[key]
		if item == nil {
			item = &candidate{color: key}
			candidates[key] = item
		}
		item.count++
		item.score += 0.25 + saturation(r, g, b)
	}
	var best *candidate
	for _, item := range candidates {
		if best == nil || item.score > best.score || (item.score == best.score && item.count > best.count) {
			best = item
		}
	}
	if best == nil {
		if svgHasPaintShape(data) {
			return "#000000"
		}
		return ""
	}
	return best.color
}

func svgHasPaintShape(data []byte) bool {
	lower := strings.ToLower(string(data))
	for _, tag := range []string{"<path", "<circle", "<ellipse", "<polygon", "<polyline", "<rect", "<text"} {
		if strings.Contains(lower, tag) {
			return true
		}
	}
	return false
}

func parseSVGHexColor(raw string) (uint8, uint8, uint8, bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if len(raw) == 3 || len(raw) == 4 {
		r, okR := hexNibble(raw[0])
		g, okG := hexNibble(raw[1])
		b, okB := hexNibble(raw[2])
		return r * 17, g * 17, b * 17, okR && okG && okB
	}
	if len(raw) == 6 || len(raw) == 8 {
		r, okR := hexByte(raw[0], raw[1])
		g, okG := hexByte(raw[2], raw[3])
		b, okB := hexByte(raw[4], raw[5])
		return r, g, b, okR && okG && okB
	}
	return 0, 0, 0, false
}

func isTransparentSVGColor(raw string) bool {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if len(raw) == 4 {
		a, ok := hexNibble(raw[3])
		return ok && a == 0
	}
	if len(raw) == 8 {
		a, ok := hexByte(raw[6], raw[7])
		return ok && a == 0
	}
	return false
}

func dominantICOColor(data []byte) string {
	img, ok := decodeFirstICOBGRA(data)
	if !ok {
		return ""
	}
	return dominantIconColor(img)
}

func decodeFirstICOBGRA(data []byte) (image.Image, bool) {
	if len(data) < 22 || binary.LittleEndian.Uint16(data[0:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil, false
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 {
		return nil, false
	}
	var bestW, bestOffset, bestSize int
	for i := range count {
		entry := 6 + i*16
		if entry+16 > len(data) {
			return nil, false
		}
		w := int(data[entry])
		h := int(data[entry+1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		size := int(binary.LittleEndian.Uint32(data[entry+8 : entry+12]))
		offset := int(binary.LittleEndian.Uint32(data[entry+12 : entry+16]))
		if offset < 0 || size <= 0 || offset+size > len(data) {
			continue
		}
		if w >= bestW {
			bestW = min(w, h)
			bestOffset = offset
			bestSize = size
		}
	}
	if bestSize == 0 {
		return nil, false
	}
	return decodeDIBBGRA(data[bestOffset : bestOffset+bestSize])
}

func decodeDIBBGRA(data []byte) (image.Image, bool) {
	if len(data) < 40 {
		return nil, false
	}
	headerSize := int(binary.LittleEndian.Uint32(data[0:4]))
	if headerSize < 40 || headerSize > len(data) {
		return nil, false
	}
	w := int(int32(binary.LittleEndian.Uint32(data[4:8])))
	hRaw := int(int32(binary.LittleEndian.Uint32(data[8:12])))
	planes := binary.LittleEndian.Uint16(data[12:14])
	bpp := binary.LittleEndian.Uint16(data[14:16])
	compression := binary.LittleEndian.Uint32(data[16:20])
	if w <= 0 || hRaw == 0 || planes != 1 || bpp != 32 || compression != 0 {
		return nil, false
	}
	h := hRaw / 2
	if h <= 0 {
		h = -hRaw
	}
	pixels := w * h * 4
	if headerSize+pixels > len(data) {
		return nil, false
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	topDown := hRaw < 0
	for y := range h {
		srcY := h - 1 - y
		if topDown {
			srcY = y
		}
		row := headerSize + srcY*w*4
		for x := range w {
			px := row + x*4
			img.SetNRGBA(x, y, color.NRGBA{B: data[px], G: data[px+1], R: data[px+2], A: data[px+3]})
		}
	}
	return img, true
}

func dominantIconColor(img image.Image) string {
	type bucket struct {
		r, g, b uint64
		score   float64
		count   uint64
	}
	bounds := img.Bounds()
	buckets := make(map[uint32]*bucket)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r16, g16, b16, a16 := img.At(x, y).RGBA()
			if a16 < 0x8000 {
				continue
			}
			r := uint8(r16 >> 8)
			g := uint8(g16 >> 8)
			b := uint8(b16 >> 8)
			minC := min3(r, g, b)
			maxC := max3(r, g, b)
			if maxC < 32 || minC > 240 {
				continue
			}
			sat := saturation(r, g, b)
			if sat < 0.18 {
				continue
			}
			key := quantizedColorKey(r, g, b)
			item := buckets[key]
			if item == nil {
				item = &bucket{}
				buckets[key] = item
			}
			item.r += uint64(r)
			item.g += uint64(g)
			item.b += uint64(b)
			item.count++
			item.score += sat * math.Sqrt(float64(maxC)/255)
		}
	}
	var best *bucket
	for _, item := range buckets {
		if best == nil || item.score > best.score {
			best = item
		}
	}
	if best == nil || best.count == 0 {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x",
		uint8(best.r/best.count),
		uint8(best.g/best.count),
		uint8(best.b/best.count),
	)
}

func isNearWhite(r, g, b uint8) bool {
	return min3(r, g, b) > 240
}

func quantizedColorKey(r, g, b uint8) uint32 {
	return uint32(r&0xf0)<<16 | uint32(g&0xf0)<<8 | uint32(b&0xf0)
}

func saturation(r, g, b uint8) float64 {
	minC := float64(min3(r, g, b))
	maxC := float64(max3(r, g, b))
	if maxC == 0 {
		return 0
	}
	return (maxC - minC) / maxC
}

func min3(a, b, c uint8) uint8 {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func max3(a, b, c uint8) uint8 {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}

func hexByte(hi, lo byte) (uint8, bool) {
	h, okH := hexNibble(hi)
	l, okL := hexNibble(lo)
	return h<<4 | l, okH && okL
}

func hexNibble(b byte) (uint8, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

func fallbackName(domain string) string {
	host := domain
	if i := strings.Index(host, "."); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return domain
	}
	return strings.ToUpper(host[:1]) + host[1:]
}
