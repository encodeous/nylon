// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.2
// 	protoc        v5.29.3
// source: protocol/nylon.proto

package protocol

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

type Source struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=Id,proto3" json:"Id,omitempty"`
	Seqno         uint32                 `protobuf:"varint,2,opt,name=Seqno,proto3" json:"Seqno,omitempty"`
	Sig           []byte                 `protobuf:"bytes,3,opt,name=Sig,proto3" json:"Sig,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Source) Reset() {
	*x = Source{}
	mi := &file_protocol_nylon_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Source) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Source) ProtoMessage() {}

func (x *Source) ProtoReflect() protoreflect.Message {
	mi := &file_protocol_nylon_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Source.ProtoReflect.Descriptor instead.
func (*Source) Descriptor() ([]byte, []int) {
	return file_protocol_nylon_proto_rawDescGZIP(), []int{0}
}

func (x *Source) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Source) GetSeqno() uint32 {
	if x != nil {
		return x.Seqno
	}
	return 0
}

func (x *Source) GetSig() []byte {
	if x != nil {
		return x.Sig
	}
	return nil
}

type HsMsg struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Types that are valid to be assigned to Type:
	//
	//	*HsMsg_Hello
	Type          isHsMsg_Type `protobuf_oneof:"type"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *HsMsg) Reset() {
	*x = HsMsg{}
	mi := &file_protocol_nylon_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *HsMsg) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*HsMsg) ProtoMessage() {}

func (x *HsMsg) ProtoReflect() protoreflect.Message {
	mi := &file_protocol_nylon_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use HsMsg.ProtoReflect.Descriptor instead.
func (*HsMsg) Descriptor() ([]byte, []int) {
	return file_protocol_nylon_proto_rawDescGZIP(), []int{1}
}

func (x *HsMsg) GetType() isHsMsg_Type {
	if x != nil {
		return x.Type
	}
	return nil
}

func (x *HsMsg) GetHello() *HsHello {
	if x != nil {
		if x, ok := x.Type.(*HsMsg_Hello); ok {
			return x.Hello
		}
	}
	return nil
}

type isHsMsg_Type interface {
	isHsMsg_Type()
}

type HsMsg_Hello struct {
	Hello *HsHello `protobuf:"bytes,1,opt,name=Hello,proto3,oneof"`
}

func (*HsMsg_Hello) isHsMsg_Type() {}

type HsHello struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=Id,proto3" json:"Id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *HsHello) Reset() {
	*x = HsHello{}
	mi := &file_protocol_nylon_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *HsHello) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*HsHello) ProtoMessage() {}

