# Bulk Secret Fetch For User Defaults

## Problem

1. **User defaults resolve independently** - Each object default is resolved through its own `address(...).secret.id` path.
2. **Secret IDs fetch plaintext eagerly** - `Query.secret` fetches plaintext when no `cacheKey` is supplied so it can derive a stable handle.
3. **Providers are single-secret only** - Dagger's internal secret RPC and provider abstraction only expose `GetSecret`.
4. **1Password already has bulk support** - The Go SDK exposes `Secrets().ResolveAll`, but Dagger cannot use it from the current plumbing.

## Solution

Add an internal bulk secret-fetch building block, then use it for user-default `Secret` and `[]Secret` arguments during dynamic input injection. Keep `Secret.Plaintext` unchanged.

## Current Path

User defaults are loaded as `.env` strings:

```go
// core/modulesource.go
func (src *ModuleSource) LoadUserDefaults(ctx context.Context) (rerr error) {
	innerEnvFile, _, err := src.innerEnvFile(ctx)
	if err != nil {
		return err
	}
	outerEnvFile, _, err := src.outerEnvFile(ctx)
	if err != nil {
		return err
	}

	outerForName, err := outerEnvFile.Namespace(ctx, src.ModuleName)
	if err != nil {
		return err
	}
	outerForOriginalName, err := outerEnvFile.Namespace(ctx, src.ModuleOriginalName)
	if err != nil {
		return err
	}

	src.UserDefaults = NewEnvFile(true).WithEnvFiles(innerEnvFile, outerForName, outerForOriginalName)
	return nil
}
```

Each default is looked up by argument name:

```go
// core/modfunc.go
defaults, err := fn.UserDefaults(mainCtx)
if err != nil {
	return nil, false, fmt.Errorf("lookup defaults for function %q: %w", fn.metadata.Name, err)
}

userInput, ok, err := defaults.LookupCaseInsensitive(mainCtx, arg.Self().Name)
if err != nil {
	return nil, false, err
}
if !ok {
	return nil, false, nil
}

return fn.newUserDefault(arg.Self(), userInput), true, nil
```

Object defaults are resolved independently during dynamic input injection:

```go
// core/modfunc.go
for i, userDefault := range userDefaults {
	eg.Go(func() error {
		input, err := userDefault.DagqlID(ctx)
		if err != nil {
			return err
		}
		arg := userDefault.Arg
		userDefaultVals[i] = &userDefaultArgInput{
			argName: arg.Name,
			val:     input,
		}
		return nil
	})
}
```

For `Secret`, that path becomes `address(...).secret`:

```go
// core/modfunc.go
resolveOne := func(userInput, typename string) (any, error) {
	var result dagql.AnyObjectResult
	if err := srv.Select(mainCtx, srv.Root(), &result,
		dagql.Selector{
			Field: "address",
			Args: []dagql.NamedInput{{
				Name:  "value",
				Value: dagql.NewString(userInput),
			}},
		},
		dagql.Selector{
			Field: strings.ToLower(typename),
		},
	); err != nil {
		return nil, ud.errorf(err, "resolve object (%q)", typename)
	}

	id, err := result.Select(mainCtx, srv, dagql.Selector{Field: "id"})
	if err != nil {
		return nil, ud.errorf(err, "get object ID")
	}
	return id.Unwrap(), nil
}
```

`Query.secret` fetches plaintext to derive the session handle:

```go
// core/schema/secret.go
concreteVal := &core.Secret{
	URIVal:         args.URI,
	SourceClientID: clientMetadata.ClientID,
}

if args.CacheKey.Valid {
	handle = core.SecretHandleFromCacheKey(string(args.CacheKey.Value))
} else {
	plaintext, err := concreteVal.Plaintext(ctx)
	if err != nil {
		...
	}
	handle = core.SecretHandleFromPlaintext(parent.Self().SecretSalt(), plaintext)
}
```

That calls the single-secret session RPC:

```go
// core/secret.go
resp, err := secrets.NewSecretsClient(conn).GetSecret(ctx, &secrets.GetSecretRequest{
	ID: secret.URIVal,
})
if err != nil {
	return nil, err
}
return resp.Data, nil
```

