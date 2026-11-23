package core

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func testLLMImage(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	return img
}

func testLLMPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, testLLMImage(width, height)))
	return buf.Bytes()
}

func testLLMJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, testLLMImage(width, height), nil))
	return buf.Bytes()
}

// testLLMWebP builds a solid-color lossless WebP. Go has no WebP encoder, but
// a VP8L stream whose prefix codes each hold a single symbol encodes every
// pixel in zero bits, so the header alone describes the whole image.
func testLLMWebP(width, height int) []byte {
	var bits []byte
	var acc uint64
	var n uint
	write := func(v uint64, size uint) {
		acc |= v << n
		n += size
		for n >= 8 {
			bits = append(bits, byte(acc))
			acc >>= 8
			n -= 8
		}
	}
	// Signature, width - 1, height - 1, no alpha, version 0.
	write(0x2f, 8)
	write(uint64(width-1), 14)
	write(uint64(height-1), 14)
	write(0, 1)
	write(0, 3)
	// No transforms, no color cache, no meta prefix codes.
	write(0, 1)
	write(0, 1)
	write(0, 1)
	// Green, red, blue and alpha: simple codes of one 8-bit symbol each.
	for _, symbol := range []uint64{128, 64, 32, 255} {
		write(1, 1)
		write(0, 1)
		write(1, 1)
		write(symbol, 8)
	}
	// Distance: a simple code of one 1-bit symbol.
	write(1, 1)
	write(0, 1)
	write(0, 1)
	write(0, 1)
	if n > 0 {
		bits = append(bits, byte(acc))
	}
	if len(bits)%2 == 1 {
		bits = append(bits, 0)
	}

	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(4+8+len(bits)))
	out.WriteString("WEBPVP8L")
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(bits)))
	out.Write(bits)
	return out.Bytes()
}

func requireLLMImageSize(t *testing.T, data []byte, format string, width, height int) {
	t.Helper()
	cfg, actual, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, format, actual)
	require.Equal(t, width, cfg.Width)
	require.Equal(t, height, cfg.Height)
}

