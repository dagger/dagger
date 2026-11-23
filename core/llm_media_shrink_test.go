package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql"
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
		data, mimeType := ShrinkLLMImage(testLLMPNG(t, 4000, 1000), "image/png", DefaultLLMImageEdge)
		require.Equal(t, "image/png", mimeType)
		requireLLMImageSize(t, data, "png", DefaultLLMImageEdge, DefaultLLMImageEdge/4)
	})

	t.Run("JPEG stays JPEG", func(t *testing.T) {
		data, mimeType := ShrinkLLMImage(testLLMJPEG(t, 1000, 3456), "image/jpeg", DefaultLLMImageEdge)
		require.Equal(t, "image/jpeg", mimeType)
		requireLLMImageSize(t, data, "jpeg", 745, DefaultLLMImageEdge)
	})

	t.Run("WebP becomes PNG", func(t *testing.T) {
		webp := testLLMWebP(3000, 100)
		requireLLMImageSize(t, webp, "webp", 3000, 100)
		data, mimeType := ShrinkLLMImage(webp, "image/webp", DefaultLLMImageEdge)
		require.Equal(t, "image/png", mimeType)
		requireLLMImageSize(t, data, "png", DefaultLLMImageEdge, 86)
		img, err := png.Decode(bytes.NewReader(data))
		require.NoError(t, err)
		require.Equal(t, color.RGBA{R: 64, G: 128, B: 32, A: 255}, color.RGBAModel.Convert(img.At(1000, 40)))
	})

	t.Run("JPEG keeps its Exif segment", func(t *testing.T) {
		src := testLLMJPEG(t, 3000, 100)
		exif := []byte("\xff\xe1\x00\x10Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08")
		src = append(append(append([]byte{}, src[:2]...), exif...), src[2:]...)
		data, mimeType := ShrinkLLMImage(src, "image/jpeg", DefaultLLMImageEdge)
		require.Equal(t, "image/jpeg", mimeType)
		requireLLMImageSize(t, data, "jpeg", DefaultLLMImageEdge, 86)
		require.Equal(t, exif, data[2:2+len(exif)])
	})

	t.Run("an opaque dithered image does not outgrow its source", func(t *testing.T) {
		// A dithered GIF compresses far better than its smoothed, shrunk PNG,
		// or even a JPEG of it, but its sampled palette stays as small.
		img := image.NewPaletted(image.Rect(0, 0, 3000, 2000), color.Palette{
			color.Black, color.White, color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255},
		})
		rng := rand.New(rand.NewPCG(1, 2))
		for i := range img.Pix {
			img.Pix[i] = uint8(rng.IntN(4))
		}
		var buf bytes.Buffer
		require.NoError(t, gif.Encode(&buf, img, nil))
		data, mimeType := ShrinkLLMImage(buf.Bytes(), "image/gif", DefaultLLMImageEdge)
		require.Equal(t, "image/png", mimeType)
		requireLLMImageSize(t, data, "png", DefaultLLMImageEdge, 1717)
		require.Less(t, len(data), buf.Len())
	})

	t.Run("an opaque image the PNG would outgrow becomes JPEG", func(t *testing.T) {
		// Noise as a lossy WebP would carry it: tiny at the source, huge as PNG.
		noise := image.NewRGBA(image.Rect(0, 0, 2576, 1717))
		rng := rand.New(rand.NewPCG(7, 8))
		for i := range noise.Pix {
			noise.Pix[i] = uint8(rng.IntN(256))
			if i%4 == 3 {
				noise.Pix[i] = 255
			}
		}
		data, mimeType, err := encodeShrunkLLMImage([]byte("a small lossy source"), "webp", noise, noise)
		require.NoError(t, err)
		require.Equal(t, "image/jpeg", mimeType)
		requireLLMImageSize(t, data, "jpeg", 2576, 1717)
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
		data, mimeType := ShrinkLLMImage(buf.Bytes(), "image/png", DefaultLLMImageEdge)
		require.Equal(t, "image/png", mimeType)
		requireLLMImageSize(t, data, "png", DefaultLLMImageEdge, 1717)
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
		data, mimeType := ShrinkLLMImage(buf.Bytes(), "image/png", DefaultLLMImageEdge)
		require.Equal(t, "image/png", mimeType)
		requireLLMImageSize(t, data, "png", DefaultLLMImageEdge, 1717)
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
		data, mimeType := ShrinkLLMImage(buf.Bytes(), "image/gif", DefaultLLMImageEdge)
		require.Equal(t, "image/png", mimeType)
		img, err := png.Decode(bytes.NewReader(data))
		require.NoError(t, err)
		require.Equal(t, image.Rect(0, 0, DefaultLLMImageEdge, 644), img.Bounds())
		_, _, _, a := img.At(DefaultLLMImageEdge/4, 300).RGBA()
		require.Equal(t, uint32(0xffff), a, "the frame covers the left half")
		_, _, _, a = img.At(DefaultLLMImageEdge*3/4, 300).RGBA()
		require.Zero(t, a, "the rest of the screen is transparent")
	})

	t.Run("progressive JPEGs are costed by their samples", func(t *testing.T) {
		_, progressive := jpegProgressiveSamples(testLLMJPEG(t, 10, 10))
		require.False(t, progressive)
		// SOF2 of a 4:2:0 image: Y sampled 2x2, Cb and Cr 1x1.
		sof2 := "\xff\xc2\x00\x11\x08\x0f\xa0\x17\x70\x03\x01\x22\x00\x02\x11\x01\x03\x11\x01"
		samples, progressive := jpegProgressiveSamples([]byte("\xff\xd8\xff\xe0\x00\x04ab" + sof2 + "\xff\xda\x00\x02"))
		require.True(t, progressive)
		require.InDelta(t, 1.5, samples, 1e-9)
		samples, progressive = jpegProgressiveSamples([]byte("\xff\xd8\xff\xc2\x00\x02\xff\xda\x00\x02"))
		require.True(t, progressive)
		require.InDelta(t, 3, samples, 1e-9, "an unreadable frame header assumes 4:4:4")
	})

	t.Run("images within the cap are byte-identical", func(t *testing.T) {
		for _, tc := range []struct {
			data     []byte
			mimeType string
		}{
			{testLLMPNG(t, DefaultLLMImageEdge, 300), "image/png"},
			{testLLMJPEG(t, 300, DefaultLLMImageEdge), "image/jpeg"},
			{testLLMWebP(2000, 2000), "image/webp"},
		} {
			data, mimeType := ShrinkLLMImage(tc.data, tc.mimeType, DefaultLLMImageEdge)
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
			data, mimeType := ShrinkLLMImage(tc.data, tc.mimeType, DefaultLLMImageEdge)
			require.Equal(t, tc.mimeType, mimeType)
			require.Equal(t, tc.data, data)
		}
	})
}

