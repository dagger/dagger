package httpdns

import (
	"context"
	"io"
	"net/http"

	"github.com/dagger/dagger/engine/session/networks"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/upload"
	"github.com/pkg/errors"
)

func newTransport(rt http.RoundTripper, sm *session.Manager, g session.Group, netConfig *networks.Config) http.RoundTripper {
	return &sessionHandler{
		rt: netconfhttp.NewTransport(rt, netConfig),
		sm: sm,
		g:  g,
	}
}

type sessionHandler struct {
	rt http.RoundTripper

	sm *session.Manager
	g  session.Group
}

func (h *sessionHandler) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "buildkit-session" {
		return h.handleSession(req)
	}

	return h.rt.RoundTrip(req)
}

func (h *sessionHandler) handleSession(req *http.Request) (*http.Response, error) {
	if req.Method != "GET" {
		return nil, errors.Errorf("invalid request")
	}

	var resp *http.Response
	err := h.sm.Any(context.TODO(), h.g, func(ctx context.Context, _ string, caller session.Caller) error {
		up, err := upload.New(context.TODO(), caller, req.URL)
		if err != nil {
			return err
		}

		pr, pw := io.Pipe()
		go func() {
			_, err := up.WriteTo(pw)
			pw.CloseWithError(err)
		}()

		resp = &http.Response{
			Status:        "200 OK",
			StatusCode:    200,
			Body:          pr,
			ContentLength: -1,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return resp, nil
}
