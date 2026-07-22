package bootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"

	"dagger.io/dagger/engineconn"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/querybuilder"
)

type ID string

type Request struct {
	Query     string         `json:"query"`
	OpName    string         `json:"operationName,omitempty"`
	Variables map[string]any `json:"variables,omitempty"`
}

type Response struct {
	Data       any
	Errors     any
	Extensions map[string]any
}

type errorWrappedClient struct {
	graphql.Client
}

func (c errorWrappedClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	err := c.Client.MakeRequest(ctx, req, resp)
	if err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		return resp.Errors
	}
	return nil
}

type Client struct {
	query  *querybuilder.Selection
	client graphql.Client
	conn   engineconn.EngineConn
}

func Connect(ctx context.Context) (*Client, error) {
	cfg := &engineconn.Config{}
	conn, err := engineconn.Get(ctx, cfg)
	if err != nil {
		return nil, err
	}
	gql := errorWrappedClient{graphql.NewClient("http://"+conn.Host()+"/query", conn)}
	c := &Client{
		query:  querybuilder.Query().Client(gql),
		client: gql,
		conn:   conn,
	}
	return c, nil
}

func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Client) Do(ctx context.Context, req *Request, resp *Response) error {
	r := graphql.Response{}
	if resp != nil {
		r.Data = resp.Data
	}
	err := c.client.MakeRequest(ctx, &graphql.Request{
		Query:     req.Query,
		Variables: req.Variables,
		OpName:    req.OpName,
	}, &r)
	if resp != nil {
		resp.Errors = r.Errors
		resp.Extensions = r.Extensions
	}
	return err
}

// Module types and builder methods for codegen
type Module struct {
	query *querybuilder.Selection
}

func (c *Client) Module() *Module {
	return &Module{
		query: c.query.Select("module"),
	}
}

func (r *Module) WithDescription(description string) *Module {
	return &Module{
		query: r.query.Select("withDescription").Arg("description", description),
	}
}

func (r *Module) WithObject(object *TypeDef) *Module {
	return &Module{
		query: r.query.Select("withObject").Arg("object", object),
	}
}

func (r *Module) WithInterface(iface *TypeDef) *Module {
	return &Module{
		query: r.query.Select("withInterface").Arg("interface", iface),
	}
}

func (r *Module) WithEnum(enum *TypeDef) *Module {
	return &Module{
		query: r.query.Select("withEnum").Arg("enum", enum),
	}
}

func (r *Module) ID(ctx context.Context) (ID, error) {
	var response ID
	q := r.query.Select("id")
	err := q.Bind(&response).Execute(ctx)
	return response, err
}

type TypeDefKind string

const (
	TypeDefKindStringKind    TypeDefKind = "STRING_KIND"
	TypeDefKindIntegerKind   TypeDefKind = "INTEGER_KIND"
	TypeDefKindBooleanKind   TypeDefKind = "BOOLEAN_KIND"
	TypeDefKindFloatKind     TypeDefKind = "FLOAT_KIND"
	TypeDefKindVoidKind      TypeDefKind = "VOID_KIND"
	TypeDefKindListKind      TypeDefKind = "LIST_KIND"
	TypeDefKindObjectKind    TypeDefKind = "OBJECT_KIND"
	TypeDefKindInterfaceKind TypeDefKind = "INTERFACE_KIND"
	TypeDefKindEnumKind      TypeDefKind = "ENUM_KIND"
	TypeDefKindScalarKind    TypeDefKind = "SCALAR_KIND"
)

type TypeDef struct {
	query *querybuilder.Selection
}

func (c *Client) TypeDef() *TypeDef {
	return &TypeDef{
		query: c.query.Select("typeDef"),
	}
}

func (r *TypeDef) WithKind(kind TypeDefKind) *TypeDef {
	return &TypeDef{
		query: r.query.Select("withKind").Arg("kind", kind),
	}
}

func (r *TypeDef) WithOptional(optional bool) *TypeDef {
	return &TypeDef{
		query: r.query.Select("withOptional").Arg("optional", optional),
	}
}

