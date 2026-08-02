package core

import "testing"

func TestIsStreamingResponse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		accept     string
		reqBody    string
		respCT     string
		statusCode int
		want       bool
	}{
		{
			// OpenAI/Codex: streams via stream:true in the body, and its SSE
			// response does NOT carry a text/event-stream Content-Type. This is
			// the case that regressed — it must be detected as streaming.
			name:       "openai stream body, json response CT",
			reqBody:    `{"model":"gpt-5.5","stream":true,"input":[]}`,
			respCT:     "application/json",
			statusCode: 200,
			want:       true,
		},
		{
			name:       "anthropic accept header + event-stream CT",
			accept:     "text/event-stream",
			reqBody:    `{"model":"claude","stream":true}`,
			respCT:     "text/event-stream; charset=utf-8",
			statusCode: 200,
			want:       true,
		},
		{
			// Google streamGenerateContent: no stream:true in body, relies on CT.
			name:       "response CT fallback",
			reqBody:    `{"contents":[]}`,
			respCT:     "text/event-stream",
			statusCode: 200,
			want:       true,
		},
		{
			name:       "non-streaming request buffers",
			reqBody:    `{"model":"gpt-4.1","messages":[]}`,
			respCT:     "application/json",
			statusCode: 200,
			want:       false,
		},
		{
			// A streaming request that errors returns a JSON error body, which we
			// must buffer (not tee) so the detail can be parsed.
			name:       "streaming request that errors is buffered",
			reqBody:    `{"model":"gpt-5.5","stream":true}`,
			respCT:     "application/json",
			statusCode: 400,
			want:       false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := isStreamingResponse(tc.accept, []byte(tc.reqBody), tc.respCT, tc.statusCode)
			if got != tc.want {
				t.Errorf("isStreamingResponse(%q, %q, %q, %d) = %v, want %v",
					tc.accept, tc.reqBody, tc.respCT, tc.statusCode, got, tc.want)
			}
		})
	}
}
