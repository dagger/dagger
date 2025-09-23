package core

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dagger/dagger/core/modfunccache"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

//go:embed modfunccache/schema.sql
var ModFuncCacheSchema string

type CallExpirationCache struct {
	db *modfunccache.Queries
}

func NewCallExpirationCache(ctx context.Context, dbPath string) (*CallExpirationCache, error) {
	connURL := &url.URL{
		Scheme: "file",
		Path:   dbPath,
		RawQuery: url.Values{
			"_pragma": []string{
				// ref: https://www.sqlite.org/pragma.html
				"journal_mode=WAL",
				"busy_timeout=10000", // wait up to 10s when there are concurrent writers
				// TODO: handle loading corrupt db on startup
				"synchronous=OFF",

				// TODO: ?
				// cache_size
				// threads
				// optimize https://www.sqlite.org/pragma.html#pragma_optimize
			},
			"_txlock": []string{"immediate"}, // use BEGIN IMMEDIATE for transactions
		}.Encode(),
	}
	db, err := sql.Open("sqlite", connURL.String())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", connURL, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", connURL, err)
	}
	if _, err := db.Exec(ModFuncCacheSchema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	queries, err := modfunccache.Prepare(ctx, db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("prepare queries: %w", err)
	}

	return &CallExpirationCache{db: queries}, nil
}

func (f *CallExpirationCache) GetOrInitExpiration(ctx context.Context, key string, ttl int64, clientID string) (string, func(context.Context) error, error) {
	now := time.Now().Unix()
	newExpiration := now + ttl

	call, err := f.db.SelectCall(ctx, key)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
	default:
		return "", nil, fmt.Errorf("select call: %w", err)
	}

	switch {
	case call == nil:
		// Nothing saved in the cache yet, use a new mixin. Don't store yet, that only happens
		// once a call completes successfully and has been determined to be safe to cache.
		newMixin := newExpirationMixin(newExpiration, clientID)
		return newMixin, func(ctx context.Context) error {
			return f.db.SetExpiration(ctx, modfunccache.SetExpirationParams{
				Key:        key,
				Mixin:      newMixin,
				Expiration: newExpiration,
				PrevMixin:  "",
			})
		}, nil

	case call.Expiration < now:
		// We do have a cached entry, but it expired, so don't use it. Use a new mixin, but again
		// don't store it yet until the call completes successfully and is determined to be safe
		// to cache.
		newMixin := newExpirationMixin(newExpiration, clientID)
		return newMixin, func(ctx context.Context) error {
			return f.db.SetExpiration(ctx, modfunccache.SetExpirationParams{
				Key:        key,
				Mixin:      newMixin,
				Expiration: newExpiration,
				PrevMixin:  call.Mixin,
			})
		}, nil

	default:
		// We have a cached entry and it hasn't expired yet, use the cached mixin
		return call.Mixin, func(context.Context) error { return nil }, nil
	}
}

func newExpirationMixin(newExpiration int64, clientID string) string {
	return dagql.HashFrom(strconv.Itoa(int(newExpiration)), clientID).String()
}

func (f *CallExpirationCache) GCLoop(ctx context.Context) {
	for range time.Tick(10 * time.Minute) {
		now := time.Now().Unix()
		if err := f.db.GCExpiredCalls(ctx, modfunccache.GCExpiredCallsParams{
			Now: now,
		}); err != nil {
			slog.Warn("failed to GC expired function calls", "err", err)
		}
	}
}
