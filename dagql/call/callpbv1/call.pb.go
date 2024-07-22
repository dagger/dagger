// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        v3.21.12
// source: call.proto

package callpbv1

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// DAG represents a GraphQL value of a certain type, constructed by evaluating
// its contained DAG of Calls. In other words, it represents a
// constructor-addressed value, which may be an object, an array, or a scalar
// value.
//
// It may be binary=>base64-encoded to be used as a GraphQL ID value for
// objects. Alternatively it may be stored in a database and referred to via an
// RFC-6920 ni://sha-256;... URI.
type DAG struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The digest of the Call representing the "root" of the DAG. All other Calls
	// in this message are referenced through the root Call's receiver, args or
	// module.
	RootDigest string `protobuf:"bytes,1,opt,name=rootDigest,proto3" json:"rootDigest,omitempty"`
	// Map of Call digests to the Calls they represent. This structure
	// allows us to deduplicate occurrences of the same Call in the DAG.
	CallsByDigest map[string]*Call `protobuf:"bytes,2,rep,name=callsByDigest,proto3" json:"callsByDigest,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
}

func (x *DAG) Reset() {
	*x = DAG{}
	if protoimpl.UnsafeEnabled {
		mi := &file_call_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *DAG) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DAG) ProtoMessage() {}

func (x *DAG) ProtoReflect() protoreflect.Message {
	mi := &file_call_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DAG.ProtoReflect.Descriptor instead.
func (*DAG) Descriptor() ([]byte, []int) {
	return file_call_proto_rawDescGZIP(), []int{0}
}

func (x *DAG) GetRootDigest() string {
	if x != nil {
		return x.RootDigest
	}
	return ""
}

func (x *DAG) GetCallsByDigest() map[string]*Call {
	if x != nil {
		return x.CallsByDigest
	}
	return nil
}

// Call represents a function call, including all inputs necessary to call it
// again, a hint as to whether you can expect the same result, and the GraphQL
// return type for runtime type checking.
//
// In GraphQL terms, Call corresponds to a field selection against an object.
type Call struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The receiving object for the Call's field selection. If specified, this is
	// the digest of the Call that returns the receiver. If not specified, Query
	// is implied.
	ReceiverDigest string `protobuf:"bytes,1,opt,name=receiverDigest,proto3" json:"receiverDigest,omitempty"`
	// The GraphQL type of the call's return value.
	Type *Type `protobuf:"bytes,2,opt,name=type,proto3" json:"type,omitempty"`
	// The GraphQL field name to select.
	Field string `protobuf:"bytes,3,opt,name=field,proto3" json:"field,omitempty"`
	// The arguments to pass to the GraphQL field selection. The order matters;
	// if it changes, the digest changes. For optimal readability hese should
	// ideally be in the order defined in the schema.
	Args []*Argument `protobuf:"bytes,4,rep,name=args,proto3" json:"args,omitempty"`
	// If true, this Call is not reproducible; repeated evaluations may return
	// different values.
	Tainted bool `protobuf:"varint,5,opt,name=tainted,proto3" json:"tainted,omitempty"`
	// If true, this Call may be omitted from the DAG without changing
	// the final result.
	//
	// This may be used to prevent meta-queries from busting cache keys when
	// desired, if Calls are used as a cache key for persistence.
	//
	// It is worth noting that we don't store meta information at this level and
	// continue to force metadata to be set via GraphQL queries. It makes Calls
	// always easy to evaluate.
	Meta bool `protobuf:"varint,6,opt,name=meta,proto3" json:"meta,omitempty"`
	// If the field selection returns a list, this is the index of the element to
	// return from the Call. This value is 1-indexed, hence being call nth (1st,
	// not 0th). At the same time that this is set, the Call's Type must also be
	// changed to its Elem. If the type does not have an Elem.
	//
	// Here we're teetering dangerously close to full blown attribute path
	// selection, but we're intentionally limiting ourselves instead to cover
	// only the common case of returning a list of objects. The only case not
	// handled is a nested list. Don't do that; return a list of typed values
	// instead.
	Nth int64 `protobuf:"varint,7,opt,name=nth,proto3" json:"nth,omitempty"`
	// The module that provides the implementation of the field.
	//
	// The actual usage of this is opaque to the protocol. In Dagger this is
	// the module providing the implementation of the field.
	Module *Module `protobuf:"bytes,8,opt,name=module,proto3" json:"module,omitempty"`
	// A unique digest of this Call. Note that this must not be set when
	// calculating the Call's digest.
	Digest string `protobuf:"bytes,9,opt,name=digest,proto3" json:"digest,omitempty"`
	// The view that this call was made in. Since a graphql server may present
	// slightly different views depending on the specified view, to be able to
	// evaluate calls wherever, we need to track which view the call was actually
	// called in.
	View string `protobuf:"bytes,10,opt,name=view,proto3" json:"view,omitempty"`
}

func (x *Call) Reset() {
	*x = Call{}
	if protoimpl.UnsafeEnabled {
		mi := &file_call_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Call) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Call) ProtoMessage() {}

func (x *Call) ProtoReflect() protoreflect.Message {
	mi := &file_call_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Call.ProtoReflect.Descriptor instead.
func (*Call) Descriptor() ([]byte, []int) {
	return file_call_proto_rawDescGZIP(), []int{1}
}

func (x *Call) GetReceiverDigest() string {
	if x != nil {
		return x.ReceiverDigest
	}
	return ""
}

func (x *Call) GetType() *Type {
	if x != nil {
		return x.Type
	}
	return nil
}

func (x *Call) GetField() string {
	if x != nil {
		return x.Field
	}
	return ""
}

func (x *Call) GetArgs() []*Argument {
	if x != nil {
		return x.Args
	}
	return nil
}

func (x *Call) GetTainted() bool {
	if x != nil {
		return x.Tainted
	}
	return false
}

func (x *Call) GetMeta() bool {
	if x != nil {
		return x.Meta
	}
	return false
}

func (x *Call) GetNth() int64 {
	if x != nil {
		return x.Nth
	}
	return 0
}

func (x *Call) GetModule() *Module {
	if x != nil {
		return x.Module
	}
	return nil
}

func (x *Call) GetDigest() string {
	if x != nil {
		return x.Digest
	}
	return ""
}

func (x *Call) GetView() string {
	if x != nil {
		return x.View
	}
	return ""
}

// Module represents a self-contained logical module that can be dynamically
// loaded to evaluate an Call that uses it. The details of this task are not
// defined at the protocol layer.
type Module struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The digest of the Call that provides the module.
	CallDigest string `protobuf:"bytes,1,opt,name=callDigest,proto3" json:"callDigest,omitempty"`
	// The name of the module.
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// A human-readable ref which may be interpreted by an external system to
	// yield the same module.
	Ref string `protobuf:"bytes,3,opt,name=ref,proto3" json:"ref,omitempty"`
}

func (x *Module) Reset() {
	*x = Module{}
	if protoimpl.UnsafeEnabled {
		mi := &file_call_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Module) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Module) ProtoMessage() {}

func (x *Module) ProtoReflect() protoreflect.Message {
	mi := &file_call_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Module.ProtoReflect.Descriptor instead.
func (*Module) Descriptor() ([]byte, []int) {
	return file_call_proto_rawDescGZIP(), []int{2}
}

func (x *Module) GetCallDigest() string {
	if x != nil {
		return x.CallDigest
	}
	return ""
}

func (x *Module) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Module) GetRef() string {
	if x != nil {
		return x.Ref
	}
	return ""
}

// A named value passed to a GraphQL field or contained in an input object.
type Argument struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name  string   `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Value *Literal `protobuf:"bytes,2,opt,name=value,proto3" json:"value,omitempty"`
}

