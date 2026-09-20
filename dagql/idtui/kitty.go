package idtui

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/vito/tuist"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	kittyMaxColumns    = 80
	kittyMaxRows       = 24
	kittyCellWidth     = 8
	kittyCellHeight    = 16
	kittyMaxInput      = 4 << 20
	kittyMaxPixels     = 8 << 20
	kittyMaxDimension  = 8192
	kittyMaxImages     = 64
	kittyMaxCacheBytes = 16 << 20
	kittyMaxAttempts   = 256
	kittyChunkSize     = 4096
	kittyMarkerPrefix  = "\x1b]777;dagger-image;"
	kittyPlaceholder   = '\U0010eeee'
)

// kittyDiacritics is the first 80 entries of the Kitty graphics protocol's
// rowcolumn-diacritics.txt, not a contiguous range of Unicode combining marks.
var kittyDiacritics = [...]rune{
	0x0305, 0x030d, 0x030e, 0x0310, 0x0312, 0x033d, 0x033e, 0x033f,
	0x0346, 0x034a, 0x034b, 0x034c, 0x0350, 0x0351, 0x0352, 0x0357,
	0x035b, 0x0363, 0x0364, 0x0365, 0x0366, 0x0367, 0x0368, 0x0369,
	0x036a, 0x036b, 0x036c, 0x036d, 0x036e, 0x036f, 0x0483, 0x0484,
	0x0485, 0x0486, 0x0487, 0x0592, 0x0593, 0x0594, 0x0595, 0x0597,
	0x0598, 0x0599, 0x059c, 0x059d, 0x059e, 0x059f, 0x05a0, 0x05a1,
	0x05a8, 0x05a9, 0x05ab, 0x05ac, 0x05af, 0x05c4, 0x0610, 0x0611,
	0x0612, 0x0613, 0x0614, 0x0615, 0x0616, 0x0617, 0x0657, 0x0658,
	0x0659, 0x065a, 0x065b, 0x065d, 0x065e, 0x06d6, 0x06d7, 0x06d8,
	0x06d9, 0x06da, 0x06db, 0x06dc, 0x06df, 0x06e0, 0x06e1, 0x06e2,
}

// kittyImagesEnabled only checks the requested protocol. The caller must also
// require an interactive, real terminal (never a plain/headless/ASCII reporter).
// Explicit opt-in permits multiplexers, but does not implement their passthrough.
func kittyImagesEnabled(getenv func(string) string) bool {
	switch getenv("DAGGER_TUI_IMAGES") {
	case "kitty":
		return true
	case "":
		return getenv("TERM") == "xterm-kitty" && getenv("TMUX") == "" && getenv("STY") == ""
	default:
		return false
	}
}

type kittyImage struct {
	id            uint32
	payload       string // bounded PNG, base64 encoded
	columns, rows int
	lines         []string
	uploaded      bool
}

// Entries are never evicted: tuist may retain rows referencing them. Once a
// budget is exhausted new images fall back to text. Invalid inputs are memoized
// too, so repainting a corrupt image does not repeatedly run a decoder.
// The mutex serializes decoding and terminal uploads, bounding peak decode memory.
type kittyImages struct {
	mu         sync.Mutex
	active     bool
	nonce      string
	cache      map[[32]byte]*kittyImage
	byID       map[uint32]*kittyImage
	cacheBytes int
}

func newKittyImages() *kittyImages {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil
	}
	return &kittyImages{
		nonce: hex.EncodeToString(nonce[:]),
		cache: make(map[[32]byte]*kittyImage),
		byID:  make(map[uint32]*kittyImage),
	}
}

