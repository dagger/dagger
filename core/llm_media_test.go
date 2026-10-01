package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestLLMContentValidation(t *testing.T) {
	image := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: []byte("hello")}
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
	large := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: make([]byte, MaxLLMMediaBytes/2+1)}
	require.NoError(t, large.Validate())
	require.ErrorContains(t, ValidateLLMContent([]*LLMContentBlock{large, large}), "exceeds")
	require.ErrorContains(t, (&LLMContentBlock{Kind: LLMContentToolResult, Content: []*LLMContentBlock{large, large}}).Validate(), "exceeds")
}

func TestLLMContentValidationExactSize(t *testing.T) {
	// Both blocks together are exactly at the limit.
	block := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: make([]byte, MaxLLMMediaBytes/2)}
	require.NoError(t, ValidateLLMContent([]*LLMContentBlock{block, block}))
	block.Data = make([]byte, MaxLLMMediaBytes)
	require.NoError(t, block.Validate())
	block.Data = make([]byte, MaxLLMMediaBytes+1)
	require.ErrorContains(t, block.Validate(), "exceeds")
}

func TestLLMContentAttributionOrder(t *testing.T) {
	image := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: []byte("hello")}
	origin := &LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentName: "reviewer", Ref: "#1", ReplyTo: "#2"}
	for name, blocks := range map[string][]*LLMContentBlock{
		"image only":        {image},
		"image before text": {image, {Kind: LLMContentText, Text: "describe"}},
		"text before image": {{Kind: LLMContentText, Text: "describe"}, image},
	} {
		t.Run(name, func(t *testing.T) {
			llm := (&LLM{endpointMtx: &sync.Mutex{}, mcp: &MCP{}}).WithContent(blocks, origin)
			before := llm.Messages[0].Clone()
			rendered := renderMessagesForModel(llm.Messages)
			require.Equal(t, LLMContentText, rendered[0].Content[0].Kind)
			require.True(t, strings.HasPrefix(rendered[0].Content[0].Text, origin.AttributionHeader()))
			require.Equal(t, before, llm.Messages[0], "rendering must not mutate stored content")
		})
	}
}

func TestLLMContentRecordedOrigin(t *testing.T) {
	msgs, err := decodeRecordedMessages([]byte(`[{"role":"USER","origin":{"kind":"AGENT","agentName":"reviewer","ref":"#1","replyTo":"#2"},"content":[{"kind":"IMAGE","mimeType":"image/png","data":"aGVsbG8="}]},{"role":"ASSISTANT","content":[{"kind":"TEXT","text":"a picture"}]}]`))
	require.NoError(t, err)
	require.Equal(t, &LLMMessageOrigin{Kind: LLMMessageOriginAgent, AgentName: "reviewer", Ref: "#1", ReplyTo: "#2"}, msgs[0].Origin)
	_, ctx := recordingTestRecorder(t)
	response, err := newRecordedResponseProvider(msgs).SendQuery(ctx, renderMessagesForModel(msgs[:1]), nil, nil)
	require.NoError(t, err)
	require.Equal(t, "a picture", response.TextContent())
}

