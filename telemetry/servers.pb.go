// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.33.0
// 	protoc        v3.19.6
// source: servers.proto

package telemetry

import (
	v11 "go.opentelemetry.io/proto/otlp/logs/v1"
	v1 "go.opentelemetry.io/proto/otlp/trace/v1"
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

type TelemetryRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	TraceId []byte `protobuf:"bytes,1,opt,name=trace_id,json=traceId,proto3" json:"trace_id,omitempty"`
}

func (x *TelemetryRequest) Reset() {
	*x = TelemetryRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_servers_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TelemetryRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TelemetryRequest) ProtoMessage() {}

func (x *TelemetryRequest) ProtoReflect() protoreflect.Message {
	mi := &file_servers_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TelemetryRequest.ProtoReflect.Descriptor instead.
func (*TelemetryRequest) Descriptor() ([]byte, []int) {
	return file_servers_proto_rawDescGZIP(), []int{0}
}

func (x *TelemetryRequest) GetTraceId() []byte {
	if x != nil {
		return x.TraceId
	}
	return nil
}

var File_servers_proto protoreflect.FileDescriptor

var file_servers_proto_rawDesc = []byte{
	0x0a, 0x0d, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12,
	0x09, 0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x1a, 0x26, 0x6f, 0x70, 0x65, 0x6e,
	0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f,
	0x6c, 0x6f, 0x67, 0x73, 0x2f, 0x76, 0x31, 0x2f, 0x6c, 0x6f, 0x67, 0x73, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x1a, 0x28, 0x6f, 0x70, 0x65, 0x6e, 0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72,
	0x79, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x74, 0x72, 0x61, 0x63, 0x65, 0x2f, 0x76, 0x31,
	0x2f, 0x74, 0x72, 0x61, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x2d, 0x0a, 0x10,
	0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x12, 0x19, 0x0a, 0x08, 0x74, 0x72, 0x61, 0x63, 0x65, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0c, 0x52, 0x07, 0x74, 0x72, 0x61, 0x63, 0x65, 0x49, 0x64, 0x32, 0x66, 0x0a, 0x0c, 0x54,
	0x72, 0x61, 0x63, 0x65, 0x73, 0x53, 0x6f, 0x75, 0x72, 0x63, 0x65, 0x12, 0x56, 0x0a, 0x09, 0x53,
	0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x12, 0x1b, 0x2e, 0x74, 0x65, 0x6c, 0x65, 0x6d,
	0x65, 0x74, 0x72, 0x79, 0x2e, 0x54, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x28, 0x2e, 0x6f, 0x70, 0x65, 0x6e, 0x74, 0x65, 0x6c, 0x65,
	0x6d, 0x65, 0x74, 0x72, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x74, 0x72, 0x61, 0x63,
	0x65, 0x2e, 0x76, 0x31, 0x2e, 0x54, 0x72, 0x61, 0x63, 0x65, 0x73, 0x44, 0x61, 0x74, 0x61, 0x22,
	0x00, 0x30, 0x01, 0x32, 0x61, 0x0a, 0x0a, 0x4c, 0x6f, 0x67, 0x73, 0x53, 0x6f, 0x75, 0x72, 0x63,
	0x65, 0x12, 0x53, 0x0a, 0x09, 0x53, 0x75, 0x62, 0x73, 0x63, 0x72, 0x69, 0x62, 0x65, 0x12, 0x1b,
	0x2e, 0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x2e, 0x54, 0x65, 0x6c, 0x65, 0x6d,
	0x65, 0x74, 0x72, 0x79, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x25, 0x2e, 0x6f, 0x70,
	0x65, 0x6e, 0x74, 0x65, 0x6c, 0x65, 0x6d, 0x65, 0x74, 0x72, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2e, 0x6c, 0x6f, 0x67, 0x73, 0x2e, 0x76, 0x31, 0x2e, 0x4c, 0x6f, 0x67, 0x73, 0x44, 0x61,
	0x74, 0x61, 0x22, 0x00, 0x30, 0x01, 0x42, 0x0d, 0x5a, 0x0b, 0x2e, 0x2f, 0x74, 0x65, 0x6c, 0x65,
	0x6d, 0x65, 0x74, 0x72, 0x79, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_servers_proto_rawDescOnce sync.Once
	file_servers_proto_rawDescData = file_servers_proto_rawDesc
)

func file_servers_proto_rawDescGZIP() []byte {
	file_servers_proto_rawDescOnce.Do(func() {
		file_servers_proto_rawDescData = protoimpl.X.CompressGZIP(file_servers_proto_rawDescData)
	})
	return file_servers_proto_rawDescData
}

var file_servers_proto_msgTypes = make([]protoimpl.MessageInfo, 1)
var file_servers_proto_goTypes = []interface{}{
	(*TelemetryRequest)(nil), // 0: telemetry.TelemetryRequest
	(*v1.TracesData)(nil),    // 1: opentelemetry.proto.trace.v1.TracesData
	(*v11.LogsData)(nil),     // 2: opentelemetry.proto.logs.v1.LogsData
}
var file_servers_proto_depIdxs = []int32{
	0, // 0: telemetry.TracesSource.Subscribe:input_type -> telemetry.TelemetryRequest
	0, // 1: telemetry.LogsSource.Subscribe:input_type -> telemetry.TelemetryRequest
	1, // 2: telemetry.TracesSource.Subscribe:output_type -> opentelemetry.proto.trace.v1.TracesData
	2, // 3: telemetry.LogsSource.Subscribe:output_type -> opentelemetry.proto.logs.v1.LogsData
	2, // [2:4] is the sub-list for method output_type
	0, // [0:2] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_servers_proto_init() }
func file_servers_proto_init() {
	if File_servers_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_servers_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TelemetryRequest); i {
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
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_servers_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   1,
			NumExtensions: 0,
			NumServices:   2,
		},
		GoTypes:           file_servers_proto_goTypes,
		DependencyIndexes: file_servers_proto_depIdxs,
		MessageInfos:      file_servers_proto_msgTypes,
	}.Build()
	File_servers_proto = out.File
	file_servers_proto_rawDesc = nil
	file_servers_proto_goTypes = nil
	file_servers_proto_depIdxs = nil
}
