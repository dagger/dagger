package dagql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
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
	cache *DagqlCache

	typed Typed

	resultID int

	refCount int
}

type cacheResult struct {
	*sharedCacheResult
	resultDigest string
}

var _ Result = &cacheResult{}

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

func (cr *sharedCacheResult) ResultID() int {
	return cr.resultID
}

func (cr *sharedCacheResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(cr.typed)
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

	// result id in db -> shared cache result
	sharedResults map[int]*sharedCacheResult

	// TODO: ...
	resultsByDigest map[digest.Digest]*cacheResult

	db *sql.DB
}

func NewDagqlCache(db *sql.DB) *DagqlCache {
	return &DagqlCache{
		sharedResults:   make(map[int]*sharedCacheResult),
		resultsByDigest: make(map[digest.Digest]*cacheResult),
		db:              db,
	}
}

func (c *DagqlCache) LoadID(
	ctx context.Context,
	s *Server,
	parent Object,
	fieldSpec *FieldSpec,
	cacheSpec CacheSpec,
	callID *call.ID,
) (_ Result, _ *call.ID, rerr error) {
	view := View(callID.View())
	idArgs := callID.Args()
	inputArgs := make(map[string]Input, len(idArgs))
	for _, argSpec := range fieldSpec.Args.Inputs(view) {
		// just be n^2 since the overhead of a map is likely more expensive
		// for the expected low value of n
		var inputLit call.Literal
		for _, idArg := range idArgs {
			if idArg.Name() == argSpec.Name {
				inputLit = idArg.Value()
				break
			}
		}

		switch {
		case inputLit != nil:
			input, err := argSpec.Type.Decoder().DecodeInput(inputLit.ToInput())
			if err != nil {
				return nil, nil, fmt.Errorf("Call: init arg %q value as %T (%s) using %T: %w", argSpec.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
			}
			inputArgs[argSpec.Name] = input

		case argSpec.Default != nil:
			inputArgs[argSpec.Name] = argSpec.Default

		case argSpec.Type.Type().NonNull:
			// error out if the arg is missing but required
			return nil, nil, fmt.Errorf("missing required argument: %q", argSpec.Name)

		default:
			// explicitly include as null
			inputArgs[argSpec.Name] = nil
		}
	}

	doNotCache := cacheSpec.DoNotCache != ""
	res, err := s.cache.Call(ctx, s, parent, callID, inputArgs, doNotCache)
	if err != nil {
		return nil, nil, err
	}
	return res, callID, nil
}

func (c *DagqlCache) Select(
	ctx context.Context,
	s *Server,
	parent Object,
	constructor *call.ID, // TODO: method on Object?
	fieldSpec *FieldSpec, // TODO: method on Object?
	cacheSpec CacheSpec, // TODO: method on Object?
	sel Selector,
) (_ Result, _ *call.ID, rerr error) {
	view := sel.View
	if fieldSpec.ViewFilter == nil {
		// fields in the global view shouldn't attach the current view to the
		// selector (since they're global from all perspectives)
		view = ""
	}

	idArgs := make([]*call.Argument, 0, len(sel.Args))
	inputArgs := make(map[string]Input, len(sel.Args))
	for _, argSpec := range fieldSpec.Args.Inputs(view) {
		// just be n^2 since the overhead of a map is likely more expensive
		// for the expected low value of n
		var namedInput NamedInput
		for _, selArg := range sel.Args {
			if selArg.Name == argSpec.Name {
				namedInput = selArg
				break
			}
		}

		switch {
		case namedInput.Value != nil:
			idArgs = append(idArgs, call.NewArgument(
				namedInput.Name,
				namedInput.Value.ToLiteral(),
				argSpec.Sensitive,
			))
			inputArgs[argSpec.Name] = namedInput.Value

		case argSpec.Default != nil:
			inputArgs[argSpec.Name] = argSpec.Default

		case argSpec.Type.Type().NonNull:
			// error out if the arg is missing but required
			return nil, nil, fmt.Errorf("missing required argument: %q", argSpec.Name)

		default:
			// explicitly include as null
			inputArgs[argSpec.Name] = nil
		}
	}
	// TODO: it's better DX if it matches schema order
	sort.Slice(idArgs, func(i, j int) bool {
		return idArgs[i].Name() < idArgs[j].Name()
	})

	astType := fieldSpec.Type.Type()
	if sel.Nth != 0 {
		astType = astType.Elem
	}

	newID := constructor.Append(
		astType,
		sel.Field,
		string(view),
		fieldSpec.Module,
		sel.Nth,
		"",
		idArgs...,
	)

	doNotCache := cacheSpec.DoNotCache != ""
	if cacheSpec.GetCacheConfig != nil {
		origDgst := newID.Digest()

		cacheCfgCtx := idToContext(ctx, newID)
		cacheCfgCtx = srvToContext(cacheCfgCtx, s)
		cacheCfg, err := cacheSpec.GetCacheConfig(cacheCfgCtx, parent, inputArgs, view, CacheConfig{
			Digest: origDgst,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to compute cache key for %s.%s: %w", parent.ObjectType().TypeName(), sel.Field, err)
		}

		if len(cacheCfg.UpdatedArgs) > 0 {
			maps.Copy(inputArgs, cacheCfg.UpdatedArgs)
			for argName, argInput := range cacheCfg.UpdatedArgs {
				var found bool
				for i, idArg := range idArgs {
					if idArg.Name() == argName {
						idArgs[i] = idArg.WithValue(argInput.ToLiteral())
						found = true
						break
					}
				}
				if !found {
					idArgs = append(idArgs, call.NewArgument(
						argName,
						argInput.ToLiteral(),
						false,
					))
				}
			}
			newID = constructor.Append(
				astType,
				sel.Field,
				string(view),
				fieldSpec.Module,
				sel.Nth,
				"",
				idArgs...,
			)
		}

		if cacheCfg.Digest != origDgst {
			newID = newID.WithDigest(cacheCfg.Digest)
		}
	}

	res, err := s.cache.Call(ctx, s, parent, newID, inputArgs, doNotCache)
	if err != nil {
		return nil, nil, err
	}
	return res, newID, nil
}

func (c *DagqlCache) Call(
	ctx context.Context,
	s *Server,
	parent Object,
	newID *call.ID,
	inputArgs map[string]Input,
	doNotCache bool,
) (_ Result, rerr error) {
	ctx = idToContext(ctx, newID)

	res, err := c.call(ctx, s, parent, newID.Field(), View(newID.View()), inputArgs, newID.Digest(), doNotCache)
	if err != nil {
		return nil, fmt.Errorf("failed to call field %q: %w", newID.Field(), err)
	}
	return res, nil
}

func (c *DagqlCache) call(
	ctx context.Context,
	s *Server,
	parent Object,
	fieldName string,
	view View,
	inputArgs map[string]Input,
	resultDigest digest.Digest,
	doNotCache bool,
) (_ Result, rerr error) {
	startedAt := time.Now()
	lg := slog.With(
		"field", fmt.Sprintf("%s.%s", parent.ObjectType().TypeName(), fieldName),
		"doNotCache", doNotCache,
		"digest", resultDigest.String(),
	)

	c.mu.Lock()
	existingResult, ok := c.resultsByDigest[resultDigest]
	if ok {
		existingResult.refCount++
		c.mu.Unlock()
		lg.
			With("duration", time.Since(startedAt)).
			Debug("INMEM CACHE HIT")
		return existingResult, nil
	}
	c.mu.Unlock()

	if doNotCache {
		// TODO: technically still caching atm, just trying to avoid huge overhead of introspection queries atm
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

		c.mu.Lock()
		defer c.mu.Unlock()

		sharedRes := &sharedCacheResult{
			cache:    c,
			typed:    typedVal,
			resultID: 0,
		}
		res := &cacheResult{
			sharedCacheResult: sharedRes,
			resultDigest:      resultDigest.String(),
		}
		c.resultsByDigest[resultDigest] = res

		lg.
			With("duration", time.Since(startedAt)).
			Debug("DONOTCACHE EXECUTE RETURNED RESULT")
		return res, nil
	}

	field, ok := parent.ObjectType().FieldSpec(fieldName, view)
	if !ok {
		return nil, fmt.Errorf("Call: %s has no such field: %q", parent.ObjectType().TypeName(), fieldName)
	}

	// TODO: needs to handle modules (where type names may overlap)
	metaDigestInputs := []string{
		parent.ObjectType().TypeName(),
		fieldName,
	}
	metaDigest := HashFrom(metaDigestInputs...)

	args := make([]NamedInput, 0, len(inputArgs))
	for name, arg := range inputArgs {
		args = append(args, NamedInput{Name: name, Value: arg})
	}
	slices.SortFunc(args, func(a, b NamedInput) int {
		return strings.Compare(a.Name, b.Name)
	})

	argResults := make([]Result, 0, len(args))
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

	// TODO: don't load resultBlob unless needed (i.e. when not in memory already)
	resultID, resultBlob, hitCache, err := c.checkCache(ctx, metaDigest, parent, argResults, resultDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to check cache: %w", err)
	}
	if hitCache {
		// TODO:
		/*
		 */
		fmt.Printf("cached result for %s.%s (%T(%+v), id:%d): %s (%d)\n",
			parent.ObjectType().TypeName(), fieldName,
			parent, "", parent.ResultID(), // r.Self, r.Self, parentResult.ResultID(),
			"", // string(resultBlob),
			resultID,
		)

		c.mu.Lock()
		defer c.mu.Unlock()

		sharedRes, ok := c.sharedResults[resultID]
		if !ok {
			typedVal, err := field.Type.FromJSON(ctx, resultBlob)
			if err != nil {
				return nil, fmt.Errorf("failed to decode cached result for %s.%s (%T): %w",
					parent.ObjectType().TypeName(), fieldName,
					field.Type,
					err,
				)
			}
			if n, ok := typedVal.(Derefable); ok {
				typedVal, ok = n.Deref()
				if !ok {
					return nil, nil
				}
			}

			sharedRes = &sharedCacheResult{
				cache:    c,
				typed:    typedVal,
				resultID: resultID,
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
			Debug("DB CACHE HIT")
		return res, nil
	}

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

	// did the call return another result from its own call it made internally?
	existingRes, ok := typedVal.(Result)
	if ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		// TODO: ???
		// c.resultsByDigest[resultDigest] = existingRes.(*cacheResult)

		// TODO: INCREMENT REF COUNT ?????
		// TODO: INCREMENT REF COUNT ?????
		// TODO: INCREMENT REF COUNT ?????
		lg.
			With("duration", time.Since(startedAt)).
			Debug("EXECUTE RETURNED ANOTHER RESULT")
		return existingRes, nil
	}

	resultID, err = c.insertCache(ctx, metaDigest, parent, argResults, typedVal, resultDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to insert cache result: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	sharedRes, ok := c.sharedResults[resultID]
	if !ok {
		sharedRes = &sharedCacheResult{
			cache:    c,
			typed:    typedVal,
			resultID: resultID,
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
		Debug("EXECUTE RETURNED RESULT")
	return res, nil
}

// TODO: enums are essentially modeled as a "pseudo"-call like selectNth(enum, nth) for simplicity at the moment, not the most performant
func (c *DagqlCache) SelectNth(
	ctx context.Context,
	s *Server,
	enumRes Result,
	nth int,
) (Result, error) {
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
	if nthRes, ok := UnwrapAs[Result](nthVal); ok {
		// TODO: INCREMENT REF COUNT ?????
		// TODO: INCREMENT REF COUNT ?????
		// TODO: INCREMENT REF COUNT ?????
		return nthRes, nil
	}

	// TODO: ...
	doNotCache := enumRes.ResultID() == 0
	if doNotCache {
		c.mu.Lock()
		defer c.mu.Unlock()

		sharedRes := &sharedCacheResult{
			cache:    c,
			typed:    nthVal,
			resultID: 0,
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

	resultID, _, hitCache, err := c.checkCache(ctx, metaDigest, enumRes, []Result{nthInput}, resultDigest)
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
	resultDigest digest.Digest,
) (int, []byte, bool, error) {
	// TODO:
	// - fast-path check for resultDigest incorporated below
	// - prepared statements for each field (need to adjust branching NULL stuff below)
	// - check if variations on the query are more performant (i.e. lots of JOINs instead of lots of WHERE clauses)

	stmt := sq.Select("c.result_id", "r.json").
		From("calls c").
		LeftJoin("resultDigests rd ON c.input_result = rd.result_id").
		Join("results r ON c.result_id = r.result_id").
		Where(sq.Eq{"c.meta_digest": metaDigest})

	inputConditions := sq.Or{}
	if parent != nil && parent.ResultID() != 0 {
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
		if arg != nil && arg.ResultID() != 0 {
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
		return 0, nil, false, fmt.Errorf("failed to build SQL statement: %w", err)
	}

	rows, err := c.db.QueryContext(ctx, q, qArgs...)
	if err != nil {
		return 0, nil, false, fmt.Errorf("failed to query SQL statement: %w", err)
	}
	defer rows.Close()

	type resultRow struct {
		ResultID int
		Json     []byte
	}
	var results []resultRow
	for rows.Next() {
		var res resultRow
		if err := rows.Scan(
			&res.ResultID,
			&res.Json,
		); err != nil {
			return 0, nil, false, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, res)
	}
	if err := rows.Close(); err != nil {
		return 0, nil, false, fmt.Errorf("failed to close rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, false, fmt.Errorf("error in rows: %w", err)
	}

	if len(results) == 0 {
		return 0, nil, false, nil
	}

	// TODO: choose "best" result
	result := results[0]

	insertDigstQ, insertDigestArgs, err := sq.Insert("resultDigests").
		Values(result.ResultID, resultDigest.String()).
		Suffix("ON CONFLICT DO NOTHING").
		ToSql()
	if err != nil {
		return 0, nil, false, fmt.Errorf("failed to build insert digest statement: %w", err)
	}
	if _, err := c.db.ExecContext(ctx, insertDigstQ, insertDigestArgs...); err != nil {
		return 0, nil, false, fmt.Errorf("failed to insert digest: %w", err)
	}

	return result.ResultID, result.Json, true, nil
}

func (c *DagqlCache) insertCache(
	ctx context.Context,
	metaDigest digest.Digest,
	parent Result,
	args []Result,
	res Typed,
	resultDigest digest.Digest,
) (int, error) {
	resID := 0 // TODO: uggo
	var resDigest string
	if result, ok := res.(Result); ok {
		resID = result.ResultID()
		resDigest = result.ResultDigest()
	}

	if resID == 0 { // TODO: uggo
		// jsonBlob, err := res.Typed().ToJSON(res.Value())
		jsonBlob, err := json.Marshal(res)
		if err != nil {
			return 0, fmt.Errorf("failed to convert result to JSON: %w", err)
		}

		insertResultQ, insertResultArgs, err := sq.Insert("results").
			Columns("json").
			Values(jsonBlob).
			Suffix("RETURNING result_id").
			ToSql()
		if err != nil {
			return 0, fmt.Errorf("failed to build insert result statement: %w", err)
		}
		if err := c.db.QueryRowContext(ctx, insertResultQ, insertResultArgs...).Scan(&resID); err != nil {
			// TODO: whole query in error strnig probs nono
			return 0, fmt.Errorf("failed to insert result: %w: %s", err, insertResultQ)
		}
	}

	var parentResultID any
	if parent != nil && parent.ResultID() != 0 {
		parentResultID = parent.ResultID()
	}
	insertCallQ, insertCallArgs, err := sq.Insert("calls").
		Columns("meta_digest", "input_index", "input_result", "result_id").
		Values(metaDigest.String(), 0, parentResultID, resID).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to insert call parent index: %w", err)
	}
	if _, err := c.db.ExecContext(ctx, insertCallQ, insertCallArgs...); err != nil {
		// TODO: don't include full query in error string
		return 0, fmt.Errorf("failed to insert call parent index: %w, %s, %+v", err, insertCallQ, insertCallArgs)
	}
	for i, arg := range args {
		i := i + 1
		var argResultID any
		if arg != nil && arg.ResultID() != 0 {
			argResultID = arg.ResultID()
		}
		_, err := sq.Insert("calls").
			Columns("meta_digest", "input_index", "input_result", "result_id").
			Values(metaDigest.String(), i, argResultID, resID).
			RunWith(c.db).
			ExecContext(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to insert call arg index %d: %w", i, err)
		}
	}

	// TODO: insert ALL digests, including any extras specified during the call

	insertDigstQ, insertDigestArgs, err := sq.Insert("resultDigests").
		Values(resID, resultDigest.String()).
		Suffix("ON CONFLICT DO NOTHING").
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("failed to build insert digest statement: %w", err)
	}
	if _, err := c.db.ExecContext(ctx, insertDigstQ, insertDigestArgs...); err != nil {
		return 0, fmt.Errorf("failed to insert digest: %w", err)
	}

	if resDigest != resultDigest.String() && resDigest != "" {
		insertDigstQ, insertDigestArgs, err := sq.Insert("resultDigests").
			Values(resID, resDigest).
			Suffix("ON CONFLICT DO NOTHING").
			ToSql()
		if err != nil {
			return 0, fmt.Errorf("failed to build insert digest statement: %w", err)
		}
		if _, err := c.db.ExecContext(ctx, insertDigstQ, insertDigestArgs...); err != nil {
			return 0, fmt.Errorf("failed to insert digest: %w", err)
		}
	}

	return resID, nil
}

func (s *Server) ScalarResult(
	ctx context.Context,
	input Input,
) (int, digest.Digest, error) {
	return s.cache.ScalarResult(ctx, input)
}

func (c *DagqlCache) ScalarResult(
	ctx context.Context,
	input Input,
) (int, digest.Digest, error) {
	blob, err := json.Marshal(input)
	if err != nil {
		return 0, "", fmt.Errorf("failed to convert input to JSON: %w", err)
	}

	dgst := HashFrom(string(blob))

	// TODO: dedupe by digest

	var resultID int
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
		insertResultQ, insertResultArgs, err := sq.Insert("results").
			Columns("json").
			Values(blob).
			Suffix("RETURNING result_id").
			ToSql()
		if err != nil {
			return 0, "", fmt.Errorf("failed to build insert result statement: %w", err)
		}
		if err := c.db.QueryRowContext(ctx, insertResultQ, insertResultArgs...).Scan(&resultID); err != nil {
			return 0, "", fmt.Errorf("failed to insert result: %w", err)
		}
		insertDigstQ, insertDigestArgs, err := sq.Insert("resultDigests").
			Values(resultID, dgst.String()).
			Suffix("ON CONFLICT DO NOTHING").
			ToSql()
		if err != nil {
			return 0, "", fmt.Errorf("failed to build insert digest statement: %w", err)
		}
		if _, err := c.db.ExecContext(ctx, insertDigstQ, insertDigestArgs...); err != nil {
			return 0, "", fmt.Errorf("failed to insert digest: %w", err)
		}
		return resultID, dgst, nil

	default:
		return 0, "", fmt.Errorf("failed to query result digest: %w", err)
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