func TestLLMContentRecordedMediaComparison(t *testing.T) {
	image := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: []byte("private picture bytes")}
	other := image.Clone()
	other.Data = []byte("different private picture bytes")
	// Neither the bytes nor their base64 form may appear in a diff.
	leaks := []string{
		string(image.Data), base64.StdEncoding.EncodeToString(image.Data),
		string(other.Data), base64.StdEncoding.EncodeToString(other.Data),
	}
	for _, nested := range []bool{false, true} {
		for name, mutate := range map[string]func([]*LLMContentBlock) []*LLMContentBlock{
			"bytes": func(blocks []*LLMContentBlock) []*LLMContentBlock { blocks[0].Data = other.Data; return blocks },
			"MIME":  func(blocks []*LLMContentBlock) []*LLMContentBlock { blocks[0].MIMEType = "image/jpeg"; return blocks },
			"kind":  func(blocks []*LLMContentBlock) []*LLMContentBlock { blocks[0].Kind = LLMContentAudio; return blocks },
			"order": func(blocks []*LLMContentBlock) []*LLMContentBlock {
				blocks[0], blocks[1] = blocks[1], blocks[0]
				return blocks
			},
			"missing": func(blocks []*LLMContentBlock) []*LLMContentBlock { return blocks[1:] },
		} {
			t.Run(fmt.Sprintf("nested=%t/%s", nested, name), func(t *testing.T) {
				expected := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{image.Clone(), other.Clone()}}
				actual := expected.Clone()
				actual.Content = mutate(actual.Content)
				if nested {
					expected.Content = []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call", Content: expected.Content}}
					actual.Content = []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call", Content: actual.Content}}
				}
				provider := newRecordedResponseProvider([]*LLMMessage{expected, {Role: LLMMessageRoleAssistant}})
				_, err := provider.SendQuery(context.Background(), []*LLMMessage{actual}, nil, nil)
				require.ErrorContains(t, err, "media mismatch")
				for _, leak := range leaks {
					require.NotContains(t, err.Error(), leak)
				}
				require.Equal(t, []byte(image.Data), recordingMedia(expected)[0].Data, "diff must not mutate the recording")
			})
		}
	}

	// Equal neighboring media must also be sanitized when only text differs.
	expected := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
		{Kind: LLMContentText, Text: "pid=1"}, image.Clone(),
		{Kind: LLMContentToolResult, Content: []*LLMContentBlock{other.Clone()}},
	}}
	actual := expected.Clone()
	actual.Content[0].Text = "pid=2"
	provider := newRecordedResponseProvider([]*LLMMessage{expected, {Role: LLMMessageRoleAssistant}})
	_, ctx := recordingTestRecorder(t)
	_, err := provider.SendQuery(ctx, []*LLMMessage{actual}, nil, nil)
	require.NoError(t, err, "legacy stabilized text matching must remain intact")
	actual.Content[0].Text = "different text"
	_, err = provider.SendQuery(ctx, []*LLMMessage{actual}, nil, nil)
	require.ErrorContains(t, err, "message history diverges")
	require.NotContains(t, err.Error(), "media mismatch")
	for _, leak := range leaks {
		require.NotContains(t, err.Error(), leak)
	}
	require.Equal(t, image.Data, actual.Content[1].Data, "diff must not mutate history")
}

func TestLLMContentFromBytes(t *testing.T) {
	for _, tc := range []struct {
		data, mime string
		kind       LLMContentBlockKind
	}{
		{"\x89PNG\r\n\x1a\n", "image/png", LLMContentImage},
		{"RIFF\x00\x00\x00\x00WAVE", "audio/wav", LLMContentAudio},
		{"%PDF-1.7\n", "application/pdf", LLMContentDocument},
	} {
		block, err := LLMContentFromBytes([]byte(tc.data), "")
		require.NoError(t, err)
		require.Equal(t, tc.mime, block.MIMEType)
		require.Equal(t, tc.kind, block.Kind)
		require.Equal(t, dagql.Bytes(tc.data), block.Data)
	}
	_, err := LLMContentFromBytes([]byte("plain text"), "")
	require.ErrorContains(t, err, "unsupported")
	_, err = LLMContentFromBytes(nil, "image/png")
	require.Error(t, err)
}

