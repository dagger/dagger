package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Most tests inspect queued histories only: no provider credentials or LLM
// requests are needed, including when reconstructing a portable conversation.
// The continuation test runs the loop against a canned recording instead.
const mediaPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="
const mediaWAV = "UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA="
const mediaPDF = "%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\n%%EOF\n"

const mediaMessagesSelection = `messages { role content {
	kind text data mimeType callId errored
	content { kind text data mimeType }
} }`

type mediaBlock struct {
	Kind     string       `json:"kind"`
	Text     string       `json:"text"`
	Data     string       `json:"data"`
	MIMEType string       `json:"mimeType"`
	CallID   string       `json:"callId"`
	Errored  bool         `json:"errored"`
	Content  []mediaBlock `json:"content"`
}

type mediaMessage struct {
	Role    string       `json:"role"`
	Content []mediaBlock `json:"content"`
}

func mediaHistory(t *testctx.T, c *dagger.Client, llm *dagger.LLM) []mediaMessage {
	t.Helper()
	id, err := llm.ID(t.Context())
	require.NoError(t, err)
	res, err := testutil.QueryWithClient[struct {
		Node struct {
			Messages []mediaMessage `json:"messages"`
		} `json:"node"`
	}](c, t, `query($id: ID!) { node(id: $id) { ... on LLM { `+mediaMessagesSelection+` } } }`,
		&testutil.QueryOptions{Variables: map[string]any{"id": id}})
	require.NoError(t, err)
	return res.Node.Messages
}

func (LLMSuite) TestMediaContentFiles(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	png := c.Container().From(alpineImage).
		WithNewFile("/image.b64", mediaPNG).
		WithExec([]string{"sh", "-c", "base64 -d /image.b64 > /image.png"}).File("/image.png")
	pdf := c.Directory().WithNewFile("document.pdf", mediaPDF).File("document.pdf")

	for _, tc := range []struct {
		name, kind, mime, data string
		file                   *dagger.File
	}{
		{"image", "IMAGE", "image/png", mediaPNG, png},
		{"document", "DOCUMENT", "application/pdf", base64.StdEncoding.EncodeToString([]byte(mediaPDF)), pdf},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			llm := c.LLM().WithContentFile(tc.file)
			messages := mediaHistory(t, c, llm)
			require.Len(t, messages, 1)
			require.Equal(t, "USER", messages[0].Role)
			require.Len(t, messages[0].Content, 1)
			block := messages[0].Content[0]
			require.Equal(t, tc.kind, block.Kind)
			require.Equal(t, tc.mime, block.MIMEType)
			require.Equal(t, tc.data, block.Data)
			fromBlock := c.LLM().WithContent([]dagger.LLMContentBlockInput{{
				Kind: dagger.LLMContentBlockKind(tc.kind), File: tc.file,
			}})
			require.Equal(t, messages, mediaHistory(t, c, fromBlock))

			transcript, err := llm.Transcript(ctx)
			require.NoError(t, err)
			require.Contains(t, transcript, tc.mime)
			require.NotContains(t, transcript, tc.data)

			// Portable reconstruction must contain the resolved bytes rather than
			// depend on the original File or its producing container.
			id, err := sink.captureLLMRecipe(ctx, t, c, llm)
			require.NoError(t, err)
			recipe := new(call.ID)
			require.NoError(t, recipe.Decode(string(id)))
			for cur := recipe; cur != nil; cur = cur.Receiver() {
				require.NotEqual(t, "withContentFile", cur.Field(), "portable media must not retain the file-producing recipe")
			}
			reloaded := dagger.Ref[*dagger.LLM](c, id)
			require.Equal(t, messages, mediaHistory(t, c, reloaded))
		})
	}

	// The existing text API must not start interpreting PDF-looking prompts as
	// documents. Binary media is explicitly opt-in through withContentFile.
	messages := mediaHistory(t, c, c.LLM().WithPromptFile(pdf))
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)
	require.Equal(t, "TEXT", messages[0].Content[0].Kind)
	require.Equal(t, mediaPDF, messages[0].Content[0].Text)
	require.Empty(t, messages[0].Content[0].Data)
}

