package core

import (
	"bytes"
	"context"
	"image"
	_ "image/gif" // decode GIF images to shrink them
	"image/jpeg"
	"image/png"
	"slices"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core/modelcatalog"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode WebP images to shrink them
)

// DefaultLLMImageEdge bounds the long edge of an image sent to a model whose
// limit LLMImageMaxEdge does not know: the largest any Claude model uses, and
// under Anthropic's 3000px limit for requests with more than 20 images.
const DefaultLLMImageEdge = 2576

const (
	// maxLLMImageDecodePixels bounds the images we are willing to decode to
	// shrink (a decoded 64MP image takes 256MiB, twice that at 16 bits per
	// channel; a progressive JPEG counts four times its pixels), so a hostile
	// header cannot make the engine allocate gigabytes. Larger images are
	// passed through unchanged.
	maxLLMImageDecodePixels = 64 << 20

	// maxLLMImageKernelBytes bounds the intermediate buffer of Catmull-Rom
	// scaling (32 bytes per destination column per source row). Beyond it,
	// bilinear scaling, which needs no buffer, is used instead.
	maxLLMImageKernelBytes = 256 << 20

	// llmImageJPEGQuality is the quality of a shrunk JPEG, and
	// llmImageJPEGFallbackQuality that of one whose source was compressed
	// harder, so that shrinking does not grow it.
	llmImageJPEGQuality         = 90
	llmImageJPEGFallbackQuality = 75

	// llmImagePNGGrowthFloor is the size below which a shrunk PNG is kept
	// even if it outgrew its source.
	llmImagePNGGrowthFloor = 1 << 20
)

// llmImageShrinks serializes shrinking, which takes hundreds of MiB for the
// largest images, so concurrent requests cannot multiply that.
var llmImageShrinks = make(chan struct{}, 1)

// ShrinkLLMImage scales an image down so its long edge is at most maxEdge,
// preserving its aspect ratio, and returns it with its MIME type. JPEG stays
// JPEG, keeping its Exif segment. Every other format (GIF: its first frame)
// becomes PNG, unless that PNG would be large and outgrow its source: then an
// opaque image becomes JPEG, and a transparent one is sampled rather than
// blended (keeping a palette), so it adds no colors.
//
// Images within the cap, and images that cannot be decoded as the type their
// MIME type declares, are returned unchanged, byte for byte; only the header
// is read to tell.
//
// Shrink an image once, when it joins a conversation (see LLM.FitImages):
// the stored bytes are then what every later request resends, keeping prompt
// caches stable.
func ShrinkLLMImage(data []byte, mimeType string, maxEdge int) ([]byte, string) {
	data, mimeType, _ = shrinkLLMImage(data, mimeType, maxEdge)
	return data, mimeType
}

// shrinkLLMImage is ShrinkLLMImage, also reporting whether it changed the
// image.
func shrinkLLMImage(data []byte, mimeType string, maxEdge int) ([]byte, string, bool) {
	if len(data) == 0 || len(data) > MaxLLMMediaBytes || maxEdge <= 0 {
		return data, mimeType, false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || "image/"+format != mimeType {
		return data, mimeType, false
	}
	if cfg.Width <= maxEdge && cfg.Height <= maxEdge {
		return data, mimeType, false
	}
	cost := int64(cfg.Width) * int64(cfg.Height)
	if format == "jpeg" && jpegProgressive(data) {
		// Progressive decoding also keeps every component's coefficients.
		cost *= 4
	}
	if cost > maxLLMImageDecodePixels {
		return data, mimeType, false
	}

	llmImageShrinks <- struct{}{}
	defer func() { <-llmImageShrinks }()

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mimeType, false
	}
	if b := src.Bounds(); b != image.Rect(0, 0, cfg.Width, cfg.Height) {
		// A GIF's first frame can cover only part of its logical screen.
		canvas := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
		draw.Draw(canvas, b, src, b.Min, draw.Src)
		src = canvas
	}
	width, height := shrunkLLMImageSize(cfg.Width, cfg.Height, maxEdge)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	scaler := draw.Interpolator(draw.CatmullRom) // keeps text in screenshots legible
	if 32*int64(width)*int64(cfg.Height) > maxLLMImageKernelBytes {
		scaler = draw.ApproxBiLinear
	}
	scaler.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)

	shrunk, shrunkMIMEType, err := encodeShrunkLLMImage(data, format, src, dst)
	if err != nil || len(shrunk) > MaxLLMMediaBytes {
		return data, mimeType, false
	}
	return shrunk, shrunkMIMEType, true
}