func TestLLMImageMaxEdge(t *testing.T) {
	for _, tc := range []struct {
		provider LLMProvider
		model    string
		edge     int
	}{
		// Claude 4.7 and later use the high-resolution tier.
		{Anthropic, "claude-opus-4-7", 2576},
		{Anthropic, "claude-opus-4-8", 2576},
		{Anthropic, "claude-opus-5", 2576},
		{Anthropic, "claude-sonnet-5", 2576},
		{Anthropic, "claude-haiku-5-1", 2576},
		{Anthropic, "claude-fable-5[1m]", 2576},
		{OpenRouter, "anthropic/claude-opus-4-7", 2576},
		{Other, "us.anthropic.claude-opus-4-7-v1:0", 2576},
		{Other, "claude-opus-4-7@20260101", 2576},
		// Earlier Claude models use the standard tier.
		{Anthropic, "claude-opus-4-6", 1568},
		{Anthropic, "claude-sonnet-4-5-20250929", 1568},
		{Anthropic, "claude-sonnet-4-20250514", 1568},
		{Anthropic, "claude-haiku-4-5", 1568},
		{Anthropic, "claude-3-7-sonnet-latest", 1568},
		{Anthropic, "claude-opus-4", 1568},
		{Anthropic, "", 1568},
		// GPT-5.5 sees the original image at detail "auto", up to 6000px.
		{OpenAI, "gpt-5.5", 6000},
		{OpenAI, "gpt-5.5-pro", 6000},
		{OpenRouter, "openai/gpt-5.5", 6000},
		// Later models keep it, but reject more than 30,000 patches.
		{OpenAICodex, "gpt-5.6-sol", 5536},
		{OpenAI, "gpt-6-astra", 5536},
		{OpenAI, "gpt-6.2-astra", 5536},
		// Earlier GPT models, and the o-series, see it at "high".
		{OpenAI, "gpt-5.4", 2048},
		{OpenAI, "gpt-5.4-mini", 2048},
		{OpenAICodex, "gpt-5.3-codex", 2048},
		{OpenAI, "gpt-5.1", 2048},
		{OpenAI, "gpt-5", 2048},
		{OpenAI, "gpt-4.1", 2048},
		{OpenAI, "gpt-4o", 2048},
		{OpenAI, "chatgpt-4o-latest", 2048},
		{OpenAI, "o3", 2048},
		{OpenAI, "o4-mini", 2048},
		// No documented limit, or an unknown model.
		{Google, "gemini-2.5-pro", DefaultLLMImageEdge},
		{Local, "llama3.2-vision", DefaultLLMImageEdge},
		{OpenAI, "my-self-hosted-model", DefaultLLMImageEdge},
		{Other, "", DefaultLLMImageEdge},
	} {
		require.Equal(t, tc.edge, LLMImageMaxEdge(tc.provider, tc.model), "%s %s", tc.provider, tc.model)
	}
}