func (x *HsHello) ProtoReflect() protoreflect.Message {
	mi := &file_protocol_nylon_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use HsHello.ProtoReflect.Descriptor instead.
func (*HsHello) Descriptor() ([]byte, []int) {
	return file_protocol_nylon_proto_rawDescGZIP(), []int{2}
}

func (x *HsHello) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

type CtlMsg struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Types that are valid to be assigned to Type:
	//
	//	*CtlMsg_Route
	//	*CtlMsg_Seqno
	Type          isCtlMsg_Type `protobuf_oneof:"type"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CtlMsg) Reset() {
	*x = CtlMsg{}
	mi := &file_protocol_nylon_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CtlMsg) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CtlMsg) ProtoMessage() {}

func (x *CtlMsg) ProtoReflect() protoreflect.Message {
	mi := &file_protocol_nylon_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CtlMsg.ProtoReflect.Descriptor instead.
func (*CtlMsg) Descriptor() ([]byte, []int) {
	return file_protocol_nylon_proto_rawDescGZIP(), []int{3}
}

func (x *CtlMsg) GetType() isCtlMsg_Type {
	if x != nil {
		return x.Type
	}
	return nil
}

func (x *CtlMsg) GetRoute() *CtlRouteUpdate {
	if x != nil {
		if x, ok := x.Type.(*CtlMsg_Route); ok {
			return x.Route
		}
	}
	return nil
}

func (x *CtlMsg) GetSeqno() *CtlSeqnoRequest {
	if x != nil {
		if x, ok := x.Type.(*CtlMsg_Seqno); ok {
			return x.Seqno
		}
	}
	return nil
}

type isCtlMsg_Type interface {
	isCtlMsg_Type()
}

type CtlMsg_Route struct {
	Route *CtlRouteUpdate `protobuf:"bytes,1,opt,name=Route,proto3,oneof"`
}

type CtlMsg_Seqno struct {
	Seqno *CtlSeqnoRequest `protobuf:"bytes,2,opt,name=Seqno,proto3,oneof"`
}

func (*CtlMsg_Route) isCtlMsg_Type() {}

func (*CtlMsg_Seqno) isCtlMsg_Type() {}

type CtlRouteUpdate struct {
	state         protoimpl.MessageState   `protogen:"open.v1"`
	Urgent        bool                     `protobuf:"varint,1,opt,name=Urgent,proto3" json:"Urgent,omitempty"`
	Updates       []*CtlRouteUpdate_Params `protobuf:"bytes,2,rep,name=Updates,proto3" json:"Updates,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CtlRouteUpdate) Reset() {
	*x = CtlRouteUpdate{}
	mi := &file_protocol_nylon_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CtlRouteUpdate) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CtlRouteUpdate) ProtoMessage() {}

func (x *CtlRouteUpdate) ProtoReflect() protoreflect.Message {
	mi := &file_protocol_nylon_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CtlRouteUpdate.ProtoReflect.Descriptor instead.
func (*CtlRouteUpdate) Descriptor() ([]byte, []int) {
	return file_protocol_nylon_proto_rawDescGZIP(), []int{4}
}

func (x *CtlRouteUpdate) GetUrgent() bool {
	if x != nil {
		return x.Urgent
	}
	return false
}

func (x *CtlRouteUpdate) GetUpdates() []*CtlRouteUpdate_Params {
	if x != nil {
		return x.Updates
	}
	return nil
}

type CtlSeqnoRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Current       *Source                `protobuf:"bytes,1,opt,name=Current,proto3" json:"Current,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CtlSeqnoRequest) Reset() {
	*x = CtlSeqnoRequest{}
	mi := &file_protocol_nylon_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CtlSeqnoRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CtlSeqnoRequest) ProtoMessage() {}

func (x *CtlSeqnoRequest) ProtoReflect() protoreflect.Message {
	mi := &file_protocol_nylon_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CtlSeqnoRequest.ProtoReflect.Descriptor instead.
func (*CtlSeqnoRequest) Descriptor() ([]byte, []int) {
	return file_protocol_nylon_proto_rawDescGZIP(), []int{5}
}

func (x *CtlSeqnoRequest) GetCurrent() *Source {
	if x != nil {
		return x.Current
	}
	return nil
}

type CtlRouteUpdate_Params struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Source        *Source                `protobuf:"bytes,1,opt,name=Source,proto3" json:"Source,omitempty"`
	Metric        uint32                 `protobuf:"varint,2,opt,name=Metric,proto3" json:"Metric,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CtlRouteUpdate_Params) Reset() {
	*x = CtlRouteUpdate_Params{}
	mi := &file_protocol_nylon_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CtlRouteUpdate_Params) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CtlRouteUpdate_Params) ProtoMessage() {}