// encodeShrunkLLMImage encodes dst, src scaled down, avoiding an encoding
// that grows much bigger than the source's.
func encodeShrunkLLMImage(data []byte, format string, src image.Image, dst *image.RGBA) ([]byte, string, error) {
	if format == "jpeg" {
		exif := jpegExifSegment(data)
		shrunk, err := encodeLLMJPEG(dst, llmImageJPEGQuality, exif)
		if err != nil {
			return nil, "", err
		}
		if len(shrunk) > len(data) {
			smaller, smallerErr := encodeLLMJPEG(dst, llmImageJPEGFallbackQuality, exif)
			if smallerErr == nil && len(smaller) < len(shrunk) {
				shrunk = smaller
			}
		}
		return shrunk, "image/jpeg", nil
	}

	shrunk, err := encodeLLMPNG(dst)
	if err != nil || len(shrunk) <= max(len(data), llmImagePNGGrowthFloor) {
		return shrunk, "image/png", err
	}
	// Lossless PNG can dwarf a lossy or dithered source (WebP, GIF). An
	// opaque image loses nothing the model would see as JPEG instead; a
	// transparent one is sampled rather than blended, adding no colors, and
	// a paletted one keeps its palette.
	var alt []byte
	altMIMEType := "image/png"
	bounds := dst.Bounds()
	if dst.Opaque() {
		alt, err = encodeLLMJPEG(dst, llmImageJPEGQuality, nil)
		altMIMEType = "image/jpeg"
	} else if paletted, ok := src.(*image.Paletted); ok {
		alt, err = encodeLLMPNG(shrinkPaletted(paletted, bounds.Dx(), bounds.Dy()))
	} else {
		sampled := image.NewRGBA(bounds)
		draw.NearestNeighbor.Scale(sampled, bounds, src, src.Bounds(), draw.Src, nil)
		alt, err = encodeLLMPNG(sampled)
	}
	if err == nil && len(alt) < len(shrunk) {
		return alt, altMIMEType, nil
	}
	return shrunk, "image/png", nil
}

// shrinkPaletted scales a paletted image down by nearest-neighbor sampling,
// keeping its palette.
func shrinkPaletted(src *image.Paletted, width, height int) *image.Paletted {
	dst := image.NewPaletted(image.Rect(0, 0, width, height), src.Palette)
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	for y := range height {
		srow := src.Pix[(2*y+1)*sh/(2*height)*src.Stride:]
		row := dst.Pix[y*dst.Stride:]
		for x := range width {
			row[x] = srow[(2*x+1)*sw/(2*width)]
		}
	}
	return dst
}

func encodeLLMPNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeLLMJPEG encodes a shrunk image as JPEG, carrying over the source's
// Exif segment (if any) so its orientation still applies.
func encodeLLMJPEG(img image.Image, quality int, exif []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(exif) == 0 {
		return out, nil
	}
	// The Exif APP1 segment belongs right after the start-of-image marker.
	withExif := make([]byte, 0, len(out)+len(exif))
	withExif = append(withExif, out[:2]...)
	withExif = append(withExif, exif...)
	return append(withExif, out[2:]...), nil
}

// jpegExifSegment returns a JPEG's Exif APP1 segment, marker included, or nil.
func jpegExifSegment(data []byte) []byte {
	return jpegSegment(data, func(marker byte, payload []byte) bool {
		return marker == 0xe1 && bytes.HasPrefix(payload, []byte("Exif\x00\x00"))
	})
}

