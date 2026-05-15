package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

type Test struct{}

func (m *Test) GetHttp(ctx context.Context) (string, error) {
	resp, err := http.Get("https://server")
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	bs, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(bs), nil
}
