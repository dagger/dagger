package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"go.dagger.io/dagger/api"
)

type Cloud2 struct {
	client *api.Client
	url    string
}

func NewCloud2() *Cloud2 {
	return &Cloud2{
		client: api.New(),
		url:    eventsURL(),
	}
}

func (c *Cloud2) Write(p []byte) (int, error) {
	event := map[string]interface{}{}
	d := json.NewDecoder(bytes.NewReader(p))
	if err := d.Decode(&event); err != nil {
		return 0, fmt.Errorf("cannot decode event: %s", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewBuffer(p))
	if err == nil {
		c.client.Do(req.Context(), req)
	}
	return len(p), nil
}
