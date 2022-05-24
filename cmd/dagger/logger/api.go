package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type APIOutput struct {
	Out io.Writer
}

func (c *APIOutput) Write(p []byte) (int, error) {
	event := map[string]interface{}{}
	d := json.NewDecoder(bytes.NewReader(p))
	if err := d.Decode(&event); err != nil {
		return 0, fmt.Errorf("cannot decode event: %s", err)
	}

	return fmt.Fprintln(c.Out, fmt.Sprintf("%+v", event))
}
