package dagql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
	"github.com/google/uuid"
	"github.com/ngrok/sqlmw"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var Schema string

func init() {
	// sql.Register("sqlite-mw", sqlmw.Driver(&sqlite.Driver{}, &sqlInterceptor{}))
}

func NewDB(dbPath string) (*sql.DB, error) {
	// TODO: go through all the pragmas
	connURL := &url.URL{
		Scheme: "file",
		Host:   "",
		Path:   dbPath,
		RawQuery: url.Values{
			"_pragma": []string{
				"foreign_keys=ON",
				"journal_mode=WAL",
				"synchronous=OFF",
				"busy_timeout=10000",
			},
			"_txlock": []string{"immediate"},
		}.Encode(),
	}

	// db, err := sql.Open("sqlite-mw", connURL.String())
	db, err := sql.Open("sqlite", connURL.String())
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", connURL, err)
	}

	if _, err := db.Exec(Schema); err != nil {
		return nil, fmt.Errorf("failed to run schema migration: %w", err)
	}

	return db, nil
}

type sharedCacheResult struct {
	// mu sync.Mutex

	cache *DagqlCache

	typed Typed

	resultID string

	refCount int

	persisted bool

	inputs map[int]CacheResult
	// dependents map[int]CacheResult
	digests map[digest.Digest]struct{}
}

type cacheResult struct {
	*sharedCacheResult
	resultDigest string
}

type CacheResult interface {
	Result
	Release(context.Context) error
	// PostCall(context.Context) error
}

var _ CacheResult = &cacheResult{}

func (cr *sharedCacheResult) Type() *ast.Type {
	return cr.typed.Type()
}

func (cr *sharedCacheResult) FromJSON(ctx context.Context, bs []byte) (Typed, error) {
	if err := json.Unmarshal(bs, &cr.typed); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	return cr, nil
}

func (cr *sharedCacheResult) Unwrap() Typed {
	return cr.typed
}

func (cr *sharedCacheResult) ResultID() string {
	return cr.resultID
}

func (cr *sharedCacheResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(cr.typed)
}

func (cr *cacheResult) Release(ctx context.Context) error {
	cr.cache.mu.Lock()
	defer cr.cache.mu.Unlock()

	cr.refCount--
	if cr.refCount > 0 {
		return nil
	}

	if cr.persisted {
		// free memory for the result, but hold onto the shared result metadata in memory
		cr.sharedCacheResult.typed = nil
	} else {
		delete(cr.cache.sharedResults, cr.resultID)
		delete(cr.cache.resultsByDigest, digest.Digest(cr.resultDigest))
	}

	/* TODO: pruning, something like
	if err := cr.cache.db.ExecContext(ctx, "DELETE FROM results WHERE result_id = ?", cr.resultID); err != nil {
		return fmt.Errorf("failed to delete result: %w", err)
	}
	*/

	return nil
}

func (cr *cacheResult) ResultDigest() string {
	return cr.resultDigest
}

// TODO:
func (cr *cacheResult) String() string {
	return fmt.Sprintf("&{sharedCacheResult:%+v resultDigest:%s}", cr.sharedCacheResult, cr.resultDigest)
}

type DagqlCache struct {
	mu sync.Mutex

	sharedResults   map[string]*sharedCacheResult
	resultsByDigest map[digest.Digest]*cacheResult

	db *sql.DB
}

