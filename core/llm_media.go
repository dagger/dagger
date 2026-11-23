package core

import (
	"bytes"
	"image"
	_ "image/gif" // decode GIF images to shrink them
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode WebP images to shrink them
)

// MaxLLMImageEdge bounds the long edge of an image sent to a model.
//
// Providers shrink larger images to their own maximum before the model sees
// them (Anthropic: 1568px for the standard tier, 2576px for the
// high-resolution tier), so nothing the model would have seen is lost.
// Anthropic also rejects a whole request that carries more than 20 images if
// any of them has a side over 3000px, and history is resent every turn, so
// one oversized image would break every turn once a conversation grows past
// 20 images.
const MaxLLMImageEdge = 2576

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

// ShrinkLLMImage scales an image down so its long edge is at most
// MaxLLMImageEdge, preserving its aspect ratio, and returns it with its MIME
// type. JPEG stays JPEG, keeping its Exif segment. Every other format (GIF:
// its first frame) becomes PNG, unless that PNG would be large and outgrow
// its source: then an opaque image becomes JPEG, and a transparent one is
// sampled rather than blended (keeping a palette), so it adds no colors.
//
// Images within the cap, and images that cannot be decoded as the type their
// MIME type declares, are returned unchanged, byte for byte; only the header
// is read to tell.
//
// Shrink an image once, when its content block is created: the stored bytes
// are then what every later request resends, keeping prompt caches stable.
func ShrinkLLMImage(data []byte, mimeType string) ([]byte, string) {
	if len(data) == 0 || len(data) > MaxLLMMediaBytes {
		return data, mimeType
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || "image/"+format != mimeType {
		return data, mimeType
	}
	if cfg.Width <= MaxLLMImageEdge && cfg.Height <= MaxLLMImageEdge {
		return data, mimeType
	}
	cost := int64(cfg.Width) * int64(cfg.Height)
	if format == "jpeg" && jpegProgressive(data) {
		// Progressive decoding also keeps every component's coefficients.
		cost *= 4
	}
	if cost > maxLLMImageDecodePixels {
		return data, mimeType
	}

	llmImageShrinks <- struct{}{}
	defer func() { <-llmImageShrinks }()

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mimeType
	}
	if b := src.Bounds(); b != image.Rect(0, 0, cfg.Width, cfg.Height) {
		// A GIF's first frame can cover only part of its logical screen.
		canvas := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
		draw.Draw(canvas, b, src, b.Min, draw.Src)
		src = canvas
	}
	width, height := shrunkLLMImageSize(cfg.Width, cfg.Height)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	scaler := draw.Interpolator(draw.CatmullRom) // keeps text in screenshots legible
	if 32*int64(width)*int64(cfg.Height) > maxLLMImageKernelBytes {
		scaler = draw.ApproxBiLinear
	}
	scaler.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)

	shrunk, shrunkMIMEType, err := encodeShrunkLLMImage(data, format, src, dst)
	if err != nil || len(shrunk) > MaxLLMMediaBytes {
		return data, mimeType
	}
	return shrunk, shrunkMIMEType
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

// shrunkLLMImageSize scales width and height so the long edge is
// MaxLLMImageEdge.
func shrunkLLMImageSize(width, height int) (int, int) {
	scale := func(short, long int) int {
		return max(1, int((int64(short)*MaxLLMImageEdge+int64(long)/2)/int64(long)))
	}
	if width >= height {
		return MaxLLMImageEdge, scale(height, width)
	}
	return scale(width, height), MaxLLMImageEdge
}

// shrinkImage applies ShrinkLLMImage to an IMAGE block in place.
func (b *LLMContentBlock) shrinkImage() {
	if b == nil || b.Kind != LLMContentImage {
		return
	}
	b.Data, b.MIMEType = ShrinkLLMImage(b.Data, b.MIMEType)
}
