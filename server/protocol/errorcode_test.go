// Copyright (C) 2026 RBsGameLab
// SPDX-License-Identifier: AGPL-3.0-only

package protocol

import "testing"

func TestErrorCodeOfReadsTheReplyAndTheMessageInsideAOneof(t *testing.T) {
	notExist := ErrorCode_ErrorCode_ErrSceneEntityNotExist
	if got := ErrorCodeOf(&DamageExecuteResponse{ErrorCode: notExist}); got != notExist {
		t.Fatalf("top-level errorCode: %v", got)
	}
	if got := ErrorCodeOf(&DamageExecuteResponse{}); got != ErrorCode_ErrorCode_Success {
		t.Fatalf("unset: %v", got)
	}
	wrapped := &CombatResponseData{Message: &CombatResponseData_DamageExecuteResponse{DamageExecuteResponse: &DamageExecuteResponse{ErrorCode: notExist}}}
	if got := ErrorCodeOf(wrapped); got != notExist {
		t.Fatalf("inside the oneof: %v", got)
	}
	if got := ErrorCodeOf(&CombatResponseData{}); got != ErrorCode_ErrorCode_Success {
		t.Fatalf("empty oneof: %v", got)
	}
	var none *DamageExecuteResponse
	if got := ErrorCodeOf(none); got != ErrorCode_ErrorCode_Success {
		t.Fatalf("nil reply: %v", got)
	}
	if got := ErrorCodeOf(nil); got != ErrorCode_ErrorCode_Success {
		t.Fatalf("nil interface: %v", got)
	}
}
