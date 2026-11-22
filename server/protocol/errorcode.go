// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package protocol

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const errorCodeEnum protoreflect.FullName = "aki.protocol.ErrorCode"

// ErrorCodeOf is the ErrorCode a reply carries: its first top-level field of the ErrorCode enum
// (errorCode, errorId or error), or, for a oneof wrapper such as CombatResponseData, that of
// the message set in it. Success (0) when it has none (R-166: the gateway logs the others).
func ErrorCodeOf(m proto.Message) ErrorCode {
	if m == nil {
		return ErrorCode_ErrorCode_Success
	}
	r := m.ProtoReflect()
	if !r.IsValid() {
		return ErrorCode_ErrorCode_Success
	}
	fields := r.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.Kind() == protoreflect.EnumKind && f.Cardinality() != protoreflect.Repeated && f.Enum().FullName() == errorCodeEnum {
			return ErrorCode(r.Get(f).Enum())
		}
	}
	oneofs := r.Descriptor().Oneofs()
	for i := 0; i < oneofs.Len(); i++ {
		if f := r.WhichOneof(oneofs.Get(i)); f != nil && f.Kind() == protoreflect.MessageKind {
			return ErrorCodeOf(r.Get(f).Message().Interface())
		}
	}
	return ErrorCode_ErrorCode_Success
}