// render produces ordinary, one-cell-wide Unicode placeholders, never graphics
// commands. Every row has its own private OSC marker, so even if the first rows
// are clipped off, the first surviving row uploads the image at terminal Write.
// Explicit coordinates on every cell also support horizontal clipping.
// A nil or stopped manager always falls back, including final text reports.
func (k *kittyImages) render(data, mimeType string, width int) ([]string, bool) {
	if k == nil || width <= 0 || len(data) == 0 || len(data) > base64.StdEncoding.EncodedLen(kittyMaxInput) {
		return nil, false
	}
	format := map[string]string{"image/png": "png", "image/jpeg": "jpeg", "image/gif": "gif", "image/webp": "webp"}[mimeType]
	if format == "" {
		return nil, false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.active {
		return nil, false
	}
	width = min(width, kittyMaxColumns)
	h := sha256.New()
	fmt.Fprintf(h, "%s:%d:", mimeType, width)
	io.WriteString(h, data)
	key := [32]byte(h.Sum(nil))
	if entry, ok := k.cache[key]; ok {
		if entry == nil {
			return nil, false
		}
		return append([]string(nil), entry.lines...), true
	}
	if len(k.cache) >= kittyMaxAttempts || len(k.byID) >= kittyMaxImages || k.cacheBytes >= kittyMaxCacheBytes {
		return nil, false
	}
	k.cache[key] = nil
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) > kittyMaxInput {
		return nil, false
	}
	// DecodeConfig MUST precede Decode. Compressed inputs may describe enormous
	// allocations even though the encoded payload is small.
	cfg, actualFormat, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || actualFormat != format || cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > kittyMaxDimension || cfg.Height > kittyMaxDimension ||
		int64(cfg.Width)*int64(cfg.Height) > kittyMaxPixels {
		return nil, false
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	scale := min(1.0, float64(width*kittyCellWidth)/float64(cfg.Width), float64(kittyMaxRows*kittyCellHeight)/float64(cfg.Height))
	w, height := max(1, int(float64(cfg.Width)*scale)), max(1, int(float64(cfg.Height)*scale))
	cols, rows := (w+kittyCellWidth-1)/kittyCellWidth, (height+kittyCellHeight-1)/kittyCellHeight
	// Pad partial cells transparently instead of stretching small images up to
	// a cell boundary. For GIFs, image.Decode returns only the first frame.
	dst := image.NewNRGBA(image.Rect(0, 0, cols*kittyCellWidth, rows*kittyCellHeight))
	draw.ApproxBiLinear.Scale(dst, image.Rect(0, 0, w, height), src, src.Bounds(), draw.Src, nil)
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, dst); err != nil {
		return nil, false
	}
	payload := base64.StdEncoding.EncodeToString(pngData.Bytes())
	if k.cacheBytes+len(payload) > kittyMaxCacheBytes {
		return nil, false
	}
	// Random 24-bit IDs avoid the small sequential IDs commonly used by other
	// terminal applications. Zero is reserved. Never delete a global image set.
	var id uint32
	for id == 0 || k.byID[id] != nil {
		var b [3]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, false
		}
		id = uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	}
	entry := &kittyImage{id: id, payload: payload, columns: cols, rows: rows}
	marker := fmt.Sprintf("%s%s;%d\a", kittyMarkerPrefix, k.nonce, id)
	for row := range rows {
		var line strings.Builder
		line.WriteString(marker)
		fmt.Fprintf(&line, "\x1b[38;2;%d;%d;%dm\x1b[59m", id>>16, (id>>8)&255, id&255)
		for col := range cols {
			line.WriteRune(kittyPlaceholder)
			line.WriteRune(kittyDiacritics[row])
			line.WriteRune(kittyDiacritics[col])
		}
		line.WriteString("\x1b[39m")
		entry.lines = append(entry.lines, line.String())
	}
	k.cache[key], k.byID[id] = entry, entry
	k.cacheBytes += len(payload)
	return append([]string(nil), entry.lines...), true
}

func (k *kittyImages) isActive() bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.active
}