func testLLMConversation(model string, provider LLMProvider) *LLM {
	return (&LLM{endpointMtx: &sync.Mutex{}, mcp: &MCP{}}).WithModel(model, string(provider))
}

func TestLLMFitImages(t *testing.T) {
	ctx := t.Context()
	large := testLLMPNG(t, 3200, 1600)

	t.Run("an image joining a conversation fits its model", func(t *testing.T) {
		for model, edge := range map[string]int{"claude-opus-4-6": 1568, "claude-opus-4-7": 2576} {
			blocks := []*LLMContentBlock{
				{Kind: LLMContentText, Text: "look"},
				{Kind: LLMContentImage, MIMEType: "image/png", Data: large},
			}
			fitted, err := testLLMConversation(model, Anthropic).FitImages(ctx, blocks)
			require.NoError(t, err)
			require.Equal(t, "look", fitted[0].Text)
			requireLLMImageSize(t, fitted[1].Data, "png", edge, edge/2)
			require.Equal(t, dagql.Bytes(large), blocks[1].Data, "the joining blocks are not modified")
		}
	})

	t.Run("tool result images fit too", func(t *testing.T) {
		result := &LLMContentBlock{Kind: LLMContentToolResult, CallID: "call-1", Content: []*LLMContentBlock{
			{Kind: LLMContentImage, MIMEType: "image/png", Data: large},
		}}
		fitted, err := testLLMConversation("claude-sonnet-4-5", Anthropic).FitImages(ctx, []*LLMContentBlock{result})
		require.NoError(t, err)
		requireLLMImageSize(t, fitted[0].Content[0].Data, "png", 1568, 784)
		require.Equal(t, "call-1", fitted[0].CallID)
		require.Equal(t, dagql.Bytes(large), result.Content[0].Data)
	})

	t.Run("images within the limit are kept as they are", func(t *testing.T) {
		blocks := []*LLMContentBlock{{Kind: LLMContentImage, MIMEType: "image/png", Data: large}}
		fitted, err := testLLMConversation("gpt-5.5", OpenAI).FitImages(ctx, blocks)
		require.NoError(t, err)
		require.Same(t, blocks[0], fitted[0])
	})

	t.Run("a canceled fit fails rather than storing the image unfitted", func(t *testing.T) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		llmImageShrinks <- struct{}{} // another shrink is running
		defer func() { <-llmImageShrinks }()
		_, err := testLLMConversation("claude-opus-4-6", Anthropic).FitImages(canceled, []*LLMContentBlock{{Kind: LLMContentImage, MIMEType: "image/png", Data: large}})
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("a model switch fits the history to the new model", func(t *testing.T) {
		huge := []*LLMContentBlock{{Kind: LLMContentImage, MIMEType: "image/png", Data: testLLMPNG(t, 5000, 2500)}}
		gpt := testLLMConversation("gpt-5.5", OpenAI)
		fitted, err := gpt.FitImages(ctx, huge)
		require.NoError(t, err)
		gpt = gpt.WithContent(fitted, nil).WithToolResultBlocks("call-1", "screenshot", fitted, false)
		requireLLMImageSize(t, gpt.Messages[0].Content[0].Data, "png", 5000, 2500)
		require.True(t, gpt.HasOversizedImages(2576))
		require.False(t, gpt.HasOversizedImages(6000))

		claude, err := gpt.WithModel("claude-opus-4-7", string(Anthropic)).WithImagesFitted(ctx)
		require.NoError(t, err)
		requireLLMImageSize(t, claude.Messages[0].Content[0].Data, "png", 2576, 1288)
		requireLLMImageSize(t, claude.Messages[1].Content[0].Content[0].Data, "png", 2576, 1288)
		require.Equal(t, "screenshot", claude.Messages[1].Content[0].Text)
		require.Equal(t, huge[0].Data, gpt.Messages[0].Content[0].Data, "the earlier conversation is not modified")
		require.False(t, claude.HasOversizedImages(2576))

		// Switching back never enlarges, and fitting again changes nothing.
		back, err := claude.WithModel("gpt-5.5", string(OpenAI)).WithImagesFitted(ctx)
		require.NoError(t, err)
		require.Same(t, claude.Messages[0], back.Messages[0])
		again, err := claude.WithImagesFitted(ctx)
		require.NoError(t, err)
		require.Same(t, claude, again)
	})
}