func TestLLMContentRoundTrip(t *testing.T) {
	image := &LLMContentBlock{Kind: LLMContentImage, MIMEType: "image/png", Data: []byte("hello")}
	blocks := []*LLMContentBlock{{Kind: LLMContentText, Text: "describe"}, image}
	llm := &LLM{endpointMtx: &sync.Mutex{}, mcp: &MCP{}}
	llm = llm.WithContent(blocks, &LLMMessageOrigin{Kind: LLMMessageOriginUser})
	llm = llm.WithToolResultBlocks("call1", "first", blocks, true)
	blocks[0].Text = "mutated"
	require.Equal(t, "describe", llm.Messages[0].Content[0].Text)
	require.Equal(t, "first\ndescribe\n[image: image/png]", llm.Messages[1].ToolResultContent())
	require.Contains(t, llm.Transcript(), "[image: image/png]")
	require.NotContains(t, llm.Transcript(), "hello")
	require.NotContains(t, llm.Transcript(), "aGVsbG8=")

	clone := llm.Messages[1].Clone()
	clone.Content[0].Content[0].Text = "clone mutation"
	require.Equal(t, "describe", llm.Messages[1].Content[0].Content[0].Text)
	call := &LLMContentBlock{Kind: LLMContentToolCall, Arguments: JSON(`{"x":1}`)}
	call.Clone().Arguments[0] = '['
	require.Equal(t, byte('{'), call.Arguments[0])
	image.Clone().Data[0] = 'j'
	require.Equal(t, byte('h'), image.Data[0])

	inputs, err := contentBlockInputs(llm.Messages[1].Content)
	require.NoError(t, err)
	resolved, err := inputs[0].Value.Resolve(context.Background())
	require.NoError(t, err)
	require.Equal(t, image.Data, resolved.Content[1].Data)
	// Media is base64 at the JSON boundary, so persisted conversations and
	// recordings keep their shape.
	encoded, err := json.Marshal(resolved)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"mime_type":"image/png"`)
	require.Contains(t, string(encoded), `"data":"aGVsbG8="`)
	var decoded LLMContentBlock
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, resolved, &decoded)
}

func TestLLMContentRecordedMedia(t *testing.T) {
	msgs, err := decodeRecordedMessages([]byte(`[{"role":"USER","content":[{"kind":"TOOL_RESULT","callId":"c1","text":"before","content":[{"kind":"IMAGE","mimeType":"image/png","data":"aGVsbG8="}]}]}]`))
	require.NoError(t, err)
	require.Equal(t, "image/png", msgs[0].Content[0].Content[0].MIMEType)
	require.Equal(t, dagql.Bytes("hello"), msgs[0].Content[0].Content[0].Data)
	require.Equal(t, "before\n[image: image/png]", msgs[0].ToolResultContent())
	_, err = decodeRecordedMessages([]byte(`[{"role":"USER","content":[{"kind":"IMAGE","mimeType":"image/png","data":"!"}]}]`))
	require.Error(t, err)
}

func TestLLMContentInputValidation(t *testing.T) {
	_, err := (LLMContentBlockInput{Kind: LLMContentImage, Data: dagql.Opt(dagql.Bytes("hello"))}).Resolve(context.Background())
	require.ErrorContains(t, err, "MIME")
	_, err = (LLMContentBlockInput{Kind: LLMContentImage, Data: dagql.Opt(dagql.Bytes("hello")), File: dagql.Opt(FileID{})}).Resolve(context.Background())
	require.ErrorContains(t, err, "exactly one")
	_, err = (LLMContentBlockInput{Kind: LLMContentText, File: dagql.Opt(FileID{})}).Resolve(context.Background())
	require.ErrorContains(t, err, "cannot contain")
	// The recursive input schema must remain finite and expose lowerCamel mimeType.
	spec := dagql.MustInputSpec(LLMContentBlockInput{})
	def := spec.TypeDefinition("")
	require.NotNil(t, def.Fields.ForName("mimeType"))
	require.Equal(t, "Bytes", def.Fields.ForName("data").Type.String())
	require.Equal(t, "[LLMContentBlockInput!]", def.Fields.ForName("content").Type.String())
	require.False(t, strings.Contains(def.Fields.ForName("file").Type.String(), "!"))
}