func NewDagqlCache(ctx context.Context, db *sql.DB) (*DagqlCache, error) {
	cache := &DagqlCache{
		sharedResults:   make(map[string]*sharedCacheResult),
		resultsByDigest: make(map[digest.Digest]*cacheResult),
		db:              db,
	}

	// Load all persisted results into memory
	rows, err := db.QueryContext(ctx, "SELECT result_id, json FROM results")
	if err != nil {
		return nil, fmt.Errorf("failed to query results: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var resultID string
		var jsonBlob []byte
		if err := rows.Scan(&resultID, &jsonBlob); err != nil {
			return nil, fmt.Errorf("failed to scan result row: %w", err)
		}

		sharedRes := &sharedCacheResult{
			cache:     cache,
			typed:     nil, // lazy load when needed
			resultID:  resultID,
			refCount:  0,
			persisted: true,
			inputs:    make(map[int]CacheResult),
			digests:   make(map[digest.Digest]struct{}),
		}
		cache.sharedResults[resultID] = sharedRes
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating result rows: %w", err)
	}

	// Load all digest mappings
	digestRows, err := db.QueryContext(ctx, "SELECT result_id, digest FROM resultDigests")
	if err != nil {
		return nil, fmt.Errorf("failed to query result digests: %w", err)
	}
	defer digestRows.Close()

	for digestRows.Next() {
		var resultID string
		var digestStr string
		if err := digestRows.Scan(&resultID, &digestStr); err != nil {
			return nil, fmt.Errorf("failed to scan digest row: %w", err)
		}

		sharedRes, ok := cache.sharedResults[resultID]
		if !ok {
			// Skip orphaned digests
			// TODO: should we? ^ error instead?
			continue
		}

		dgst := digest.Digest(digestStr)
		sharedRes.digests[dgst] = struct{}{}

		cacheRes := &cacheResult{
			sharedCacheResult: sharedRes,
			resultDigest:      digestStr,
		}
		cache.resultsByDigest[dgst] = cacheRes
	}
	if err := digestRows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating digest rows: %w", err)
	}

	return cache, nil
}

// TODO: feels partially duped with other CacheConfig struct
type callCacheParams struct {
	DoNotCache bool
	Persist    bool
}

func (c *DagqlCache) Call(
	ctx context.Context,
	s *Server,
	parent Object,
	newID *call.ID,
	inputArgs map[string]Input,
	callCacheParams callCacheParams,
) (_ CacheResult, rerr error) {
	ctx = idToContext(ctx, newID)

	res, err := c.call(ctx, s, parent, newID.Field(), View(newID.View()), inputArgs, newID.Digest(), callCacheParams)
	if err != nil {
		return nil, fmt.Errorf("failed to call field %q: %w", newID.Field(), err)
	}
	return res, nil
}

func newResultID() string {
	return uuid.NewString()
}

