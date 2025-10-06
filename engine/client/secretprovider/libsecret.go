package secretprovider

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/jedevc/go-libsecret"
)

// libsecretProvider looks up secrets using libsecret, which connects to
// gnome-keyring and other similar providers.
// https://specifications.freedesktop.org/secret-service-spec/latest/ref-dbus-api.html.
//
// Format:
// - libsecret://<collection>/<id>
// - libsecret://<collection>/<label>
// - libsecret://<collection>?<key>=<value>
//
//nolint:gocyclo
func libsecretProvider(_ context.Context, key string) ([]byte, error) {
	uri, err := url.Parse("libsecret://" + key)
	if err != nil {
		return nil, fmt.Errorf("key in bad format: %w", err)
	}
	uri.Path = strings.TrimPrefix(uri.Path, "/")

	svc, err := libsecret.NewService()
	if err != nil {
		return nil, err
	}
	session, err := svc.Open()
	if err != nil {
		return nil, err
	}

	collections, err := svc.Collections()
	if err != nil {
		return nil, err
	}
	var collection *libsecret.Collection
	for _, candidate := range collections {
		name := path.Base(string(candidate.Path()))
		if name == uri.Hostname() {
			collection = &candidate
			break
		}
	}
	if collection == nil {
		return nil, fmt.Errorf("collection %s not found", uri.Hostname())
	}

	err = svc.Unlock(collection)
	if err != nil {
		return nil, err
	}

	// get all items
	items, err := collection.Items()
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no items found in collection %s", uri.Hostname())
	}
	for _, item := range items {
		locked, err := item.Locked()
		if err != nil {
			return nil, err
		}
		if locked {
			// something has gone *wrong* - we've just called Unlock on the
			// collection, so nothing should be locked (but this does seem to
			// happen, Unlock doesn't seem to always return an error on failure)
			return nil, fmt.Errorf("item %s is locked", item.Path())
		}
	}

	// filter items using the path
	var matching []libsecret.Item
	if uri.Path == "" {
		// path is empty, just grab all
		// libsecret://<collection>
		matching = items
		if len(uri.Query()) == 0 {
			return nil, fmt.Errorf("item %s must be filtered", key)
		}
	}
	if matching == nil {
		for _, candidate := range items {
			name := path.Base(string(candidate.Path()))
			if name == uri.Path {
				// path contains an auto-generated item specific identifier
				// libsecret://<collection>/<id>
				matching = append(matching, candidate)
			}
		}
	}
	if matching == nil {
		for _, candidate := range items {
			label, err := candidate.Label()
			if err != nil {
				return nil, err
			}
			if label == uri.Path {
				// path contains the human-readable label
				// libsecret://<collection>/<label>
				matching = append(matching, candidate)
			}
		}
	}
	items = matching

	// filter items using attributes
	matching = nil
	for _, item := range items {
		attrs, err := item.Attributes()
		if err != nil {
			return nil, err
		}
		matches := true
		for k, vs := range uri.Query() {
			v := vs[0]
			if attrs[k] != v {
				matches = false
				break
			}
		}
		if matches {
			matching = append(matching, item)
		}
	}
	items = matching

	if len(items) == 0 {
		return nil, fmt.Errorf("item %s not found", key)
	}
	if len(items) > 1 {
		return nil, fmt.Errorf("too many items found for %s", key)
	}

	item := items[0]
	secret, err := item.GetSecret(session)
	if err != nil {
		return nil, fmt.Errorf("could not get secret %s: %w", key, err)
	}

	return secret.Value, nil
}
