package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/imroc/req/v3"
	"go.dagger.io/dagger/version"
)

type Cloud struct {
	client *req.Client
	url    string
}

func NewCloud() *Cloud {
	return &Cloud{
		client: req.C().
			SetUserAgent(version.String()).
			SetTimeout(1 * time.Second),
		url: eventsURL(),
	}
}

func (c *Cloud) Send(p []byte) {
	c.client.R().
		SetBodyJsonBytes(p).
		Post(c.url)
}

func (c *Cloud) Write(p []byte) (int, error) {
	event := map[string]interface{}{}
	d := json.NewDecoder(bytes.NewReader(p))
	if err := d.Decode(&event); err != nil {
		return 0, fmt.Errorf("cannot decode event: %s", err)
	}
	c.Send(p)
	return len(p), nil
}

func eventsURL() string {
	url := os.Getenv("DAGGER_CLOUD_EVENTS_URL")
	if url == "" {
		url = "http://localhost:8020/events"
	}
	return url
}