func (c *DagqlCache) call(
	ctx context.Context,
	s *Server,
	parent Object,
	fieldName string,
	view View,
	inputArgs map[string]Input,
	resultDigest digest.Digest,
	callCacheParams callCacheParams,
) (_ CacheResult, rerr error) {
	// TODO: split up into more separate methods once settled, for readability

	startedAt := time.Now()
	lg := slog.With(
		"field", fmt.Sprintf("%s.%s", parent.ObjectType().TypeName(), fieldName),
		"doNotCache", callCacheParams.DoNotCache,
		"digest", resultDigest.String(),
	)

	//
	// No cache case
	//

	if callCacheParams.DoNotCache {
		typedVal, err := parent.ObjectType().Call(ctx, parent, fieldName, view, inputArgs)
		if err != nil {
			return nil, fmt.Errorf("failed to call field %q: %w", fieldName, err)
		}
		if n, ok := typedVal.(Derefable); ok {
			typedVal, ok = n.Deref()
			if !ok {
				return nil, nil
			}
		}

		sharedRes := &sharedCacheResult{
			cache:    c,
			typed:    typedVal,
			resultID: "",
			refCount: 1,
		}
		res := &cacheResult{
			sharedCacheResult: sharedRes,
			resultDigest:      resultDigest.String(),
		}
		return res, nil
	}

	//
	// Fast-path cache check
	//

	field, ok := parent.ObjectType().FieldSpec(fieldName, view)
	if !ok {
		return nil, fmt.Errorf("%s has no such field: %q", parent.ObjectType().TypeName(), fieldName)
	}

	c.mu.Lock()
	existingResult, ok := c.resultsByDigest[resultDigest]
	if ok {
		defer c.mu.Unlock()
		existingResult.refCount++

		if existingResult.typed == nil {
			// need to load the result from the DB now
			var jsonBlob []byte
			err := sq.Select("json").
				From("results").
				Where(sq.Eq{"result_id": existingResult.resultID}).
				Limit(1).
				RunWith(c.db).
				QueryRowContext(ctx).
				Scan(&jsonBlob)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, fmt.Errorf("result ID %s not found in DB", existingResult.resultID)
				}
				return nil, fmt.Errorf("failed to query result JSON: %w", err)
			}
			typedVal, err := field.Type.FromJSON(ctx, jsonBlob)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal result JSON during fast-path cache hit: %w", err)
			}
			existingResult.typed = typedVal
		}

		return existingResult, nil
	}
	c.mu.Unlock()

	//
	// Slower-path cache check
	//

	// TODO: needs to handle modules (where type names may overlap)
	metaDigestInputs := []string{
		parent.ObjectType().TypeName(),
		fieldName,
	}
	metaDigest := HashFrom(metaDigestInputs...)

	var resultID string

	var argResults []Result

	if callCacheParams.Persist {
		args := make([]NamedInput, 0, len(inputArgs))
		for name, arg := range inputArgs {
			args = append(args, NamedInput{Name: name, Value: arg})
		}
		slices.SortFunc(args, func(a, b NamedInput) int {
			return strings.Compare(a.Name, b.Name)
		})

		argResults = make([]Result, 0, len(args))
		for _, arg := range args {
			var argResult Result
			if arg.Value != nil {
				var err error
				argResult, err = arg.Value.ToResult(ctx, s)
				if err != nil {
					return nil, fmt.Errorf("failed to convert arg %q to result: %w", arg.Name, err)
				}
			}
			argResults = append(argResults, argResult)
		}

		var hitCache bool
		var err error
		resultID, hitCache, err = c.checkCache(ctx, metaDigest, parent, argResults)
		if err != nil {
			return nil, fmt.Errorf("failed to check cache: %w", err)
		}
		if hitCache {
			c.mu.Lock()
			defer c.mu.Unlock()

			sharedRes, ok := c.sharedResults[resultID]
			if !ok {
				// everything should be mirrored in memory, invalid state
				return nil, fmt.Errorf("result ID %s not loaded", resultID)
			}

			if sharedRes.typed == nil {
				// need to load the result from the DB now
				var jsonBlob []byte
				err := sq.Select("json").
					From("results").
					Where(sq.Eq{"result_id": resultID}).
					Limit(1).
					RunWith(c.db).
					QueryRowContext(ctx).
					Scan(&jsonBlob)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return nil, fmt.Errorf("result ID %s not found in DB", resultID)
					}
					return nil, fmt.Errorf("failed to query result JSON: %w", err)
				}
				typedVal, err := field.Type.FromJSON(ctx, jsonBlob)
				if err != nil {
					return nil, fmt.Errorf("failed to unmarshal result JSON during slow-path cache hit: %w", err)
				}
				sharedRes.typed = typedVal
			}

			sharedRes.refCount++
			_, alreadyKnownDigest := sharedRes.digests[resultDigest]
			if !alreadyKnownDigest {
				sharedRes.digests[resultDigest] = struct{}{}

				insertDigstQ, insertDigestArgs, err := sq.Insert("resultDigests").
					Values(resultID, resultDigest.String()).
					Suffix("ON CONFLICT DO NOTHING").
					ToSql()
				if err != nil {
					return nil, fmt.Errorf("failed to build insert digest statement: %w", err)
				}
				if _, err := c.db.ExecContext(ctx, insertDigstQ, insertDigestArgs...); err != nil {
					return nil, fmt.Errorf("failed to insert digest: %w", err)
				}
			}

			res := &cacheResult{
				sharedCacheResult: sharedRes,
				resultDigest:      resultDigest.String(),
			}
			c.resultsByDigest[resultDigest] = res
			return res, nil
		}
	}

	//
	// Cache miss, run it
	//

	typedVal, err := parent.ObjectType().Call(ctx, parent, fieldName, view, inputArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to call field %q: %w", fieldName, err)
	}
	if n, ok := typedVal.(Derefable); ok {
		typedVal, ok = n.Deref()
		if !ok {
			return nil, nil
		}
	}

	//
	// Did the call return another result from its own call it made internally?
	// TODO: we should probably be writing to the DB in this path ideally still, right?
	//
	if existingCacheRes, ok := typedVal.(CacheResult); ok {
		c.mu.Lock()
		defer c.mu.Unlock()

		// TODO: INCREMENT REF COUNT ?????

		lg.
			With("duration", time.Since(startedAt)).
			Debug("EXECUTE RETURNED ANOTHER CACHERESULT")
		return existingCacheRes, nil
	}
	if existingRes, ok := typedVal.(Result); ok {
		c.mu.Lock()
		defer c.mu.Unlock()

		resID := existingRes.ResultID()
		if resID == "" {
			lg.
				With("duration", time.Since(startedAt)).
				Debug("execute returned another result with 0 id")
			return &cacheResult{
				sharedCacheResult: &sharedCacheResult{
					cache:    c,
					typed:    existingRes.Unwrap(),
					resultID: "",
					refCount: 1, // TODO: ???
					inputs:   make(map[int]CacheResult),
					digests:  make(map[digest.Digest]struct{}),
				},
				resultDigest: existingRes.ResultDigest(),
			}, nil
		}

		sharedRes, ok := c.sharedResults[existingRes.ResultID()]
		if !ok {
			// TODO: ?
			return nil, fmt.Errorf(
				"execute returned another result with id %s, but no shared result found",
				resID,
			)
		}
		sharedRes.refCount++

		existingCacheRes := &cacheResult{
			sharedCacheResult: sharedRes,
			resultDigest:      existingRes.ResultDigest(),
		}

		lg.
			With("duration", time.Since(startedAt)).
			Debug("execute returned another result")
		return existingCacheRes, nil
	}

	if callCacheParams.Persist {
		resultID, err = c.insertCache(ctx, metaDigest, parent, argResults, typedVal, resultDigest)
		if err != nil {
			return nil, fmt.Errorf("failed to insert cache result: %w", err)
		}
	} else {
		resultID = newResultID()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	sharedRes, ok := c.sharedResults[resultID]
	if !ok {
		sharedRes = &sharedCacheResult{
			cache:     c,
			typed:     typedVal,
			resultID:  resultID,
			persisted: callCacheParams.Persist,
			inputs:    make(map[int]CacheResult),
			digests:   make(map[digest.Digest]struct{}),
		}
		c.sharedResults[resultID] = sharedRes
	}
	sharedRes.refCount++

	res := &cacheResult{
		sharedCacheResult: sharedRes,
		resultDigest:      resultDigest.String(),
	}
	c.resultsByDigest[resultDigest] = res

	lg.
		With("duration", time.Since(startedAt)).
		Debug("EXECUTE RETURNED RESULT")
	return res, nil
}