func (r *TypeDef) WithListOf(elementType *TypeDef) *TypeDef {
	return &TypeDef{
		query: r.query.Select("withListOf").Arg("elementType", elementType),
	}
}

func (r *TypeDef) WithScalar(name string) *TypeDef {
	return &TypeDef{
		query: r.query.Select("withScalar").Arg("name", name),
	}
}

type TypeDefWithObjectOpts struct {
	Description string
	SourceMap   *SourceMap
	Deprecated  string
}

func (r *TypeDef) WithObject(name string, opts ...TypeDefWithObjectOpts) *TypeDef {
	q := r.query.Select("withObject").Arg("name", name)
	for i := range opts {
		if opts[i].Description != "" {
			q = q.Arg("description", opts[i].Description)
		}
		if opts[i].SourceMap != nil {
			q = q.Arg("sourceMap", opts[i].SourceMap)
		}
		if opts[i].Deprecated != "" {
			q = q.Arg("deprecated", opts[i].Deprecated)
		}
	}
	return &TypeDef{query: q}
}

type TypeDefWithInterfaceOpts struct {
	Description string
	SourceMap   *SourceMap
	Deprecated  string
}

func (r *TypeDef) WithInterface(name string, opts ...TypeDefWithInterfaceOpts) *TypeDef {
	q := r.query.Select("withInterface").Arg("name", name)
	for i := range opts {
		if opts[i].Description != "" {
			q = q.Arg("description", opts[i].Description)
		}
		if opts[i].SourceMap != nil {
			q = q.Arg("sourceMap", opts[i].SourceMap)
		}
		if opts[i].Deprecated != "" {
			q = q.Arg("deprecated", opts[i].Deprecated)
		}
	}
	return &TypeDef{query: q}
}

type TypeDefWithEnumOpts struct {
	Description string
	SourceMap   *SourceMap
	Deprecated  string
}

func (r *TypeDef) WithEnum(name string, opts ...TypeDefWithEnumOpts) *TypeDef {
	q := r.query.Select("withEnum").Arg("name", name)
	for i := range opts {
		if opts[i].Description != "" {
			q = q.Arg("description", opts[i].Description)
		}
		if opts[i].SourceMap != nil {
			q = q.Arg("sourceMap", opts[i].SourceMap)
		}
		if opts[i].Deprecated != "" {
			q = q.Arg("deprecated", opts[i].Deprecated)
		}
	}
	return &TypeDef{query: q}
}

type TypeDefWithEnumMemberOpts struct {
	Value       string
	Description string
	SourceMap   *SourceMap
	Deprecated  string
}

func (r *TypeDef) WithEnumMember(value string, opts ...TypeDefWithEnumMemberOpts) *TypeDef {
	q := r.query.Select("withEnumMember").Arg("value", value)
	for i := range opts {
		if opts[i].Value != "" {
			q = q.Arg("value", opts[i].Value)
		}
		if opts[i].Description != "" {
			q = q.Arg("description", opts[i].Description)
		}
		if opts[i].SourceMap != nil {
			q = q.Arg("sourceMap", opts[i].SourceMap)
		}
		if opts[i].Deprecated != "" {
			q = q.Arg("deprecated", opts[i].Deprecated)
		}
	}
	return &TypeDef{query: q}
}

type TypeDefWithEnumValueOpts struct {
	Description string
	SourceMap   *SourceMap
	Deprecated  string
}

func (r *TypeDef) WithEnumValue(value string, opts ...TypeDefWithEnumValueOpts) *TypeDef {
	q := r.query.Select("withEnumValue").Arg("value", value)
	for i := range opts {
		if opts[i].Description != "" {
			q = q.Arg("description", opts[i].Description)
		}
		if opts[i].SourceMap != nil {
			q = q.Arg("sourceMap", opts[i].SourceMap)
		}
		if opts[i].Deprecated != "" {
			q = q.Arg("deprecated", opts[i].Deprecated)
		}
	}
	return &TypeDef{query: q}
}