## Internal API

Add a bulk RPC beside the existing single-secret RPC:

```proto
service Secrets {
	rpc GetSecret(GetSecretRequest) returns (GetSecretResponse);
	rpc GetSecrets(GetSecretsRequest) returns (GetSecretsResponse);
}

message GetSecretRequest {
	string ID = 1;
	map<string, string> annotations = 2;
}

message GetSecretResponse {
	bytes data = 1;
}

message GetSecretsRequest {
	repeated string IDs = 1;
	map<string, string> annotations = 2;
}

message GetSecretsResponse {
	map<string, GetSecretResponse> secrets = 1;
}
```

Keep `GetSecret` as a compatibility wrapper:

```go
func (sp SecretProvider) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	resp, err := sp.GetSecrets(ctx, &secrets.GetSecretsRequest{
		IDs: []string{req.ID},
	})
	if err != nil {
		return nil, err
	}
	return resp.Secrets[req.ID], nil
}
```

Upgrade the provider abstraction:

```go
type SecretResolver interface {
	Resolve(context.Context, string) ([]byte, error)
	ResolveMany(context.Context, []string) (map[string][]byte, error)
}
```

Use an adapter for existing providers:

```go
type SecretResolverFunc func(context.Context, string) ([]byte, error)

func (r SecretResolverFunc) Resolve(ctx context.Context, id string) ([]byte, error) {
	return r(ctx, id)
}

func (r SecretResolverFunc) ResolveMany(ctx context.Context, ids []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(ids))
	for _, id := range ids {
		data, err := r(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		out[id] = data
	}
	return out, nil
}
```

Group bulk requests by provider scheme:

```go
func (sp SecretProvider) GetSecrets(ctx context.Context, req *secrets.GetSecretsRequest) (*secrets.GetSecretsResponse, error) {
	byScheme := map[string][]string{}
	pathsByID := map[string]string{}

	for _, id := range req.IDs {
		scheme, path, err := ResolverPartsForID(id)
		if err != nil {
			return nil, err
		}
		byScheme[scheme] = append(byScheme[scheme], id)
		pathsByID[id] = path
	}

	resp := &secrets.GetSecretsResponse{
		Secrets: map[string]*secrets.GetSecretResponse{},
	}

	for scheme, ids := range byScheme {
		resolver := resolvers[scheme]

		paths := make([]string, 0, len(ids))
		for _, id := range ids {
			paths = append(paths, pathsByID[id])
		}

		values, err := resolver.ResolveMany(ctx, paths)
		if err != nil {
			return nil, err
		}

		for _, id := range ids {
			resp.Secrets[id] = &secrets.GetSecretResponse{
				Data: values[pathsByID[id]],
			}
		}
	}

	return resp, nil
}
```

## 1Password Provider

Use `ResolveAll` when `OP_SERVICE_ACCOUNT_TOKEN` is set:

```go
func opSDKProviderMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	client, err := onepassword.NewClient(
		ctx,
		onepassword.WithServiceAccountToken(os.Getenv("OP_SERVICE_ACCOUNT_TOKEN")),
		onepassword.WithIntegrationInfo("dagger", engine.BaseVersion(engine.Version)),
	)
	if err != nil {
		return nil, err
	}

	refs := make([]string, 0, len(keys))
	for _, key := range keys {
		refs = append(refs, "op://"+key)
	}

	resp, err := client.Secrets().ResolveAll(ctx, refs)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]byte, len(keys))
	for i, ref := range refs {
		item := resp.IndividualResponses[ref]
		if item.Error != nil {
			return nil, fmt.Errorf("unable to lookup %q: %+v", ref, item.Error)
		}
		if item.Content == nil {
			return nil, fmt.Errorf("unable to lookup %q: empty response", ref)
		}
		out[keys[i]] = []byte(item.Content.Secret)
	}

	return out, nil
}
```

The CLI fallback can keep using the sequential adapter initially.

## User Defaults Integration

Split user defaults into secret defaults and everything else:

```go
secretDefaults, otherDefaults := partitionSecretUserDefaults(userDefaults)
```