// jpegProgressive reports whether a JPEG is progressive (SOF2).
func jpegProgressive(data []byte) bool {
	return jpegSegment(data, func(marker byte, _ []byte) bool {
		return marker == 0xc2
	}) != nil
}

// jpegSegment returns the first segment before a JPEG's image data that
// matches, marker included, or nil.
func jpegSegment(data []byte, match func(marker byte, payload []byte) bool) []byte {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return nil
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return nil
		}
		marker := data[i+1]
		if marker == 0xda || marker == 0xd9 { // start of scan, end of image
			return nil
		}
		length := int(data[i+2])<<8 | int(data[i+3])
		end := i + 2 + length
		if length < 2 || end > len(data) {
			return nil
		}
		if match(marker, data[i+4:end]) {
			return data[i:end]
		}
		i = end
	}
	return nil
}

// shrunkLLMImageSize scales width and height so the long edge is maxEdge.
func shrunkLLMImageSize(width, height, maxEdge int) (int, int) {
	scale := func(short, long int) int {
		return max(1, int((int64(short)*int64(maxEdge)+int64(long)/2)/int64(long)))
	}
	if width >= height {
		return maxEdge, scale(height, width)
	}
	return scale(width, height), maxEdge
}

// LLMImageMaxEdge returns the longest image edge a model makes use of, as its
// provider documents it; a larger image is downscaled server-side anyway, so
// shrinking it to this loses nothing the model would see. The model is
// matched by family, so a Claude or GPT model served through OpenRouter or a
// proxy gets its family's limit.
//
//   - Claude: 2576px for Claude 4.7 and later, 1568px before. Both are under
//     Anthropic's 3000px limit for requests with more than 20 images.
//   - GPT: Dagger sends images with detail "auto" (or none, which defaults
//     to it). That means "original" on GPT-5.5 and later, up to 6000px, and
//     "high" before, up to 2048px, as for tile-based models (GPT-4o, GPT-4.1,
//     GPT-5.1) and the o-series.
//   - Gemini, and models we know nothing of: DefaultLLMImageEdge. Gemini
//     documents no pixel limit (it tiles at 768px).
func LLMImageMaxEdge(provider LLMProvider, model string) int {
	id := strings.ToLower(modelcatalog.NormalizeModelID(model))
	if i := strings.Index(id, "claude"); i >= 0 || provider == Anthropic {
		if i >= 0 {
			major, minor, ok := llmModelVersion(id[i+len("claude"):])
			if ok && (major > 4 || major == 4 && minor >= 7) {
				return 2576
			}
		}
		return 1568
	}
	switch {
	case strings.HasPrefix(id, "gpt-"):
		major, minor, ok := llmModelVersion(id[len("gpt-"):])
		if ok && (major > 5 || major == 5 && minor >= 5) {
			return 6000
		}
		return 2048
	case strings.HasPrefix(id, "chatgpt-"), strings.HasPrefix(id, "codex-"),
		len(id) > 1 && id[0] == 'o' && id[1] >= '1' && id[1] <= '9':
		return 2048
	}
	return DefaultLLMImageEdge
}

// llmModelVersion reads the first version in a model ID's remainder, such as
// "-opus-4-7" (4.7), "-3-5-sonnet" (3.5), "-sonnet-4" (4.0) or "5.5-pro"
// (5.5). A minor version must directly follow the major one; date stamps
// and other long numbers are not versions.
func llmModelVersion(rest string) (major, minor int, ok bool) {
	fields := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '-' || r == '.' || r == '_' || r == ':'
	})
	for i, field := range fields {
		n, isVersion := llmModelVersionNumber(field)
		if !isVersion {
			// "4o" is GPT-4o: a major version with a suffix.
			if len(field) > 1 && field[0] >= '0' && field[0] <= '9' {
				if n, isVersion = llmModelVersionNumber(strings.TrimRight(field, "abcdefghijklmnopqrstuvwxyz")); isVersion {
					return n, 0, true
				}
			}
			continue
		}
		if i+1 < len(fields) {
			if m, isMinor := llmModelVersionNumber(fields[i+1]); isMinor {
				return n, m, true
			}
		}
		return n, 0, true
	}
	return 0, 0, false
}