func TestShrinkLLMImage(t *testing.T) {
	t.Run("PNG long edge is capped with aspect ratio kept", func(t *testing.T) {
		data, mimeType := ShrinkLLMImage(testLLMPNG(t, 4000, 1000), "image/png")
		require.Equal(t, "image/png", mimeType)
		requireLLMImageSize(t, data, "png", MaxLLMImageEdge, MaxLLMImageEdge/4)
	})

	t.Run("JPEG stays JPEG", func(t *testing.T) {
		data, mimeType := ShrinkLLMImage(testLLMJPEG(t, 1000, 3456), "image/jpeg")
		require.Equal(t, "image/jpeg", mimeType)
		requireLLMImageSize(t, data, "jpeg", 745, MaxLLMImageEdge)
	})

	t.Run("WebP becomes PNG", func(t *testing.T) {
		webp := testLLMWebP(3000, 100)
		requireLLMImageSize(t, webp, "webp", 3000, 100)
		data, mimeType := ShrinkLLMImage(webp, "image/webp")
		require.Equal(t, "image/png", mimeType)
		requireLLMImageSize(t, data, "png", MaxLLMImageEdge, 86)
		img, err := png.Decode(bytes.NewReader(data))
		require.NoError(t, err)
		require.Equal(t, color.RGBA{R: 64, G: 128, B: 32, A: 255}, color.RGBAModel.Convert(img.At(1000, 40)))
	})

	t.Run("JPEG keeps its Exif segment", func(t *testing.T) {
		src := testLLMJPEG(t, 3000, 100)
		exif := []byte("\xff\xe1\x00\x10Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08")
		src = append(append(append([]byte{}, src[:2]...), exif...), src[2:]...)
		data, mimeType := ShrinkLLMImage(src, "image/jpeg")
		require.Equal(t, "image/jpeg", mimeType)
		requireLLMImageSize(t, data, "jpeg", MaxLLMImageEdge, 86)
		require.Equal(t, exif, data[2:2+len(exif)])
	})

	t.Run("an opaque image the PNG would outgrow becomes JPEG", func(t *testing.T) {
		// A dithered GIF compresses far better than its smoothed, shrunk PNG.
		img := image.NewPaletted(image.Rect(0, 0, 3000, 2000), color.Palette{
			color.Black, color.White, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255},
		})
		rng := rand.New(rand.NewPCG(1, 2))
		for i := range img.Pix {
			img.Pix[i] = uint8(rng.IntN(4))
		}
		var buf bytes.Buffer
		require.NoError(t, gif.Encode(&buf, img, nil))
		data, mimeType := ShrinkLLMImage(buf.Bytes(), "image/gif")
		require.Equal(t, "image/jpeg", mimeType)
		requireLLMImageSize(t, data, "jpeg", MaxLLMImageEdge, 1717)
		require.LessOrEqual(t, len(data), MaxLLMMediaBytes)
	})

	t.Run("a transparent paletted image the PNG would outgrow stays paletted", func(t *testing.T) {
		img := image.NewPaletted(image.Rect(0, 0, 3000, 2000), color.Palette{
			color.Transparent, color.Black, color.White, color.RGBA{R: 255, A: 255},
		})
		rng := rand.New(rand.NewPCG(3, 4))
		for i := range img.Pix {
			img.Pix[i] = uint8(rng.IntN(4))
		}
		var buf bytes.Buffer
		require.NoError(t, png.Encode(&buf, img))
		data, mimeType := ShrinkLLMImage(buf.Bytes(), "image/png")
		require.Equal(t, "image/png", mimeType)
		requireLLMImageSize(t, data, "png", MaxLLMImageEdge, 1717)
		require.Less(t, len(data), buf.Len())
		decoded, err := png.Decode(bytes.NewReader(data))
		require.NoError(t, err)
		require.IsType(t, &image.Paletted{}, decoded)
	})

	t.Run("a transparent image the PNG would outgrow is sampled", func(t *testing.T) {
		img := image.NewNRGBA(image.Rect(0, 0, 3000, 2000))
		colors := []color.NRGBA{{}, {A: 255}, {R: 255, G: 255, B: 255, A: 255}, {R: 255, A: 255}}
		rng := rand.New(rand.NewPCG(5, 6))
		for y := range 2000 {
			for x := range 3000 {
				img.SetNRGBA(x, y, colors[rng.IntN(len(colors))])
			}
		}
		var buf bytes.Buffer
		require.NoError(t, png.Encode(&buf, img))
		data, mimeType := ShrinkLLMImage(buf.Bytes(), "image/png")
		require.Equal(t, "image/png", mimeType)
		requireLLMImageSize(t, data, "png", MaxLLMImageEdge, 1717)
		require.Less(t, len(data), buf.Len())
	})

	t.Run("a GIF frame smaller than its screen keeps its place", func(t *testing.T) {
		frame := image.NewPaletted(image.Rect(0, 0, 2000, 1000), color.Palette{color.Transparent, color.Black})
		for i := range frame.Pix {
			frame.Pix[i] = 1
		}
		var buf bytes.Buffer
		require.NoError(t, gif.EncodeAll(&buf, &gif.GIF{
			Image:  []*image.Paletted{frame},
			Delay:  []int{0},
			Config: image.Config{ColorModel: frame.Palette, Width: 4000, Height: 1000},
		}))
		data, mimeType := ShrinkLLMImage(buf.Bytes(), "image/gif")
		require.Equal(t, "image/png", mimeType)
		img, err := png.Decode(bytes.NewReader(data))
		require.NoError(t, err)
		require.Equal(t, image.Rect(0, 0, MaxLLMImageEdge, 644), img.Bounds())
		_, _, _, a := img.At(MaxLLMImageEdge/4, 300).RGBA()
		require.Equal(t, uint32(0xffff), a, "the frame covers the left half")
		_, _, _, a = img.At(MaxLLMImageEdge*3/4, 300).RGBA()
		require.Zero(t, a, "the rest of the screen is transparent")
	})

	t.Run("progressive JPEGs are detected", func(t *testing.T) {
		require.False(t, jpegProgressive(testLLMJPEG(t, 10, 10)))
		require.True(t, jpegProgressive([]byte("\xff\xd8\xff\xe0\x00\x04ab\xff\xc2\x00\x02\xff\xda\x00\x02")))
	})

	t.Run("images within the cap are byte-identical", func(t *testing.T) {
		for _, tc := range []struct {
			data     []byte
			mimeType string
		}{
			{testLLMPNG(t, MaxLLMImageEdge, 300), "image/png"},
			{testLLMJPEG(t, 300, MaxLLMImageEdge), "image/jpeg"},
			{testLLMWebP(2000, 2000), "image/webp"},
		} {
			data, mimeType := ShrinkLLMImage(tc.data, tc.mimeType)
			require.Equal(t, tc.mimeType, mimeType)
			require.Equal(t, tc.data, data)
		}
	})

	t.Run("undecodable or mislabeled images pass through", func(t *testing.T) {
		for _, tc := range []struct {
			data     []byte
			mimeType string
		}{
			{[]byte("not an image"), "image/png"},
			{testLLMPNG(t, 4000, 10), "image/jpeg"},
			{testLLMPNG(t, 4000, 10), "image/svg+xml"},
		} {
			data, mimeType := ShrinkLLMImage(tc.data, tc.mimeType)
			require.Equal(t, tc.mimeType, mimeType)
			require.Equal(t, tc.data, data)
		}
	})
}