type TypeDefWithFieldOpts struct {
	Description string
	SourceMap   *SourceMap
	Deprecated  string
}

func (r *TypeDef) WithField(name string, typeDef *TypeDef, opts ...TypeDefWithFieldOpts) *TypeDef {
	q := r.query.Select("withField").Arg("name", name).Arg("typeDef", typeDef)
	for i := range opts {
		if opts[i].Description != "" {
			q = q.Arg("description", opts[i].Description)
		}
		if opts[i].SourceMap != nil {
			q = q.Arg("sourceMap", opts[i].SourceMap)
		}
		if opts[i].Deprecated != "" {
			q = q.Arg("deprecated", opts[i].Deprecated)
		}
	}
	return &TypeDef{query: q}
}

func (r *TypeDef) WithFunction(fn *Function) *TypeDef {
	return &TypeDef{
		query: r.query.Select("withFunction").Arg("function", fn),
	}
}

func (r *TypeDef) WithConstructor(fn *Function) *TypeDef {
	return &TypeDef{
		query: r.query.Select("withConstructor").Arg("function", fn),
	}
}

func (r *TypeDef) ID(ctx context.Context) (ID, error) {
	var response ID
	q := r.query.Select("id")
	err := q.Bind(&response).Execute(ctx)
	return response, err
}

func (r *TypeDef) XXX_GraphQLType() string { //nolint:staticcheck
	return "TypeDef"
}

func (r *TypeDef) XXX_GraphQLIDType() string { //nolint:staticcheck
	return "ID"
}

func (r *TypeDef) XXX_GraphQLID(ctx context.Context) (string, error) { //nolint:staticcheck
	id, err := r.ID(ctx)
	return string(id), err
}

