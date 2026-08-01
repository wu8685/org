package safety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

type IdempotentDownstream interface {
	ApplyOnce(context.Context, string, []byte) error
}
type WriteRequest struct {
	WorkflowID, ActivityID string
	Payload                []byte
}

func ExecuteIdempotentWrite(ctx context.Context, downstream IdempotentDownstream, req WriteRequest) error {
	if req.WorkflowID == "" || req.ActivityID == "" {
		return errors.New("workflow and activity identity are required")
	}
	sum := sha256.Sum256([]byte(req.WorkflowID + "\x00" + req.ActivityID))
	return downstream.ApplyOnce(ctx, hex.EncodeToString(sum[:]), req.Payload)
}