func (x *CtlRouteUpdate_Params) ProtoReflect() protoreflect.Message {
	mi := &file_protocol_nylon_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CtlRouteUpdate_Params.ProtoReflect.Descriptor instead.
func (*CtlRouteUpdate_Params) Descriptor() ([]byte, []int) {
	return file_protocol_nylon_proto_rawDescGZIP(), []int{4, 0}
}

func (x *CtlRouteUpdate_Params) GetSource() *Source {
	if x != nil {
		return x.Source
	}
	return nil
}

func (x *CtlRouteUpdate_Params) GetMetric() uint32 {
	if x != nil {
		return x.Metric
	}
	return 0
}

var File_protocol_nylon_proto protoreflect.FileDescriptor

var file_protocol_nylon_proto_rawDesc = []byte{
	0x0a, 0x14, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x2f, 0x6e, 0x79, 0x6c, 0x6f, 0x6e,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x05, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x40, 0x0a,
	0x06, 0x53, 0x6f, 0x75, 0x72, 0x63, 0x65, 0x12, 0x0e, 0x0a, 0x02, 0x49, 0x64, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x02, 0x49, 0x64, 0x12, 0x14, 0x0a, 0x05, 0x53, 0x65, 0x71, 0x6e, 0x6f,
	0x18, 0x02, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x05, 0x53, 0x65, 0x71, 0x6e, 0x6f, 0x12, 0x10, 0x0a,
	0x03, 0x53, 0x69, 0x67, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x03, 0x53, 0x69, 0x67, 0x22,
	0x37, 0x0a, 0x05, 0x48, 0x73, 0x4d, 0x73, 0x67, 0x12, 0x26, 0x0a, 0x05, 0x48, 0x65, 0x6c, 0x6c,
	0x6f, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0e, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e,
	0x48, 0x73, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x48, 0x00, 0x52, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f,
	0x42, 0x06, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x22, 0x19, 0x0a, 0x07, 0x48, 0x73, 0x48, 0x65,
	0x6c, 0x6c, 0x6f, 0x12, 0x0e, 0x0a, 0x02, 0x49, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x02, 0x49, 0x64, 0x22, 0x6f, 0x0a, 0x06, 0x43, 0x74, 0x6c, 0x4d, 0x73, 0x67, 0x12, 0x2d, 0x0a,
	0x05, 0x52, 0x6f, 0x75, 0x74, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x15, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x43, 0x74, 0x6c, 0x52, 0x6f, 0x75, 0x74, 0x65, 0x55, 0x70, 0x64,
	0x61, 0x74, 0x65, 0x48, 0x00, 0x52, 0x05, 0x52, 0x6f, 0x75, 0x74, 0x65, 0x12, 0x2e, 0x0a, 0x05,
	0x53, 0x65, 0x71, 0x6e, 0x6f, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x16, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2e, 0x43, 0x74, 0x6c, 0x53, 0x65, 0x71, 0x6e, 0x6f, 0x52, 0x65, 0x71, 0x75,
	0x65, 0x73, 0x74, 0x48, 0x00, 0x52, 0x05, 0x53, 0x65, 0x71, 0x6e, 0x6f, 0x42, 0x06, 0x0a, 0x04,
	0x74, 0x79, 0x70, 0x65, 0x22, 0xa9, 0x01, 0x0a, 0x0e, 0x43, 0x74, 0x6c, 0x52, 0x6f, 0x75, 0x74,
	0x65, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x55, 0x72, 0x67, 0x65, 0x6e,
	0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x08, 0x52, 0x06, 0x55, 0x72, 0x67, 0x65, 0x6e, 0x74, 0x12,
	0x36, 0x0a, 0x07, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b,
	0x32, 0x1c, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x43, 0x74, 0x6c, 0x52, 0x6f, 0x75, 0x74,
	0x65, 0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x2e, 0x50, 0x61, 0x72, 0x61, 0x6d, 0x73, 0x52, 0x07,
	0x55, 0x70, 0x64, 0x61, 0x74, 0x65, 0x73, 0x1a, 0x47, 0x0a, 0x06, 0x50, 0x61, 0x72, 0x61, 0x6d,
	0x73, 0x12, 0x25, 0x0a, 0x06, 0x53, 0x6f, 0x75, 0x72, 0x63, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x0d, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x53, 0x6f, 0x75, 0x72, 0x63, 0x65,
	0x52, 0x06, 0x53, 0x6f, 0x75, 0x72, 0x63, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x4d, 0x65, 0x74, 0x72,
	0x69, 0x63, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x06, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63,
	0x22, 0x3a, 0x0a, 0x0f, 0x43, 0x74, 0x6c, 0x53, 0x65, 0x71, 0x6e, 0x6f, 0x52, 0x65, 0x71, 0x75,
	0x65, 0x73, 0x74, 0x12, 0x27, 0x0a, 0x07, 0x43, 0x75, 0x72, 0x72, 0x65, 0x6e, 0x74, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0b, 0x32, 0x0d, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x53, 0x6f, 0x75,
	0x72, 0x63, 0x65, 0x52, 0x07, 0x43, 0x75, 0x72, 0x72, 0x65, 0x6e, 0x74, 0x42, 0x0b, 0x5a, 0x09,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x2f, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x33,
}

var (
	file_protocol_nylon_proto_rawDescOnce sync.Once
	file_protocol_nylon_proto_rawDescData = file_protocol_nylon_proto_rawDesc
)

func file_protocol_nylon_proto_rawDescGZIP() []byte {
	file_protocol_nylon_proto_rawDescOnce.Do(func() {
		file_protocol_nylon_proto_rawDescData = protoimpl.X.CompressGZIP(file_protocol_nylon_proto_rawDescData)
	})
	return file_protocol_nylon_proto_rawDescData
}

var file_protocol_nylon_proto_msgTypes = make([]protoimpl.MessageInfo, 7)
var file_protocol_nylon_proto_goTypes = []any{
	(*Source)(nil),                // 0: proto.Source
	(*HsMsg)(nil),                 // 1: proto.HsMsg
	(*HsHello)(nil),               // 2: proto.HsHello
	(*CtlMsg)(nil),                // 3: proto.CtlMsg
	(*CtlRouteUpdate)(nil),        // 4: proto.CtlRouteUpdate
	(*CtlSeqnoRequest)(nil),       // 5: proto.CtlSeqnoRequest
	(*CtlRouteUpdate_Params)(nil), // 6: proto.CtlRouteUpdate.Params
}
var file_protocol_nylon_proto_depIdxs = []int32{
	2, // 0: proto.HsMsg.Hello:type_name -> proto.HsHello
	4, // 1: proto.CtlMsg.Route:type_name -> proto.CtlRouteUpdate
	5, // 2: proto.CtlMsg.Seqno:type_name -> proto.CtlSeqnoRequest
	6, // 3: proto.CtlRouteUpdate.Updates:type_name -> proto.CtlRouteUpdate.Params
	0, // 4: proto.CtlSeqnoRequest.Current:type_name -> proto.Source
	0, // 5: proto.CtlRouteUpdate.Params.Source:type_name -> proto.Source
	6, // [6:6] is the sub-list for method output_type
	6, // [6:6] is the sub-list for method input_type
	6, // [6:6] is the sub-list for extension type_name
	6, // [6:6] is the sub-list for extension extendee
	0, // [0:6] is the sub-list for field type_name
}

func init() { file_protocol_nylon_proto_init() }
func file_protocol_nylon_proto_init() {
	if File_protocol_nylon_proto != nil {
		return
	}
	file_protocol_nylon_proto_msgTypes[1].OneofWrappers = []any{
		(*HsMsg_Hello)(nil),
	}
	file_protocol_nylon_proto_msgTypes[3].OneofWrappers = []any{
		(*CtlMsg_Route)(nil),
		(*CtlMsg_Seqno)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_protocol_nylon_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   7,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_protocol_nylon_proto_goTypes,
		DependencyIndexes: file_protocol_nylon_proto_depIdxs,
		MessageInfos:      file_protocol_nylon_proto_msgTypes,
	}.Build()
	File_protocol_nylon_proto = out.File
	file_protocol_nylon_proto_rawDesc = nil
	file_protocol_nylon_proto_goTypes = nil
	file_protocol_nylon_proto_depIdxs = nil
}