func TestLLMContentShrinksImages(t *testing.T) {
	large := testLLMPNG(t, 3456, 2234)
	const shrunkHeight = 1665

	t.Run("LLMContentFromBytes", func(t *testing.T) {
		block, err := LLMContentFromBytes(large, "")
		require.NoError(t, err)
		require.Equal(t, LLMContentImage, block.Kind)
		require.Equal(t, "image/png", block.MIMEType)
		requireLLMImageSize(t, block.Data, "png", MaxLLMImageEdge, shrunkHeight)
	})

	t.Run("inline data, nested in a tool result", func(t *testing.T) {
		input := LLMContentBlockInput{
			Kind:   LLMContentToolResult,
			CallID: "call-1",
			Content: []dagql.InputObject[LLMContentBlockInput]{{Value: LLMContentBlockInput{
				Kind:     LLMContentImage,
				MIMEType: "image/png",
				Data:     dagql.Opt(dagql.Bytes(large)),
			}}},
		}
		block, err := input.Resolve(t.Context())
		require.NoError(t, err)
		requireLLMImageSize(t, block.Content[0].Data, "png", MaxLLMImageEdge, shrunkHeight)
	})

	t.Run("MCP tool result", func(t *testing.T) {
		blocks, err := mcpContentBlocks(&mcp.CallToolResult{Content: []mcp.Content{
			&mcp.ImageContent{Data: testLLMWebP(3000, 3000), MIMEType: "image/webp"},
		}})
		require.NoError(t, err)
		require.Equal(t, "image/png", blocks[0].MIMEType)
		requireLLMImageSize(t, blocks[0].Data, "png", MaxLLMImageEdge, MaxLLMImageEdge)
	})

	t.Run("content persisted before shrinking", func(t *testing.T) {
		payload, err := json.Marshal(&LLMContent{Blocks: []*LLMContentBlock{
			{Kind: LLMContentText, Text: "caption"},
			{Kind: LLMContentImage, MIMEType: "image/png", Data: large},
		}})
		require.NoError(t, err)
		decoded, err := (&LLMContent{}).DecodePersistedObject(t.Context(), dagql.NewPersistDecodeContext(nil, 0, nil), payload)
		require.NoError(t, err)
		blocks := decoded.(*LLMContent).Blocks
		require.Equal(t, "caption", blocks[0].Text)
		requireLLMImageSize(t, blocks[1].Data, "png", MaxLLMImageEdge, shrunkHeight)
	})
}
