package engine

import (
	"encoding/json"
	"errors"
	"fmt"
)

type FrontendOpts struct {
	ServerID         string            `json:"server_id,omitempty"`
	ClientSessionID  string            `json:"client_session_id,omitempty"`
	CacheConfigType  string            `json:"cache_config_type,omitempty"`
	CacheConfigAttrs map[string]string `json:"cache_config_attrs,omitempty"`
}

func (f FrontendOpts) ServerAddr() string {
	return fmt.Sprintf("unix://%s", f.ServerSockPath())
}

func (f FrontendOpts) ServerSockPath() string {
	return fmt.Sprintf("/run/dagger/server-%s.sock", f.ServerID)
}

func (f *FrontendOpts) FromSolveOpts(opts map[string]string) error {
	strVal, ok := opts[DaggerFrontendOptsKey]
	if !ok {
		return nil
	}
	err := json.Unmarshal([]byte(strVal), f)
	if err != nil {
		return err
	}
	if f.ServerID == "" {
		return errors.New("missing server id from frontend opts")
	}
	return nil
}

func (f FrontendOpts) ToSolveOpts() (map[string]string, error) {
	if f.ServerID == "" {
		return nil, errors.New("missing server id from frontend opts")
	}
	b, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		DaggerFrontendOptsKey: string(b),
	}, nil
}