// isKittyImageLine identifies rows whose colors encode an image ID. Generic
// text styling must not strip or replace their foreground color or OSC marker.
func isKittyImageLine(line string) bool {
	return strings.ContainsRune(line, kittyPlaceholder)
}

func (k *kittyImages) wrapTerminal(term tuist.Terminal) tuist.Terminal {
	if k == nil {
		return term
	}
	return &kittyTerminal{Terminal: term, images: k}
}

// kittyTerminal expands only markers belonging to its manager. No paths, raw
// image data, or caller-supplied protocol commands are interpreted as markers.
type kittyTerminal struct {
	tuist.Terminal
	images        *kittyImages
	pending       string
	discardMarker bool
}

func (t *kittyTerminal) Start(onInput func([]byte), onResize func()) error {
	if err := t.Terminal.Start(onInput, onResize); err != nil {
		return err
	}
	t.images.mu.Lock()
	t.images.active = true
	t.images.mu.Unlock()
	return nil
}

func (t *kittyTerminal) Stop() {
	t.images.mu.Lock()
	t.images.active = false
	for _, entry := range t.images.byID {
		if entry.uploaded {
			t.Terminal.WriteString(fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", entry.id))
			entry.uploaded = false
		}
	}
	t.pending, t.discardMarker = "", false
	t.images.mu.Unlock()
	t.Terminal.Stop()
}

func (t *kittyTerminal) Write(p []byte) { t.WriteString(string(p)) }

func (t *kittyTerminal) WriteString(s string) {
	t.images.mu.Lock()
	defer t.images.mu.Unlock()
	s = t.pending + s
	t.pending = ""
	for len(s) > 0 {
		if t.discardMarker {
			end := strings.IndexByte(s, '\a')
			if end < 0 {
				return
			}
			t.discardMarker = false
			s = s[end+1:]
			continue
		}
		start := strings.Index(s, kittyMarkerPrefix)
		if start < 0 {
			// Keep a possible split marker prefix, but forward other terminal
			// escapes unchanged. Pending data is bounded by the marker length.
			keep := min(len(s), len(kittyMarkerPrefix)-1)
			for keep > 0 && !strings.HasPrefix(kittyMarkerPrefix, s[len(s)-keep:]) {
				keep--
			}
			t.Terminal.WriteString(s[:len(s)-keep])
			t.pending = s[len(s)-keep:]
			return
		}
		t.Terminal.WriteString(s[:start])
		s = s[start+len(kittyMarkerPrefix):]
		end := strings.IndexByte(s, '\a')
		if end < 0 {
			if len(s) > 128 {
				t.discardMarker = true
			} else {
				t.pending = kittyMarkerPrefix + s
			}
			return
		}
		if end <= 128 && t.images.active {
			nonce, idText, ok := strings.Cut(s[:end], ";")
			if ok && nonce == t.images.nonce {
				id, err := strconv.ParseUint(idText, 10, 24)
				if entry := t.images.byID[uint32(id)]; err == nil && entry != nil && !entry.uploaded {
					t.upload(entry)
				}
			}
		}
		s = s[end+1:]
	}
}

func (t *kittyTerminal) upload(entry *kittyImage) {
	for offset := 0; offset < len(entry.payload); offset += kittyChunkSize {
		end := min(offset+kittyChunkSize, len(entry.payload))
		more := 0
		if end < len(entry.payload) {
			more = 1
		}
		header := fmt.Sprintf("\x1b_Gm=%d", more)
		if offset == 0 {
			header = fmt.Sprintf("\x1b_Ga=t,f=100,t=d,i=%d,q=2,m=%d", entry.id, more)
		}
		t.Terminal.WriteString(header + ";" + entry.payload[offset:end] + "\x1b\\")
	}
	t.Terminal.WriteString(fmt.Sprintf("\x1b_Ga=p,U=1,i=%d,c=%d,r=%d,q=2\x1b\\", entry.id, entry.columns, entry.rows))
	entry.uploaded = true
}