func (x *Argument) Reset() {
	*x = Argument{}
	if protoimpl.UnsafeEnabled {
		mi := &file_call_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Argument) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Argument) ProtoMessage() {}

func (x *Argument) ProtoReflect() protoreflect.Message {
	mi := &file_call_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Argument.ProtoReflect.Descriptor instead.
func (*Argument) Descriptor() ([]byte, []int) {
	return file_call_proto_rawDescGZIP(), []int{3}
}

func (x *Argument) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Argument) GetValue() *Literal {
	if x != nil {
		return x.Value
	}
	return nil
}

// A value passed to an argument or contained in a list.
type Literal struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Types that are assignable to Value:
	//
	//	*Literal_CallDigest
	//	*Literal_Null
	//	*Literal_Bool
	//	*Literal_Enum
	//	*Literal_Int
	//	*Literal_Float
	//	*Literal_String_
	//	*Literal_List
	//	*Literal_Object
	Value isLiteral_Value `protobuf_oneof:"value"`
}

func (x *Literal) Reset() {
	*x = Literal{}
	if protoimpl.UnsafeEnabled {
		mi := &file_call_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Literal) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Literal) ProtoMessage() {}

func (x *Literal) ProtoReflect() protoreflect.Message {
	mi := &file_call_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Literal.ProtoReflect.Descriptor instead.
func (*Literal) Descriptor() ([]byte, []int) {
	return file_call_proto_rawDescGZIP(), []int{4}
}

func (m *Literal) GetValue() isLiteral_Value {
	if m != nil {
		return m.Value
	}
	return nil
}

func (x *Literal) GetCallDigest() string {
	if x, ok := x.GetValue().(*Literal_CallDigest); ok {
		return x.CallDigest
	}
	return ""
}

func (x *Literal) GetNull() bool {
	if x, ok := x.GetValue().(*Literal_Null); ok {
		return x.Null
	}
	return false
}

func (x *Literal) GetBool() bool {
	if x, ok := x.GetValue().(*Literal_Bool); ok {
		return x.Bool
	}
	return false
}

func (x *Literal) GetEnum() string {
	if x, ok := x.GetValue().(*Literal_Enum); ok {
		return x.Enum
	}
	return ""
}

func (x *Literal) GetInt() int64 {
	if x, ok := x.GetValue().(*Literal_Int); ok {
		return x.Int
	}
	return 0
}

func (x *Literal) GetFloat() float64 {
	if x, ok := x.GetValue().(*Literal_Float); ok {
		return x.Float
	}
	return 0
}

func (x *Literal) GetString_() string {
	if x, ok := x.GetValue().(*Literal_String_); ok {
		return x.String_
	}
	return ""
}

func (x *Literal) GetList() *List {
	if x, ok := x.GetValue().(*Literal_List); ok {
		return x.List
	}
	return nil
}

func (x *Literal) GetObject() *Object {
	if x, ok := x.GetValue().(*Literal_Object); ok {
		return x.Object
	}
	return nil
}

type isLiteral_Value interface {
	isLiteral_Value()
}

type Literal_CallDigest struct {
	CallDigest string `protobuf:"bytes,1,opt,name=callDigest,proto3,oneof"`
}

type Literal_Null struct {
	Null bool `protobuf:"varint,2,opt,name=null,proto3,oneof"`
}

type Literal_Bool struct {
	Bool bool `protobuf:"varint,3,opt,name=bool,proto3,oneof"`
}

type Literal_Enum struct {
	Enum string `protobuf:"bytes,4,opt,name=enum,proto3,oneof"`
}

type Literal_Int struct {
	Int int64 `protobuf:"varint,5,opt,name=int,proto3,oneof"`
}

type Literal_Float struct {
	Float float64 `protobuf:"fixed64,6,opt,name=float,proto3,oneof"`
}

type Literal_String_ struct {
	String_ string `protobuf:"bytes,7,opt,name=string,proto3,oneof"`
}

type Literal_List struct {
	List *List `protobuf:"bytes,8,opt,name=list,proto3,oneof"`
}

type Literal_Object struct {
	Object *Object `protobuf:"bytes,9,opt,name=object,proto3,oneof"`
}

func (*Literal_CallDigest) isLiteral_Value() {}

func (*Literal_Null) isLiteral_Value() {}

func (*Literal_Bool) isLiteral_Value() {}

func (*Literal_Enum) isLiteral_Value() {}

func (*Literal_Int) isLiteral_Value() {}

func (*Literal_Float) isLiteral_Value() {}

func (*Literal_String_) isLiteral_Value() {}

func (*Literal_List) isLiteral_Value() {}

func (*Literal_Object) isLiteral_Value() {}

// A list of values.
type List struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Values []*Literal `protobuf:"bytes,1,rep,name=values,proto3" json:"values,omitempty"`
}