func (r *TypeDef) MarshalJSON() ([]byte, error) {
	id, err := r.ID(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(id)
}

type JSON string

type FunctionCachePolicy string

const (
	FunctionCachePolicyNever      FunctionCachePolicy = "NEVER"
	FunctionCachePolicyPerSession FunctionCachePolicy = "SESSION"
	FunctionCachePolicyDefault    FunctionCachePolicy = "DEFAULT"
)

type FunctionWithCachePolicyOpts struct {
	TimeToLive string
}

type FunctionWithDeprecatedOpts struct {
	Reason string
}

type Function struct {
	query *querybuilder.Selection
}

func (c *Client) Function(name string, returnType *TypeDef) *Function {
	return &Function{
		query: c.query.Select("function").Arg("name", name).Arg("returnType", returnType),
	}
}

func (r *Function) WithDescription(description string) *Function {
	return &Function{
		query: r.query.Select("withDescription").Arg("description", description),
	}
}

func (r *Function) WithCachePolicy(policy FunctionCachePolicy, opts ...FunctionWithCachePolicyOpts) *Function {
	q := r.query.Select("withCachePolicy").Arg("policy", policy)
	for i := range opts {
		if opts[i].TimeToLive != "" {
			q = q.Arg("timeToLive", opts[i].TimeToLive)
		}
	}
	return &Function{query: q}
}

func (r *Function) WithSourceMap(sourceMap *SourceMap) *Function {
	return &Function{
		query: r.query.Select("withSourceMap").Arg("sourceMap", sourceMap),
	}
}

func (r *Function) WithDeprecated(opts FunctionWithDeprecatedOpts) *Function {
	q := r.query.Select("withDeprecated")
	if opts.Reason != "" {
		q = q.Arg("reason", opts.Reason)
	}
	return &Function{query: q}
}

func (r *Function) WithCheck() *Function {
	return &Function{
		query: r.query.Select("withCheck"),
	}
}

func (r *Function) WithGenerator() *Function {
	return &Function{
		query: r.query.Select("withGenerator"),
	}
}

func (r *Function) WithUp() *Function {
	return &Function{
		query: r.query.Select("withUp"),
	}
}

type FunctionWithArgOpts struct {
	Description    string
	DefaultValue   JSON
	DefaultPath    string
	DefaultAddress string
	SourceMap      *SourceMap
	Deprecated     string
	Ignore         []string
}

func (r *Function) WithArg(name string, typeDef *TypeDef, opts ...FunctionWithArgOpts) *Function {
	q := r.query.Select("withArg").Arg("name", name).Arg("typeDef", typeDef)
	for i := range opts {
		if opts[i].Description != "" {
			q = q.Arg("description", opts[i].Description)
		}
		if opts[i].DefaultValue != "" {
			q = q.Arg("defaultValue", opts[i].DefaultValue)
		}
		if opts[i].DefaultPath != "" {
			q = q.Arg("defaultPath", opts[i].DefaultPath)
		}
		if opts[i].DefaultAddress != "" {
			q = q.Arg("defaultAddress", opts[i].DefaultAddress)
		}
		if opts[i].SourceMap != nil {
			q = q.Arg("sourceMap", opts[i].SourceMap)
		}
		if opts[i].Deprecated != "" {
			q = q.Arg("deprecated", opts[i].Deprecated)
		}
		if len(opts[i].Ignore) > 0 {
			q = q.Arg("ignore", opts[i].Ignore)
		}
	}
	return &Function{query: q}
}

func (r *Function) ID(ctx context.Context) (ID, error) {
	var response ID
	q := r.query.Select("id")
	err := q.Bind(&response).Execute(ctx)
	return response, err
}

func (r *Function) XXX_GraphQLType() string { //nolint:staticcheck
	return "Function"
}

func (r *Function) XXX_GraphQLIDType() string { //nolint:staticcheck
	return "ID"
}

func (r *Function) XXX_GraphQLID(ctx context.Context) (string, error) { //nolint:staticcheck
	id, err := r.ID(ctx)
	return string(id), err
}

func (r *Function) MarshalJSON() ([]byte, error) {
	id, err := r.ID(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(id)
}

type SourceMap struct {
	query *querybuilder.Selection
}

func (c *Client) SourceMap(filename string, line int, column int) *SourceMap {
	filename = filepath.ToSlash(filename)
	return &SourceMap{
		query: c.query.Select("sourceMap").Arg("filename", filename).Arg("line", line).Arg("column", column),
	}
}

func (r *SourceMap) ID(ctx context.Context) (ID, error) {
	var response ID
	q := r.query.Select("id")
	err := q.Bind(&response).Execute(ctx)
	return response, err
}

func (r *SourceMap) XXX_GraphQLType() string { //nolint:staticcheck
	return "SourceMap"
}

func (r *SourceMap) XXX_GraphQLIDType() string { //nolint:staticcheck
	return "ID"
}

func (r *SourceMap) XXX_GraphQLID(ctx context.Context) (string, error) { //nolint:staticcheck
	id, err := r.ID(ctx)
	return string(id), err
}

func (r *SourceMap) MarshalJSON() ([]byte, error) {
	id, err := r.ID(context.Background())
	if err != nil {
		return nil, err
	}
	return json.Marshal(id)
}

// Ensure GraphQLMarshaller interface is implemented
var (
	_ querybuilder.GraphQLMarshaller = (*TypeDef)(nil)
	_ querybuilder.GraphQLMarshaller = (*Function)(nil)
	_ querybuilder.GraphQLMarshaller = (*SourceMap)(nil)
)

type SchemaTypeDef struct {
	query *querybuilder.Selection
}

func (c *Client) Schema(json JSON) *SchemaTypeDef {
	return &SchemaTypeDef{
		query: c.query.Select("schema").Arg("json", json),
	}
}

func (r *SchemaTypeDef) Merge(moduleTypes JSON, moduleName string) *SchemaTypeDef {
	return &SchemaTypeDef{
		query: r.query.Select("merge").Arg("moduleTypes", moduleTypes).Arg("moduleName", moduleName),
	}
}

func (r *SchemaTypeDef) Contents(ctx context.Context) (string, error) {
	var response string
	q := r.query.Select("contents")
	err := q.Bind(&response).Execute(ctx)
	return response, err
}
