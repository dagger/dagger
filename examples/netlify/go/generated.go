// Code generated by github.com/99designs/gqlgen, DO NOT EDIT.

package main

import (
	"context"
	"encoding/json"

	"github.com/dagger/cloak/sdk/go/dagger"
)

func (r *deploy) url(ctx context.Context, obj *Deploy) (string, error) {

	return obj.URL, nil

}

func (r *deploy) deployURL(ctx context.Context, obj *Deploy) (string, error) {

	return obj.DeployURL, nil

}

func (r *deploy) logsURL(ctx context.Context, obj *Deploy) (*string, error) {

	return obj.LogsURL, nil

}

func (r *query) netlify(ctx context.Context) (*Netlify, error) {

	return new(Netlify), nil

}

type deploy struct{}
type netlify struct{}
type query struct{}

func main() {
	dagger.Serve(context.Background(), map[string]func(context.Context, dagger.ArgsInput) (interface{}, error){
		"Deploy.url": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			obj := new(Deploy)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&deploy{}).url(ctx,

				obj,
			)
		},
		"Deploy.deployURL": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			obj := new(Deploy)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&deploy{}).deployURL(ctx,

				obj,
			)
		},
		"Deploy.logsURL": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			obj := new(Deploy)
			bytes, err = json.Marshal(fc.ParentResult)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, obj); err != nil {
				return nil, err
			}

			return (&deploy{}).logsURL(ctx,

				obj,
			)
		},
		"Netlify.deploy": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			var contents dagger.FSID

			bytes, err = json.Marshal(fc.Args["contents"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &contents); err != nil {
				return nil, err
			}

			var subdir string

			bytes, err = json.Marshal(fc.Args["subdir"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &subdir); err != nil {
				return nil, err
			}

			var siteName string

			bytes, err = json.Marshal(fc.Args["siteName"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &siteName); err != nil {
				return nil, err
			}

			var token dagger.SecretID

			bytes, err = json.Marshal(fc.Args["token"])
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(bytes, &token); err != nil {
				return nil, err
			}

			return (&netlify{}).deploy(ctx,

				contents,

				&subdir,

				&siteName,

				token,
			)
		},
		"Query.netlify": func(ctx context.Context, fc dagger.ArgsInput) (interface{}, error) {
			var bytes []byte
			_ = bytes
			var err error
			_ = err

			return (&query{}).netlify(ctx)
		},
	})
}