func (x *List) Reset() {
	*x = List{}
	if protoimpl.UnsafeEnabled {
		mi := &file_call_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *List) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*List) ProtoMessage() {}

func (x *List) ProtoReflect() protoreflect.Message {
	mi := &file_call_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use List.ProtoReflect.Descriptor instead.
func (*List) Descriptor() ([]byte, []int) {
	return file_call_proto_rawDescGZIP(), []int{5}
}

func (x *List) GetValues() []*Literal {
	if x != nil {
		return x.Values
	}
	return nil
}

// A series of named values.
type Object struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Values []*Argument `protobuf:"bytes,1,rep,name=values,proto3" json:"values,omitempty"`
}

func (x *Object) Reset() {
	*x = Object{}
	if protoimpl.UnsafeEnabled {
		mi := &file_call_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Object) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Object) ProtoMessage() {}

func (x *Object) ProtoReflect() protoreflect.Message {
	mi := &file_call_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Object.ProtoReflect.Descriptor instead.
func (*Object) Descriptor() ([]byte, []int) {
	return file_call_proto_rawDescGZIP(), []int{6}
}

func (x *Object) GetValues() []*Argument {
	if x != nil {
		return x.Values
	}
	return nil
}

// A GraphQL type reference.
type Type struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	NamedType string `protobuf:"bytes,1,opt,name=namedType,proto3" json:"namedType,omitempty"`
	Elem      *Type  `protobuf:"bytes,2,opt,name=elem,proto3" json:"elem,omitempty"`
	NonNull   bool   `protobuf:"varint,3,opt,name=nonNull,proto3" json:"nonNull,omitempty"`
}

