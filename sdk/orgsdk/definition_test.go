package orgsdk

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDefinitionValidatesActivitySafetyAndDynamicBounds(t *testing.T) {
	valid := Definition{
		Name:   "dynamic-demo",
		Bounds: RuntimeBounds{MaxInstancesPerFanOut: 4, MaxRuntimeNodes: 12, MaxProjectionBytes: 64 << 10},
		Templates: []NodeTemplate{
			{ID: "decide", Label: "Decide", Type: NodeTypeActivity, Activity: &ActivityPolicy{SideEffect: SideEffectRead, Retry: RetryPolicy{MaximumAttempts: 3, StartToCloseTimeout: time.Second}}},
			{ID: "write", Label: "Write", Type: NodeTypeActivity, Activity: &ActivityPolicy{SideEffect: SideEffectWrite, Retry: RetryPolicy{MaximumAttempts: 3, StartToCloseTimeout: time.Second}, Idempotency: &IdempotencyPolicy{BusinessKeyRequired: true}}},
			{ID: "wait", Label: "Wait", Type: NodeTypeWaitForAction, Actions: []ActionDefinition{{Name: "confirm", Label: "Confirm", RequiredPermission: "run:action:confirm"}}},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid definition: %v", err)
	}

	invalidWrite := valid
	invalidWrite.Templates = append([]NodeTemplate(nil), valid.Templates...)
	invalidWrite.Templates[1].Activity = &ActivityPolicy{SideEffect: SideEffectWrite, Retry: RetryPolicy{MaximumAttempts: 3, StartToCloseTimeout: time.Second}}
	if err := invalidWrite.Validate(); err == nil || !strings.Contains(err.Error(), "idempotency or reconciliation") {
		t.Fatalf("unsafe write error = %v", err)
	}

	invalidBounds := valid
	invalidBounds.Bounds.MaxRuntimeNodes = 0
	if err := invalidBounds.Validate(); err == nil || !strings.Contains(err.Error(), "runtime bounds") {
		t.Fatalf("invalid bounds error = %v", err)
	}
}

func TestDefinitionRejectsAmbiguousActionsInvalidSchemaAndCardinality(t *testing.T) {
	definition := Definition{
		Name: "bad-actions", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 2, MaxRuntimeNodes: 4, MaxProjectionBytes: 4096},
		Templates: []NodeTemplate{
			{ID: "first", Label: "First", Type: NodeTypeWaitForAction, Cardinality: "sometimes", Actions: []ActionDefinition{{Name: "confirm", Label: "Confirm", RequiredPermission: "run:action:confirm", InputSchema: json.RawMessage(`{"type":`)}}},
			{ID: "second", Label: "Second", Type: NodeTypeWaitForAction, Actions: []ActionDefinition{{Name: "confirm", Label: "Confirm again", RequiredPermission: "run:action:confirm"}}},
		},
	}
	err := definition.Validate()
	if err == nil || !strings.Contains(err.Error(), "cardinality") || !strings.Contains(err.Error(), "schema") || !strings.Contains(err.Error(), "duplicate action") {
		t.Fatalf("definition validation error = %v", err)
	}
}

func TestDefinitionRejectsRetryableReconciliationWriteWithoutIdempotency(t *testing.T) {
	definition := Definition{
		Name: "unsafe-reconciliation", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 1, MaxProjectionBytes: 4096},
		Templates: []NodeTemplate{{
			ID: "write", Label: "Write", Type: NodeTypeActivity,
			Activity: &ActivityPolicy{SideEffect: SideEffectWrite, Retry: RetryPolicy{MaximumAttempts: 2, StartToCloseTimeout: time.Second}, Reconciliation: "inspect downstream outcome"},
		}},
	}
	err := definition.Validate()
	if err == nil || !strings.Contains(err.Error(), "disable automatic retries") {
		t.Fatalf("unsafe reconciliation policy error = %v", err)
	}
}

func TestDefinitionRejectsDuplicateTemplatesAndInvalidWaitActions(t *testing.T) {
	definition := Definition{
		Name:   "bad",
		Bounds: RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 2, MaxProjectionBytes: 1024},
		Templates: []NodeTemplate{
			{ID: "same", Label: "One", Type: NodeTypeSemantic},
			{ID: "same", Label: "Two", Type: NodeTypeWaitForAction, Actions: []ActionDefinition{{Name: "confirm"}}},
		},
	}
	if err := definition.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate template") || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("definition error = %v", err)
	}
}