func (LLMSuite) TestMediaContentBlocks(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	pdf := base64.StdEncoding.EncodeToString([]byte(mediaPDF))
	llm := c.LLM().WithContent([]dagger.LLMContentBlockInput{
		{Kind: dagger.LLMContentBlockKindText, Text: "Compare these:"},
		{Kind: dagger.LLMContentBlockKindImage, Data: mediaPNG, MimeType: "image/png"},
		{Kind: dagger.LLMContentBlockKindText, Text: "with this document"},
		{Kind: dagger.LLMContentBlockKindDocument, Data: pdf, MimeType: "application/pdf"},
		{Kind: dagger.LLMContentBlockKindAudio, Data: mediaWAV, MimeType: "audio/wav"},
	})
	messages := mediaHistory(t, c, llm)
	require.Len(t, messages, 1)
	blocks := messages[0].Content
	require.Len(t, blocks, 5)
	require.Equal(t, []string{"TEXT", "IMAGE", "TEXT", "DOCUMENT", "AUDIO"}, []string{
		blocks[0].Kind, blocks[1].Kind, blocks[2].Kind, blocks[3].Kind, blocks[4].Kind,
	})
	require.Equal(t, "Compare these:", blocks[0].Text)
	require.Equal(t, mediaPNG, blocks[1].Data)
	require.Equal(t, "with this document", blocks[2].Text)
	require.Equal(t, pdf, blocks[3].Data)
	require.Equal(t, mediaWAV, blocks[4].Data)
	require.Equal(t, "audio/wav", blocks[4].MIMEType)

	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "Compare these:")
	require.Contains(t, transcript, "with this document")
	require.NotContains(t, transcript, mediaPNG)
	require.NotContains(t, transcript, pdf)
	require.NotContains(t, transcript, mediaWAV)

	id, err := sink.captureLLMRecipe(ctx, t, c, llm)
	require.NoError(t, err)
	require.Equal(t, messages, mediaHistory(t, c, dagger.Ref[*dagger.LLM](c, id)))

	// withResponse accepts the same recursive input type, so exported assistant
	// media can be reconstructed without a model request as well.
	response := c.LLM().WithResponse([]dagger.LLMContentBlockInput{
		{Kind: dagger.LLMContentBlockKindImage, Data: mediaPNG, MimeType: "image/png"},
	})
	responseMessages := mediaHistory(t, c, response)
	require.Len(t, responseMessages, 1)
	require.Equal(t, "ASSISTANT", responseMessages[0].Role)
	require.Equal(t, mediaPNG, responseMessages[0].Content[0].Data)
}

func (LLMSuite) TestMediaToolResultBlocks(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	llm := c.LLM().WithToolResult("media-call", "legacy text", false, dagger.LLMWithToolResultOpts{
		Blocks: []dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "caption"},
			{Kind: dagger.LLMContentBlockKindImage, Data: mediaPNG, MimeType: "image/png"},
		},
	})
	messages := mediaHistory(t, c, llm)
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)
	result := messages[0].Content[0]
	require.Equal(t, "TOOL_RESULT", result.Kind)
	require.Equal(t, "media-call", result.CallID)
	require.False(t, result.Errored)
	require.Equal(t, "legacy text", result.Text)
	require.Len(t, result.Content, 2)
	require.Equal(t, "caption", result.Content[0].Text)
	require.Equal(t, mediaPNG, result.Content[1].Data)

	id, err := sink.captureLLMRecipe(ctx, t, c, llm)
	require.NoError(t, err)
	require.Equal(t, messages, mediaHistory(t, c, dagger.Ref[*dagger.LLM](c, id)))

	// Exercise recursive input decoding directly, not just the blocks argument.
	reconstructed := c.LLM().WithResponse([]dagger.LLMContentBlockInput{
		{Kind: dagger.LLMContentBlockKindToolResult, CallID: "media-call", Content: []dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindImage, Data: mediaPNG, MimeType: "image/png"},
		}},
	})
	rebuilt := mediaHistory(t, c, reconstructed)
	require.Len(t, rebuilt, 1)
	require.Equal(t, mediaPNG, rebuilt[0].Content[0].Content[0].Data)
}

