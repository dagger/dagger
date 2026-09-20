package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestLLMContentValidation(t *testing.T) {
	image := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: "aGVsbG8="}
	for _, block := range []*LLMContentBlock{
		image,
		{Kind: LLMContentAudio, MIMEType: "audio/wav", Data: image.Data},
		{Kind: LLMContentDocument, MIMEType: "application/pdf", Data: image.Data},
		{Kind: LLMContentToolResult, Text: "before", Content: []*LLMContentBlock{image}},
		{Kind: LLMContentText, Text: "legacy"},
	} {
		require.NoError(t, block.Validate())
	}
	for name, block := range map[string]*LLMContentBlock{
		"nil":           nil,
		"unknown":       {Kind: "UNKNOWN"},
		"no mime":       {Kind: LLMContentImage, Data: image.Data},
		"no data":       {Kind: LLMContentImage, MIMEType: "image/png"},
		"bad base64":    {Kind: LLMContentImage, MIMEType: "image/png", Data: "not base64"},
		"wrong mime":    {Kind: LLMContentImage, MIMEType: "audio/wav", Data: image.Data},
		"non PDF":       {Kind: LLMContentDocument, MIMEType: "text/plain", Data: image.Data},
		"media text":    {Kind: LLMContentImage, MIMEType: "image/png", Data: image.Data, Text: "bad"},
		"text media":    {Kind: LLMContentText, Data: image.Data},
		"wrong nesting": {Kind: LLMContentText, Content: []*LLMContentBlock{image}},
		"nil child":     {Kind: LLMContentToolResult, Content: []*LLMContentBlock{nil}},
		"nested call":   {Kind: LLMContentToolResult, Content: []*LLMContentBlock{{Kind: LLMContentToolCall}}},
	} {
		t.Run(name, func(t *testing.T) { require.Error(t, block.Validate()) })
	}
	large := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: base64.StdEncoding.EncodeToString(make([]byte, MaxLLMMediaBytes/2+1))}
	require.NoError(t, large.Validate())
	require.ErrorContains(t, ValidateLLMContent([]*LLMContentBlock{large, large}), "exceeds")
	require.ErrorContains(t, (&LLMContentBlock{Kind: LLMContentToolResult, Content: []*LLMContentBlock{large, large}}).Validate(), "exceeds")
}

func TestLLMContentFromBytes(t *testing.T) {
	for _, tc := range []struct {
		data, mime string
		kind       LLMContentBlockKind
	}{
		{"\x89PNG\r\n\x1a\n", "image/png", LLMContentImage},
		{"RIFF\x00\x00\x00\x00WAVE", "audio/wave", LLMContentAudio},
		{"%PDF-1.7\n", "application/pdf", LLMContentDocument},
	} {
		block, err := llmContentFromBytes([]byte(tc.data), "")
		require.NoError(t, err)
		require.Equal(t, tc.mime, block.MIMEType)
		require.Equal(t, tc.kind, block.Kind)
		require.Equal(t, base64.StdEncoding.EncodeToString([]byte(tc.data)), block.Data)
	}
	_, err := llmContentFromBytes([]byte("plain text"), "")
	require.ErrorContains(t, err, "unsupported")
	_, err = llmContentFromBytes(nil, "image/png")
	require.Error(t, err)
}

func TestLLMContentRoundTrip(t *testing.T) {
	image := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: "aGVsbG8="}
	blocks := []*LLMContentBlock{{Kind: LLMContentText, Text: "describe"}, image}
	llm := &LLM{endpointMtx: &sync.Mutex{}, mcp: &MCP{}}
	llm = llm.WithContent(blocks, &LLMMessageOrigin{Kind: LLMMessageOriginUser})
	llm = llm.WithToolResultBlocks("call1", "first", blocks, true)
	blocks[0].Text = "mutated"
	require.Equal(t, "describe", llm.Messages[0].Content[0].Text)
	require.Equal(t, "first\ndescribe\n[image: image/png]", llm.Messages[1].ToolResultContent())
	require.Contains(t, llm.Transcript(), "[image: image/png]")
	require.NotContains(t, llm.Transcript(), image.Data)

	clone := llm.Messages[1].Clone()
	clone.Content[0].Content[0].Text = "clone mutation"
	require.Equal(t, "describe", llm.Messages[1].Content[0].Content[0].Text)
	call := &LLMContentBlock{Kind: LLMContentToolCall, Arguments: JSON(`{"x":1}`)}
	call.Clone().Arguments[0] = '['
	require.Equal(t, byte('{'), call.Arguments[0])

	sels, err := llm.recipeSelectors(context.Background())
	require.NoError(t, err)
	require.Len(t, sels, 3)
	require.Equal(t, "withContent", sels[1].Field)
	require.Equal(t, "withToolResult", sels[2].Field)
	for _, sel := range sels[1:] {
		for _, arg := range sel.Args {
			if arg.Name != "content" && arg.Name != "blocks" {
				continue
			}
			inputs, ok := arg.Value.(dagql.ArrayInput[dagql.InputObject[LLMContentBlockInput]])
			if !ok {
				continue
			} // legacy tool-result text
			require.Len(t, inputs, 2)
			require.Equal(t, image.Data, inputs[1].Value.Data)
			require.Equal(t, image.MIMEType, inputs[1].Value.MIMEType)
			require.NotPanics(t, func() { inputs.ToLiteral() })
		}
	}
	inputs, err := contentBlockInputs(llm.Messages[1].Content)
	require.NoError(t, err)
	resolved, err := inputs[0].Value.Resolve(context.Background())
	require.NoError(t, err)
	require.Equal(t, image.Data, resolved.Content[1].Data)
	encoded, err := json.Marshal(resolved)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"mime_type":"image/png"`)
	var decoded LLMContentBlock
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, resolved, &decoded)
}

func TestLLMContentRecordedMedia(t *testing.T) {
	msgs, err := decodeRecordedMessages([]byte(`[{"role":"USER","content":[{"kind":"TOOL_RESULT","callId":"c1","text":"before","content":[{"kind":"IMAGE","mimeType":"image/png","data":"aGVsbG8="}]}]}]`))
	require.NoError(t, err)
	require.Equal(t, "image/png", msgs[0].Content[0].Content[0].MIMEType)
	require.Equal(t, "before\n[image: image/png]", msgs[0].ToolResultContent())
	_, err = decodeRecordedMessages([]byte(`[{"role":"USER","content":[{"kind":"IMAGE","mimeType":"image/png","data":"!"}]}]`))
	require.Error(t, err)
}

func TestLLMContentInputValidation(t *testing.T) {
	_, err := (LLMContentBlockInput{Kind: LLMContentImage, Data: "aGVsbG8="}).Resolve(context.Background())
	require.ErrorContains(t, err, "MIME")
	_, err = (LLMContentBlockInput{Kind: LLMContentImage, Data: "aGVsbG8=", File: dagql.Opt(FileID{})}).Resolve(context.Background())
	require.ErrorContains(t, err, "exactly one")
	_, err = (LLMContentBlockInput{Kind: LLMContentText, File: dagql.Opt(FileID{})}).Resolve(context.Background())
	require.ErrorContains(t, err, "cannot contain")
	// The recursive input schema must remain finite and expose lowerCamel mimeType.
	spec := dagql.MustInputSpec(LLMContentBlockInput{})
	def := spec.TypeDefinition("")
	require.NotNil(t, def.Fields.ForName("mimeType"))
	require.Equal(t, "[LLMContentBlockInput!]", def.Fields.ForName("content").Type.String())
	require.False(t, strings.Contains(def.Fields.ForName("file").Type.String(), "!"))
}