Resolve secret defaults in one bulk path:

```go
secretInputs, err := fn.resolveSecretUserDefaultsBulk(ctx, secretDefaults)
if err != nil {
	return err
}

for _, input := range secretInputs {
	userDefaultVals[input.index] = &userDefaultArgInput{
		argName: input.argName,
		val:     input.val,
	}
}
```

Keep all non-secret object defaults on the existing path:

```go
for _, userDefault := range otherDefaults {
	input, err := userDefault.DagqlID(ctx)
	if err != nil {
		return err
	}
	...
}
```

Support single and list secret defaults:

```go
func New(
	password *dagger.Secret,
	secrets []*dagger.Secret,
) *Module
```

Flatten list defaults before fetching:

```dotenv
PASSWORD=op://prod/db/password
SECRETS=op://prod/api/token,op://prod/webhook/secret
```

Bulk fetch:

```go
GetSecrets([]string{
	"op://prod/db/password",
	"op://prod/api/token",
	"op://prod/webhook/secret",
})
```

## Core Helper

Factor session-bound secret creation out of `core/schema/secret.go` so `Query.secret` and the bulk user-default path produce equivalent results:

```go
type SecretSeed struct {
	URI        string
	Plaintext  []byte
	CacheKey   string
	ResultCall *dagql.ResultCall
}

func NewSessionSecrets(
	ctx context.Context,
	query *Query,
	seeds []SecretSeed,
) ([]dagql.ObjectResult[*Secret], error)
```

Internally:

```go
handle := SecretHandleFromPlaintext(query.SecretSalt(), seed.Plaintext)

concreteVal := &Secret{
	URIVal:         seed.URI,
	SourceClientID: clientMetadata.ClientID,
}

if err := cache.BindSessionResource(ctx, sessionID, clientID, handle, concreteVal); err != nil {
	return nil, err
}

attached, err := cache.AttachResult(ctx, sessionID, srv, handleRes)
if err != nil {
	return nil, err
}
```

## Implementation Steps

1. Add `GetSecrets` to `internal/buildkit/session/secrets/secrets.proto`.
2. Regenerate `internal/buildkit/session/secrets/secrets.pb.go`.
3. Add `SecretResolver.ResolveMany` with sequential fallback.
4. Implement `SecretProvider.GetSecrets`, grouped by URI scheme.
5. Update `SecretProviderProxy` to proxy `GetSecrets`.
6. Implement 1Password SDK bulk resolution via `ResolveAll`.
7. Add a core helper for creating session-bound secrets from pre-fetched plaintext.
8. Update `Query.secret` to reuse the helper for the single-secret path.
9. Update `ModuleFunction.DynamicInputsForCall` to bulk-resolve user-default `Secret` and `[]Secret` args.
10. Leave `Secret.Plaintext` unchanged.

## Tests

Add integration coverage for single, list, and mixed single-plus-list secret defaults.

Single defaults:

```go
func New(
	a *dagger.Secret,
	b *dagger.Secret,
	c *dagger.Secret,
) *Test
```

```dotenv
A=op://vault/item/a
B=op://vault/item/b
C=op://vault/item/c
```

List defaults:

```go
func New(secrets []*dagger.Secret) *Test
```

```dotenv
SECRETS=op://vault/item/a,op://vault/item/b,op://vault/item/c
```

Expected behavior:

```go
// Dynamic input resolution creates all Secret IDs successfully.
// A provider that supports bulk receives one bulk request for the matching scheme.
```

The current integration suite covers this through `TestConstructorOptional`,
`TestConstructorOptionalEmptySecret`, and `TestSimple` table cases for `SECRETS`
and mixed `PASSWORD` plus `SECRETS` defaults.

## Non-Goals

1. Do not batch `Secret.Plaintext`.
2. Do not add public GraphQL or SDK APIs.
3. Do not require every provider to implement true bulk immediately.
4. Do not change user-facing `.env` syntax.

## Status

Implemented. `Secret.Plaintext` still uses the existing single-secret lookup path;
bulk lookup is used only while materializing secret user defaults into session-bound
secret IDs.