func (LLMSuite) TestMediaReturnedConversationDisplay(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const prompt = "show the screenshot"
	const caption = "Screenshot from the browser"
	conversation := c.LLM().
		WithPrompt(prompt).
		WithResponse([]dagger.LLMContentBlockInput{{
			Kind: dagger.LLMContentBlockKindToolCall, CallID: "screenshot", ToolName: "viewScreenshot",
		}}).
		WithContent([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: caption},
			{Kind: dagger.LLMContentBlockKindImage, Data: mediaPNG, MimeType: "image/png"},
		}).
		WithToolResult("screenshot", "", false).
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "done"}})
	id, err := conversation.ID(ctx)
	require.NoError(t, err)
	// Unlike the text-only cannedRecordingModel helper, preserve media bytes in
	// the recording: the provider checks the actual resumed conversation's image.
	recorded, err := testutil.QueryWithClient[struct {
		Node struct {
			Messages json.RawMessage `json:"messages"`
		} `json:"node"`
	}](c, t, `query($id: ID!) { node(id: $id) { ... on LLM {
		messages { role content { kind text callId toolName arguments data mimeType } }
	} } }`, &testutil.QueryOptions{Variables: map[string]any{"id": id}})
	require.NoError(t, err)
	model := "recording/" + base64.StdEncoding.EncodeToString(recorded.Node.Messages)

	// Return LLM!, not a media tool result. The loop appends a continuation
	// tool result AFTER withContent's user message, then takes another step.
	ctr := workspaceBase(t, c).
		WithNewFile("dagger.toml", "[modules.browser]\nsource = \"browser\"\n").
		WithNewFile("browser/dagger.json", `{"name":"browser","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("browser/main.dang", fmt.Sprintf(`
type Browser {
  viewScreenshot(llm: LLM!): LLM! {
    llm.withContent([
      LLMContentBlockInput(kind: LLMContentBlockKind.TEXT, text: %q),
      LLMContentBlockInput(kind: LLMContentBlockKind.IMAGE, data: %q, mimeType: "image/png")
    ])
  }
}
`, caption, mediaPNG)).
		WithExec([]string{"dagger", "--progress=plain", "-vv", "script"}, dagger.ContainerWithExecOpts{
			Stdin: fmt.Sprintf(`llm --model=%q | with-tools $(browser) | with-prompt %q | loop | last-reply`, model, prompt),
		})
	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "done", strings.TrimSpace(out), "the loop must resume from the returned conversation")
	logs, err := ctr.Stderr(ctx)
	require.NoError(t, err)
	// Assert frontend output, NOT transcript: history already contained the
	// image when emitNewMessageSpans stopped scanning at the tool result.
	require.Contains(t, logs, caption+"[image: image/png]")
}

func (LLMSuite) TestMediaInvalidContent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name  string
		block map[string]any
	}{
		{"invalid base64", map[string]any{"kind": "IMAGE", "mimeType": "image/png", "data": "not base64!"}},
		{"missing data", map[string]any{"kind": "IMAGE", "mimeType": "image/png"}},
		{"wrong MIME kind", map[string]any{"kind": "IMAGE", "mimeType": "application/pdf", "data": mediaPNG}},
		{"unsupported MIME", map[string]any{"kind": "DOCUMENT", "mimeType": "application/octet-stream", "data": mediaPNG}},
		{"text carrying media", map[string]any{"kind": "TEXT", "text": "caption", "data": mediaPNG}},
		{"tool call as user content", map[string]any{"kind": "TOOL_CALL", "callId": "call", "toolName": "read"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			_, err := testutil.QueryWithClient[map[string]any](c, t,
				`query($content: [LLMContentBlockInput!]!) { llm { withContent(content: $content) { id } } }`,
				&testutil.QueryOptions{Variables: map[string]any{"content": []any{tc.block}}})
			require.Error(t, err)
		})
	}

	fileID, err := c.Directory().WithNewFile("document.pdf", mediaPDF).File("document.pdf").ID(ctx)
	require.NoError(t, err)
	_, err = testutil.QueryWithClient[map[string]any](c, t,
		`query($file: ID!) { llm { withContent(content: [{kind: DOCUMENT, file: $file, data: "cGRm", mimeType: "application/pdf"}]) { id } } }`,
		&testutil.QueryOptions{Variables: map[string]any{"file": fileID}})
	require.Error(t, err, "a media block cannot have both file and inline data")
}

func (LLMSuite) TestMediaContentSizeLimit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name  string
		size  int
		count int
	}{
		{"one oversized file", 20*1024*1024 + 1, 1},
		{"aggregate message limit", 11 * 1024 * 1024, 2},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			file := c.Container().From(alpineImage).
				WithExec([]string{"sh", "-c", fmt.Sprintf("printf '%%%%PDF-1.4\\n' > /large.pdf; head -c %d /dev/zero >> /large.pdf", tc.size)}).
				File("/large.pdf")
			id, err := file.ID(ctx)
			require.NoError(t, err)
			blocks := make([]map[string]any, tc.count)
			for i := range blocks {
				blocks[i] = map[string]any{"kind": "DOCUMENT", "file": id}
			}
			_, err = testutil.QueryWithClient[map[string]any](c, t,
				`query($content: [LLMContentBlockInput!]!) { llm { withContent(content: $content) { id } } }`,
				&testutil.QueryOptions{Variables: map[string]any{"content": blocks}})
			require.Error(t, err)
			require.Contains(t, err.Error(), "20971520")
		})
	}
}