// llmModelVersionNumber parses a one- or two-digit version number.
func llmModelVersionNumber(field string) (int, bool) {
	if len(field) == 0 || len(field) > 2 {
		return 0, false
	}
	n, err := strconv.Atoi(field)
	return n, err == nil && n >= 0
}

// FitLLMImages returns blocks with every image, including those in tool
// results, shrunk to maxEdge (see ShrinkLLMImage). Blocks are never
// modified: a block that changes is copied, and if none does, blocks itself
// is returned.
func FitLLMImages(blocks []*LLMContentBlock, maxEdge int) []*LLMContentBlock {
	fitted, _ := fitLLMImages(blocks, maxEdge)
	return fitted
}

func fitLLMImages(blocks []*LLMContentBlock, maxEdge int) ([]*LLMContentBlock, bool) {
	var fitted []*LLMContentBlock
	for i, block := range blocks {
		fit, changed := fitLLMImageBlock(block, maxEdge)
		if changed && fitted == nil {
			fitted = slices.Clone(blocks)
		}
		if fitted != nil {
			fitted[i] = fit
		}
	}
	if fitted == nil {
		return blocks, false
	}
	return fitted, true
}

func fitLLMImageBlock(block *LLMContentBlock, maxEdge int) (*LLMContentBlock, bool) {
	if block == nil {
		return nil, false
	}
	switch block.Kind {
	case LLMContentImage:
		data, mimeType, changed := shrinkLLMImage(block.Data, block.MIMEType, maxEdge)
		if !changed {
			return block, false
		}
		cp := *block
		cp.Data, cp.MIMEType = data, mimeType
		return &cp, true
	case LLMContentToolResult:
		content, changed := fitLLMImages(block.Content, maxEdge)
		if !changed {
			return block, false
		}
		cp := *block
		cp.Content = content
		return &cp, true
	default:
		return block, false
	}
}

func hasLLMImage(blocks []*LLMContentBlock) bool {
	return slices.ContainsFunc(blocks, func(block *LLMContentBlock) bool {
		return block != nil && (block.Kind == LLMContentImage || hasLLMImage(block.Content))
	})
}

// ImageMaxEdge returns LLMImageMaxEdge for the conversation's current model,
// routed as a request would be. If the route cannot be resolved, the model
// and provider the conversation names are used as they are.
func (llm *LLM) ImageMaxEdge(ctx context.Context) int {
	if endpoint, err := llm.Route(ctx); err == nil {
		return LLMImageMaxEdge(endpoint.Provider, endpoint.Model)
	}
	return LLMImageMaxEdge(LLMProvider(llm.provider), llm.model)
}

// FitImages fits the images in blocks joining this conversation to its
// current model (see FitLLMImages). The model is only routed when there is
// an image.
func (llm *LLM) FitImages(ctx context.Context, blocks []*LLMContentBlock) []*LLMContentBlock {
	if !hasLLMImage(blocks) {
		return blocks
	}
	return FitLLMImages(blocks, llm.ImageMaxEdge(ctx))
}

// WithImagesFitted fits the images already in the history to the current
// model, after it changed: only shrinking, as an image smaller than the new
// limit was already all the earlier model used. A model switch already
// invalidates the provider's prompt cache, so changing the bytes now costs
// nothing more.
func (llm *LLM) WithImagesFitted(ctx context.Context) *LLM {
	var fitted *LLM
	var maxEdge int
	for i, msg := range llm.Messages {
		if !hasLLMImage(msg.Content) {
			continue
		}
		if maxEdge == 0 {
			maxEdge = llm.ImageMaxEdge(ctx)
		}
		content, changed := fitLLMImages(msg.Content, maxEdge)
		if !changed {
			continue
		}
		if fitted == nil {
			fitted = llm.Clone()
		}
		// Messages are shared with other clones: replace, never modify.
		cp := *msg
		cp.Content = content
		fitted.Messages[i] = &cp
	}
	if fitted == nil {
		return llm
	}
	return fitted
}