func (x *Type) Reset() {
	*x = Type{}
	if protoimpl.UnsafeEnabled {
		mi := &file_call_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Type) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Type) ProtoMessage() {}

func (x *Type) ProtoReflect() protoreflect.Message {
	mi := &file_call_proto_msgTypes[7]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Type.ProtoReflect.Descriptor instead.
func (*Type) Descriptor() ([]byte, []int) {
	return file_call_proto_rawDescGZIP(), []int{7}
}

func (x *Type) GetNamedType() string {
	if x != nil {
		return x.NamedType
	}
	return ""
}

func (x *Type) GetElem() *Type {
	if x != nil {
		return x.Elem
	}
	return nil
}

func (x *Type) GetNonNull() bool {
	if x != nil {
		return x.NonNull
	}
	return false
}

var File_call_proto protoreflect.FileDescriptor

var file_call_proto_rawDesc = []byte{
	0x0a, 0x0a, 0x63, 0x61, 0x6c, 0x6c, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x06, 0x64, 0x61,
	0x67, 0x67, 0x65, 0x72, 0x22, 0xbb, 0x01, 0x0a, 0x03, 0x44, 0x41, 0x47, 0x12, 0x1e, 0x0a, 0x0a,
	0x72, 0x6f, 0x6f, 0x74, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x0a, 0x72, 0x6f, 0x6f, 0x74, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x12, 0x44, 0x0a, 0x0d,
	0x63, 0x61, 0x6c, 0x6c, 0x73, 0x42, 0x79, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x18, 0x02, 0x20,
	0x03, 0x28, 0x0b, 0x32, 0x1e, 0x2e, 0x64, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x44, 0x41, 0x47,
	0x2e, 0x43, 0x61, 0x6c, 0x6c, 0x73, 0x42, 0x79, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x45, 0x6e,
	0x74, 0x72, 0x79, 0x52, 0x0d, 0x63, 0x61, 0x6c, 0x6c, 0x73, 0x42, 0x79, 0x44, 0x69, 0x67, 0x65,
	0x73, 0x74, 0x1a, 0x4e, 0x0a, 0x12, 0x43, 0x61, 0x6c, 0x6c, 0x73, 0x42, 0x79, 0x44, 0x69, 0x67,
	0x65, 0x73, 0x74, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x22, 0x0a, 0x05, 0x76, 0x61,
	0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0c, 0x2e, 0x64, 0x61, 0x67, 0x67,
	0x65, 0x72, 0x2e, 0x43, 0x61, 0x6c, 0x6c, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02,
	0x38, 0x01, 0x22, 0xa0, 0x02, 0x0a, 0x04, 0x43, 0x61, 0x6c, 0x6c, 0x12, 0x26, 0x0a, 0x0e, 0x72,
	0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x72, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x0e, 0x72, 0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x72, 0x44, 0x69, 0x67,
	0x65, 0x73, 0x74, 0x12, 0x20, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x0c, 0x2e, 0x64, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x54, 0x79, 0x70, 0x65, 0x52,
	0x04, 0x74, 0x79, 0x70, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x18, 0x03,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x12, 0x24, 0x0a, 0x04, 0x61,
	0x72, 0x67, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x64, 0x61, 0x67, 0x67,
	0x65, 0x72, 0x2e, 0x41, 0x72, 0x67, 0x75, 0x6d, 0x65, 0x6e, 0x74, 0x52, 0x04, 0x61, 0x72, 0x67,
	0x73, 0x12, 0x18, 0x0a, 0x07, 0x74, 0x61, 0x69, 0x6e, 0x74, 0x65, 0x64, 0x18, 0x05, 0x20, 0x01,
	0x28, 0x08, 0x52, 0x07, 0x74, 0x61, 0x69, 0x6e, 0x74, 0x65, 0x64, 0x12, 0x12, 0x0a, 0x04, 0x6d,
	0x65, 0x74, 0x61, 0x18, 0x06, 0x20, 0x01, 0x28, 0x08, 0x52, 0x04, 0x6d, 0x65, 0x74, 0x61, 0x12,
	0x10, 0x0a, 0x03, 0x6e, 0x74, 0x68, 0x18, 0x07, 0x20, 0x01, 0x28, 0x03, 0x52, 0x03, 0x6e, 0x74,
	0x68, 0x12, 0x26, 0x0a, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x18, 0x08, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x0e, 0x2e, 0x64, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x4d, 0x6f, 0x64, 0x75, 0x6c,
	0x65, 0x52, 0x06, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x64, 0x69, 0x67,
	0x65, 0x73, 0x74, 0x18, 0x09, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x64, 0x69, 0x67, 0x65, 0x73,
	0x74, 0x12, 0x12, 0x0a, 0x04, 0x76, 0x69, 0x65, 0x77, 0x18, 0x0a, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x04, 0x76, 0x69, 0x65, 0x77, 0x22, 0x4e, 0x0a, 0x06, 0x4d, 0x6f, 0x64, 0x75, 0x6c, 0x65, 0x12,
	0x1e, 0x0a, 0x0a, 0x63, 0x61, 0x6c, 0x6c, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x0a, 0x63, 0x61, 0x6c, 0x6c, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x12,
	0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x12, 0x10, 0x0a, 0x03, 0x72, 0x65, 0x66, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x03, 0x72, 0x65, 0x66, 0x22, 0x45, 0x0a, 0x08, 0x41, 0x72, 0x67, 0x75, 0x6d, 0x65, 0x6e,
	0x74, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x25, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x0b, 0x32, 0x0f, 0x2e, 0x64, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x4c, 0x69,
	0x74, 0x65, 0x72, 0x61, 0x6c, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x22, 0x8a, 0x02, 0x0a,
	0x07, 0x4c, 0x69, 0x74, 0x65, 0x72, 0x61, 0x6c, 0x12, 0x20, 0x0a, 0x0a, 0x63, 0x61, 0x6c, 0x6c,
	0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x48, 0x00, 0x52, 0x0a,
	0x63, 0x61, 0x6c, 0x6c, 0x44, 0x69, 0x67, 0x65, 0x73, 0x74, 0x12, 0x14, 0x0a, 0x04, 0x6e, 0x75,
	0x6c, 0x6c, 0x18, 0x02, 0x20, 0x01, 0x28, 0x08, 0x48, 0x00, 0x52, 0x04, 0x6e, 0x75, 0x6c, 0x6c,
	0x12, 0x14, 0x0a, 0x04, 0x62, 0x6f, 0x6f, 0x6c, 0x18, 0x03, 0x20, 0x01, 0x28, 0x08, 0x48, 0x00,
	0x52, 0x04, 0x62, 0x6f, 0x6f, 0x6c, 0x12, 0x14, 0x0a, 0x04, 0x65, 0x6e, 0x75, 0x6d, 0x18, 0x04,
	0x20, 0x01, 0x28, 0x09, 0x48, 0x00, 0x52, 0x04, 0x65, 0x6e, 0x75, 0x6d, 0x12, 0x12, 0x0a, 0x03,
	0x69, 0x6e, 0x74, 0x18, 0x05, 0x20, 0x01, 0x28, 0x03, 0x48, 0x00, 0x52, 0x03, 0x69, 0x6e, 0x74,
	0x12, 0x16, 0x0a, 0x05, 0x66, 0x6c, 0x6f, 0x61, 0x74, 0x18, 0x06, 0x20, 0x01, 0x28, 0x01, 0x48,
	0x00, 0x52, 0x05, 0x66, 0x6c, 0x6f, 0x61, 0x74, 0x12, 0x18, 0x0a, 0x06, 0x73, 0x74, 0x72, 0x69,
	0x6e, 0x67, 0x18, 0x07, 0x20, 0x01, 0x28, 0x09, 0x48, 0x00, 0x52, 0x06, 0x73, 0x74, 0x72, 0x69,
	0x6e, 0x67, 0x12, 0x22, 0x0a, 0x04, 0x6c, 0x69, 0x73, 0x74, 0x18, 0x08, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x0c, 0x2e, 0x64, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x4c, 0x69, 0x73, 0x74, 0x48, 0x00,
	0x52, 0x04, 0x6c, 0x69, 0x73, 0x74, 0x12, 0x28, 0x0a, 0x06, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74,
	0x18, 0x09, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0e, 0x2e, 0x64, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e,
	0x4f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x48, 0x00, 0x52, 0x06, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74,
	0x42, 0x07, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x22, 0x2f, 0x0a, 0x04, 0x4c, 0x69, 0x73,
	0x74, 0x12, 0x27, 0x0a, 0x06, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28,
	0x0b, 0x32, 0x0f, 0x2e, 0x64, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x4c, 0x69, 0x74, 0x65, 0x72,
	0x61, 0x6c, 0x52, 0x06, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x73, 0x22, 0x32, 0x0a, 0x06, 0x4f, 0x62,
	0x6a, 0x65, 0x63, 0x74, 0x12, 0x28, 0x0a, 0x06, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x73, 0x18, 0x01,
	0x20, 0x03, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x64, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x41, 0x72,
	0x67, 0x75, 0x6d, 0x65, 0x6e, 0x74, 0x52, 0x06, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x73, 0x22, 0x60,
	0x0a, 0x04, 0x54, 0x79, 0x70, 0x65, 0x12, 0x1c, 0x0a, 0x09, 0x6e, 0x61, 0x6d, 0x65, 0x64, 0x54,
	0x79, 0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x6e, 0x61, 0x6d, 0x65, 0x64,
	0x54, 0x79, 0x70, 0x65, 0x12, 0x20, 0x0a, 0x04, 0x65, 0x6c, 0x65, 0x6d, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x0c, 0x2e, 0x64, 0x61, 0x67, 0x67, 0x65, 0x72, 0x2e, 0x54, 0x79, 0x70, 0x65,
	0x52, 0x04, 0x65, 0x6c, 0x65, 0x6d, 0x12, 0x18, 0x0a, 0x07, 0x6e, 0x6f, 0x6e, 0x4e, 0x75, 0x6c,
	0x6c, 0x18, 0x03, 0x20, 0x01, 0x28, 0x08, 0x52, 0x07, 0x6e, 0x6f, 0x6e, 0x4e, 0x75, 0x6c, 0x6c,
	0x42, 0x0c, 0x5a, 0x0a, 0x2e, 0x2f, 0x63, 0x61, 0x6c, 0x6c, 0x70, 0x62, 0x76, 0x31, 0x62, 0x06,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_call_proto_rawDescOnce sync.Once
	file_call_proto_rawDescData = file_call_proto_rawDesc
)

func file_call_proto_rawDescGZIP() []byte {
	file_call_proto_rawDescOnce.Do(func() {
		file_call_proto_rawDescData = protoimpl.X.CompressGZIP(file_call_proto_rawDescData)
	})
	return file_call_proto_rawDescData
}

var file_call_proto_msgTypes = make([]protoimpl.MessageInfo, 9)
var file_call_proto_goTypes = []any{
	(*DAG)(nil),      // 0: dagger.DAG
	(*Call)(nil),     // 1: dagger.Call
	(*Module)(nil),   // 2: dagger.Module
	(*Argument)(nil), // 3: dagger.Argument
	(*Literal)(nil),  // 4: dagger.Literal
	(*List)(nil),     // 5: dagger.List
	(*Object)(nil),   // 6: dagger.Object
	(*Type)(nil),     // 7: dagger.Type
	nil,              // 8: dagger.DAG.CallsByDigestEntry
}
var file_call_proto_depIdxs = []int32{
	8,  // 0: dagger.DAG.callsByDigest:type_name -> dagger.DAG.CallsByDigestEntry
	7,  // 1: dagger.Call.type:type_name -> dagger.Type
	3,  // 2: dagger.Call.args:type_name -> dagger.Argument
	2,  // 3: dagger.Call.module:type_name -> dagger.Module
	4,  // 4: dagger.Argument.value:type_name -> dagger.Literal
	5,  // 5: dagger.Literal.list:type_name -> dagger.List
	6,  // 6: dagger.Literal.object:type_name -> dagger.Object
	4,  // 7: dagger.List.values:type_name -> dagger.Literal
	3,  // 8: dagger.Object.values:type_name -> dagger.Argument
	7,  // 9: dagger.Type.elem:type_name -> dagger.Type
	1,  // 10: dagger.DAG.CallsByDigestEntry.value:type_name -> dagger.Call
	11, // [11:11] is the sub-list for method output_type
	11, // [11:11] is the sub-list for method input_type
	11, // [11:11] is the sub-list for extension type_name
	11, // [11:11] is the sub-list for extension extendee
	0,  // [0:11] is the sub-list for field type_name
}

func init() { file_call_proto_init() }
func file_call_proto_init() {
	if File_call_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_call_proto_msgTypes[0].Exporter = func(v any, i int) any {
			switch v := v.(*DAG); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_call_proto_msgTypes[1].Exporter = func(v any, i int) any {
			switch v := v.(*Call); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_call_proto_msgTypes[2].Exporter = func(v any, i int) any {
			switch v := v.(*Module); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_call_proto_msgTypes[3].Exporter = func(v any, i int) any {
			switch v := v.(*Argument); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_call_proto_msgTypes[4].Exporter = func(v any, i int) any {
			switch v := v.(*Literal); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_call_proto_msgTypes[5].Exporter = func(v any, i int) any {
			switch v := v.(*List); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_call_proto_msgTypes[6].Exporter = func(v any, i int) any {
			switch v := v.(*Object); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_call_proto_msgTypes[7].Exporter = func(v any, i int) any {
			switch v := v.(*Type); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	file_call_proto_msgTypes[4].OneofWrappers = []any{
		(*Literal_CallDigest)(nil),
		(*Literal_Null)(nil),
		(*Literal_Bool)(nil),
		(*Literal_Enum)(nil),
		(*Literal_Int)(nil),
		(*Literal_Float)(nil),
		(*Literal_String_)(nil),
		(*Literal_List)(nil),
		(*Literal_Object)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_call_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   9,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_call_proto_goTypes,
		DependencyIndexes: file_call_proto_depIdxs,
		MessageInfos:      file_call_proto_msgTypes,
	}.Build()
	File_call_proto = out.File
	file_call_proto_rawDesc = nil
	file_call_proto_goTypes = nil
	file_call_proto_depIdxs = nil
}