// TODO: enums are essentially modeled as a "pseudo"-call like selectNth(enum, nth) for simplicity at the moment, not the most performant
func (c *DagqlCache) SelectNth(
	ctx context.Context,
	s *Server,
	enumRes Result,
	nth int,
) (CacheResult, error) {
	startedAt := time.Now()

	// TODO: worth thinking through if this should use enumRes.ResultID() instead, or something else
	resultDigest := HashFrom(enumRes.ResultDigest(), strconv.Itoa(int(nth)))

	lg := slog.With(
		"nth", nth,
		"enum", fmt.Sprintf("%T", enumRes),
		"enumResID", enumRes.ResultID(),
		"enumResDigest", enumRes.ResultDigest(),
		"digest", resultDigest.String(),
	)

	c.mu.Lock()
	existingResult, ok := c.resultsByDigest[resultDigest]
	if ok {
		existingResult.refCount++
		c.mu.Unlock()
		lg.
			With("duration", time.Since(startedAt)).
			Debug("SELECTNTH INMEM CACHE HIT")
		return existingResult, nil
	}
	c.mu.Unlock()

	enum, ok := UnwrapAs[Enumerable](enumRes)
	if !ok {
		return nil, fmt.Errorf("enumToSelectable: not an enumerable: %T", enumRes)
	}
	nthVal, err := enum.Nth(int(nth))
	if err != nil {
		return nil, fmt.Errorf("nth %d: %w", nth, err)
	}
	if nthRes, ok := UnwrapAs[CacheResult](nthVal); ok {
		// TODO: INCREMENT REF COUNT ?????
		// TODO: INCREMENT REF COUNT ?????
		// TODO: INCREMENT REF COUNT ?????
		return nthRes, nil
	}

	// TODO: ...
	doNotCache := enumRes.ResultID() == ""
	if doNotCache {
		c.mu.Lock()
		defer c.mu.Unlock()

		sharedRes := &sharedCacheResult{
			cache:    c,
			typed:    nthVal,
			resultID: "",
		}
		res := &cacheResult{
			sharedCacheResult: sharedRes,
			resultDigest:      resultDigest.String(),
		}
		c.resultsByDigest[resultDigest] = res

		lg.
			With("duration", time.Since(startedAt)).
			Debug("DONOTCACHE SELECTNTH RETURNED RESULT")
		return res, nil
	}

	metaDigest := HashFrom("selectNth")
	nthInput, err := Int(nth).ToResult(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("scalar result: %w", err)
	}

	resultID, hitCache, err := c.checkCache(ctx, metaDigest, enumRes, []Result{nthInput})
	if err != nil {
		return nil, fmt.Errorf("failed to check cache: %w", err)
	}
	if hitCache {
		c.mu.Lock()
		defer c.mu.Unlock()

		sharedRes, ok := c.sharedResults[resultID]
		if !ok {
			sharedRes = &sharedCacheResult{
				cache:    c,
				typed:    nthVal,
				resultID: resultID,
				inputs:   make(map[int]CacheResult),
				digests:  make(map[digest.Digest]struct{}),
			}
			c.sharedResults[resultID] = sharedRes
		}
		sharedRes.refCount++

		res := &cacheResult{
			sharedCacheResult: sharedRes,
			resultDigest:      resultDigest.String(),
		}
		// TODO: overwrite always when it already exists?
		// TODO: overwrite always when it already exists?
		// TODO: overwrite always when it already exists?
		c.resultsByDigest[resultDigest] = res

		lg.
			With("duration", time.Since(startedAt)).
			Debug("SELECTNTH DB CACHE HIT")
		return res, nil
	}

	resultID, err = c.insertCache(ctx, metaDigest, enumRes, []Result{nthInput}, nthVal, resultDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to insert cache result: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	sharedRes, ok := c.sharedResults[resultID]
	if !ok {
		sharedRes = &sharedCacheResult{
			cache:    c,
			typed:    nthVal,
			resultID: resultID,
			inputs:   make(map[int]CacheResult),
			digests:  make(map[digest.Digest]struct{}),
		}
		c.sharedResults[resultID] = sharedRes
	}
	sharedRes.refCount++

	res := &cacheResult{
		sharedCacheResult: sharedRes,
		resultDigest:      resultDigest.String(),
	}
	// TODO: overwrite always when it already exists?
	// TODO: overwrite always when it already exists?
	// TODO: overwrite always when it already exists?
	c.resultsByDigest[resultDigest] = res

	lg.
		With("duration", time.Since(startedAt)).
		Debug("SELECTNTH RETURNED RESULT")
	return res, nil
}

func (c *DagqlCache) checkCache(
	ctx context.Context,
	metaDigest digest.Digest,
	parent Result,
	args []Result,
) (string, bool, error) {
	// TODO:
	// - prepared statements for each field (need to adjust branching NULL stuff below)
	// - check if variations on the query are more performant (i.e. lots of JOINs instead of lots of WHERE clauses)

	stmt := sq.Select("c.result_id").
		From("calls c").
		LeftJoin("resultDigests rd ON c.input_result = rd.result_id").
		Join("results r ON c.result_id = r.result_id").
		Where(sq.Eq{"c.meta_digest": metaDigest})

	inputConditions := sq.Or{}
	if parent != nil && parent.ResultID() != "" {
		inputConditions = append(inputConditions, sq.And{
			sq.Eq{"c.input_index": 0},
			sq.Expr("rd.digest IN (SELECT digest FROM resultDigests WHERE result_id = ?)", parent.ResultID()),
		})
	} else {
		inputConditions = append(inputConditions, sq.And{
			sq.Eq{"c.input_index": 0},
			sq.Expr("c.input_result IS NULL"),
		})
	}
	for i, arg := range args {
		i := i + 1
		if arg != nil && arg.ResultID() != "" {
			inputConditions = append(inputConditions, sq.And{
				sq.Eq{"c.input_index": i},
				sq.Expr("rd.digest IN (SELECT digest FROM resultDigests WHERE result_id = ?)", arg.ResultID()),
			})
		} else {
			inputConditions = append(inputConditions, sq.And{
				sq.Eq{"c.input_index": i},
				sq.Expr("c.input_result IS NULL"),
			})
		}
	}

	stmt = stmt.Where(inputConditions).
		GroupBy("c.result_id").
		Having(sq.Eq{"COUNT(DISTINCT c.input_index)": len(args) + 1})

	q, qArgs, err := stmt.ToSql()
	if err != nil {
		return "", false, fmt.Errorf("failed to build SQL statement: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, q, qArgs...)
	if err != nil {
		return "", false, fmt.Errorf("failed to query SQL statement: %w", err)
	}
	defer rows.Close()

	type resultRow struct {
		ResultID string
	}
	var results []resultRow
	for rows.Next() {
		var res resultRow
		if err := rows.Scan(
			&res.ResultID,
		); err != nil {
			return "", false, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, res)
	}
	if err := rows.Close(); err != nil {
		return "", false, fmt.Errorf("failed to close rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("error in rows: %w", err)
	}

	if len(results) == 0 {
		return "", false, nil
	}

	// TODO: choose "best" result?
	result := results[0]

	return result.ResultID, true, nil
}

func (c *DagqlCache) insertCache(
	ctx context.Context,
	metaDigest digest.Digest,
	parent Result,
	args []Result,
	res Typed,
	resultDigest digest.Digest,
) (string, error) {
	resID := "" // TODO: uggo
	var resDigest string
	if result, ok := res.(Result); ok {
		resID = result.ResultID()
		resDigest = result.ResultDigest()
	}

	if resID == "" { // TODO: uggo
		resID = newResultID()

		// jsonBlob, err := res.Typed().ToJSON(res.Value())
		jsonBlob, err := json.Marshal(res)
		if err != nil {
			return "", fmt.Errorf("failed to convert result to JSON: %w", err)
		}

		_, err = sq.Insert("results").
			Columns("result_id", "json").
			Values(resID, jsonBlob).
			RunWith(c.db).
			ExecContext(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to build insert result statement: %w", err)
		}
	}

	var parentResultID any
	if parent != nil && parent.ResultID() != "" {
		parentResultID = parent.ResultID()
	}
	insertCallQ, insertCallArgs, err := sq.Insert("calls").
		Columns("meta_digest", "input_index", "input_result", "result_id").
		Values(metaDigest.String(), 0, parentResultID, resID).
		ToSql()
	if err != nil {
		return "", fmt.Errorf("failed to insert call parent index: %w", err)
	}
	if _, err := c.db.ExecContext(ctx, insertCallQ, insertCallArgs...); err != nil {
		// TODO: don't include full query in error string
		return "", fmt.Errorf("failed to insert call parent index: %w, %s, %+v", err, insertCallQ, insertCallArgs)
	}
	for i, arg := range args {
		i := i + 1
		var argResultID any
		if arg != nil && arg.ResultID() != "" {
			argResultID = arg.ResultID()
		}
		_, err := sq.Insert("calls").
			Columns("meta_digest", "input_index", "input_result", "result_id").
			Values(metaDigest.String(), i, argResultID, resID).
			RunWith(c.db).
			ExecContext(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to insert call arg index %d: %w", i, err)
		}
	}

	// TODO: insert ALL digests, including any extras specified during the call

	insertDigstQ, insertDigestArgs, err := sq.Insert("resultDigests").
		Values(resID, resultDigest.String()).
		Suffix("ON CONFLICT DO NOTHING").
		ToSql()
	if err != nil {
		return "", fmt.Errorf("failed to build insert digest statement: %w", err)
	}
	if _, err := c.db.ExecContext(ctx, insertDigstQ, insertDigestArgs...); err != nil {
		return "", fmt.Errorf("failed to insert digest: %w", err)
	}

	if resDigest != resultDigest.String() && resDigest != "" {
		insertDigstQ, insertDigestArgs, err := sq.Insert("resultDigests").
			Values(resID, resDigest).
			Suffix("ON CONFLICT DO NOTHING").
			ToSql()
		if err != nil {
			return "", fmt.Errorf("failed to build insert digest statement: %w", err)
		}
		if _, err := c.db.ExecContext(ctx, insertDigstQ, insertDigestArgs...); err != nil {
			return "", fmt.Errorf("failed to insert digest: %w", err)
		}
	}

	return resID, nil
}

func (s *Server) ScalarResult(
	ctx context.Context,
	input Input,
) (string, digest.Digest, error) {
	// TODO: fix s.cache.cache naming
	return s.cache.cache.ScalarResult(ctx, input)
}

func (c *DagqlCache) ScalarResult(
	ctx context.Context,
	input Input,
) (string, digest.Digest, error) {
	blob, err := json.Marshal(input)
	if err != nil {
		return "", "", fmt.Errorf("failed to convert input to JSON: %w", err)
	}

	dgst := HashFrom(string(blob))

	// TODO: dedupe by digest

	var resultID string
	err = sq.Select("result_id").
		From("resultDigests").
		Where(sq.Eq{"digest": dgst.String()}).
		Limit(1).
		RunWith(c.db).
		QueryRowContext(ctx).
		Scan(&resultID)
	switch {
	case err == nil:
		return resultID, dgst, nil

	case errors.Is(err, sql.ErrNoRows):
		resultID = newResultID()
		_, err := sq.Insert("results").
			Columns("result_id", "json").
			Values(resultID, blob).
			RunWith(c.db).
			ExecContext(ctx)
		if err != nil {
			return "", "", fmt.Errorf("failed to build insert result statement: %w", err)
		}
		insertDigstQ, insertDigestArgs, err := sq.Insert("resultDigests").
			Values(resultID, dgst.String()).
			Suffix("ON CONFLICT DO NOTHING").
			ToSql()
		if err != nil {
			return "", "", fmt.Errorf("failed to build insert digest statement: %w", err)
		}
		if _, err := c.db.ExecContext(ctx, insertDigstQ, insertDigestArgs...); err != nil {
			return "", "", fmt.Errorf("failed to insert digest: %w", err)
		}
		return resultID, dgst, nil

	default:
		return "", "", fmt.Errorf("failed to query result digest: %w", err)
	}
}

type sqlInterceptor struct {
	sqlmw.NullInterceptor
}

func (in *sqlInterceptor) ConnExecContext(ctx context.Context, conn driver.ExecerContext, query string, args []driver.NamedValue) (driver.Result, error) {
	startedAt := time.Now()
	result, err := conn.ExecContext(ctx, query, args)
	slog.Debug("executed sql exec",
		"duration", time.Since(startedAt),
		"query", query,
		// "args", args,
		"err", err,
	)
	return result, err
}

func (in *sqlInterceptor) ConnQueryContext(ctx context.Context, conn driver.QueryerContext, query string, args []driver.NamedValue) (context.Context, driver.Rows, error) {
	startedAt := time.Now()
	rows, err := conn.QueryContext(ctx, query, args)
	slog.Debug("executed sql query",
		"duration", time.Since(startedAt),
		"query", query,
		// "args", args,
		"err", err,
	)
	return ctx, rows, err
}

func (in *sqlInterceptor) StmtQueryContext(ctx context.Context, conn driver.StmtQueryContext, query string, args []driver.NamedValue) (context.Context, driver.Rows, error) {
	startedAt := time.Now()
	rows, err := conn.QueryContext(ctx, args)
	slog.Debug("executed sql query",
		"duration", time.Since(startedAt),
		"query", query,
		// "args", args,
		"err", err,
	)
	return ctx, rows, err
}

func (in *sqlInterceptor) StmtExecContext(ctx context.Context, conn driver.StmtExecContext, query string, args []driver.NamedValue) (driver.Result, error) {
	startedAt := time.Now()
	result, err := conn.ExecContext(ctx, args)
	slog.Debug("executed sql exec",
		"duration", time.Since(startedAt),
		"query", query,
		// "args", args,
		"err", err,
	)
	return result, err
}
