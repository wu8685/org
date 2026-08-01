package orgsdk

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	temporalactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestAwaitConfirmationUsesWorkflowSignalAndProjectsIdleNode(t *testing.T) {
	definition := Definition{
		Name:   "approval",
		Bounds: RuntimeBounds{MaxInstancesPerFanOut: 4, MaxRuntimeNodes: 8, MaxProjectionBytes: 64 << 10},
		Templates: []NodeTemplate{
			{ID: "approval-gate", Label: "Confirm start", Type: NodeTypeWaitForAction, Actions: []ActionDefinition{{Name: "confirm", Label: "Confirm", RequiredPermission: "run:action:confirm"}}},
			{ID: "finalize", Label: "Finalize", Type: NodeTypeSemantic},
		},
	}
	workflowDefinition, err := NewWorkflowDefinition("ApprovalWorkflow", "v1", definition, func(ctx *WorkflowContext, input string) (string, error) {
		action, err := AwaitConfirmation(ctx, "approval-gate", "singleton", nil, 10*time.Minute)
		if err != nil {
			return "", err
		}
		if action.Action != "confirm" {
			return "", ErrActionRejected
		}
		if _, err := CompleteSemantic(ctx, "finalize", "singleton", action.Node, []NodeRef{action.Node}); err != nil {
			return "", err
		}
		return input + "-confirmed", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowDefinition.workflow)
	nodeID := RuntimeNodeID("approval", "", "approval-gate", "singleton")
	env.RegisterDelayedCallback(func() {
		value, queryErr := env.QueryWorkflow(ReservedProjectionQuery)
		if queryErr != nil {
			t.Errorf("query waiting projection: %v", queryErr)
			return
		}
		var projection Projection
		if err := value.Get(&projection); err != nil {
			t.Errorf("decode projection: %v", err)
			return
		}
		if projection.Node(nodeID).Status != NodeStatusWaitingForUser || len(projection.AllowedActions) != 1 {
			t.Errorf("waiting projection = %#v", projection)
		}
		env.SignalWorkflow(ReservedActionSignal, ActionEnvelope{OperationID: "op-1", NodeID: nodeID, Action: "confirm"})
	}, time.Second)

	env.ExecuteWorkflow(workflowDefinition.workflow, "demo")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var result string
	if err := env.GetWorkflowResult(&result); err != nil || result != "demo-confirmed" {
		t.Fatalf("result=%q err=%v", result, err)
	}
	value, err := env.QueryWorkflow(ReservedProjectionQuery)
	if err != nil {
		t.Fatal(err)
	}
	var projection Projection
	if err := value.Get(&projection); err != nil {
		t.Fatal(err)
	}
	if projection.Node(nodeID).Status != NodeStatusCompleted || len(projection.AllowedActions) != 0 {
		t.Fatalf("terminal projection = %#v", projection)
	}
}

func TestWaitForActionDecodesCustomInputAndRecordsAcceptance(t *testing.T) {
	type decision struct {
		Comment string `json:"comment"`
	}
	definition := Definition{
		Name: "custom-action", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 2, MaxRuntimeNodes: 2, MaxProjectionBytes: 8192},
		Templates: []NodeTemplate{{
			ID: "review", Label: "Review", Type: NodeTypeWaitForAction,
			Actions: []ActionDefinition{{Name: "approve", Label: "Approve", RequiredPermission: "run:action:approve"}, {Name: "reject", Label: "Reject", RequiredPermission: "run:action:reject"}},
		}},
	}
	workflowDefinition, err := NewWorkflowDefinition("CustomActionWorkflow", "v1", definition, func(ctx *WorkflowContext, _ string) (string, error) {
		input, action, err := WaitForAction[decision](ctx, "review", "singleton", nil, time.Hour)
		if err != nil {
			return "", err
		}
		return action.Action + ":" + input.Comment, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowDefinition.workflow)
	nodeID := RuntimeNodeID("custom-action", "", "review", "singleton")
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ReservedActionSignal, ActionEnvelope{OperationID: "op-approve", NodeID: nodeID, Action: "approve", Input: []byte(`{"comment":"looks good"}`)})
	}, time.Second)
	env.ExecuteWorkflow(workflowDefinition.workflow, "")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var result string
	if err := env.GetWorkflowResult(&result); err != nil || result != "approve:looks good" {
		t.Fatalf("result=%q err=%v", result, err)
	}
	value, err := env.QueryWorkflow(ReservedProjectionQuery)
	if err != nil {
		t.Fatal(err)
	}
	var projection Projection
	if err := value.Get(&projection); err != nil {
		t.Fatal(err)
	}
	if len(projection.ActionOutcomes) != 1 || projection.ActionOutcomes[0].State != ActionOutcomeAccepted || projection.ActionOutcomes[0].OperationID != "op-approve" {
		t.Fatalf("action outcomes = %#v", projection.ActionOutcomes)
	}
}

func TestWaitForActionRequiresFinitePositiveTimeout(t *testing.T) {
	definition := Definition{
		Name: "invalid-wait", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 1, MaxProjectionBytes: 8192},
		Templates: []NodeTemplate{{
			ID: "review", Label: "Review", Type: NodeTypeWaitForAction,
			Actions: []ActionDefinition{{Name: "approve", Label: "Approve", RequiredPermission: "run:action:approve"}},
		}},
	}
	workflowDefinition, err := NewWorkflowDefinition("InvalidWaitWorkflow", "v1", definition, func(ctx *WorkflowContext, _ string) (string, error) {
		_, err := AwaitConfirmation(ctx, "review", "singleton", nil, 0)
		return "", err
	})
	if err != nil {
		t.Fatal(err)
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowDefinition.workflow)
	env.ExecuteWorkflow(workflowDefinition.workflow, "")
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "positive timeout") {
		t.Fatalf("invalid wait timeout error = %v", err)
	}
}

func TestRecordedActivityResultSelectsBranchAndSkipsCandidate(t *testing.T) {
	activityPolicy := ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}
	determine := NewActivity("determine-route", activityPolicy, func(_ ActivityContext, mode string) (string, error) {
		return mode, nil
	})
	concise := NewActivity("concise", activityPolicy, func(_ ActivityContext, input string) (string, error) {
		return "concise:" + input, nil
	})
	detailedCalls := 0
	detailed := NewActivity("detailed", activityPolicy, func(_ ActivityContext, input string) (string, error) {
		detailedCalls++
		return "detailed:" + input, nil
	})
	definition := Definition{
		Name:   "decision",
		Bounds: RuntimeBounds{MaxInstancesPerFanOut: 4, MaxRuntimeNodes: 8, MaxProjectionBytes: 64 << 10},
		Templates: []NodeTemplate{
			{ID: "determine-route", Label: "Determine route", Type: NodeTypeActivity, Activity: &activityPolicy},
			{ID: "concise", Label: "Concise", Type: NodeTypeActivity, Activity: &activityPolicy},
			{ID: "detailed", Label: "Detailed", Type: NodeTypeActivity, Activity: &activityPolicy},
			{ID: "finalize", Label: "Finalize", Type: NodeTypeSemantic},
		},
	}
	workflowDefinition, err := NewWorkflowDefinition("DecisionWorkflow", "v1", definition, func(ctx *WorkflowContext, input string) (string, error) {
		routeNode, route, err := ExecuteActivity(ctx, determine, "singleton", NodeRef{}, nil, input, "")
		if err != nil {
			return "", err
		}
		var selected, skipped NodeRef
		var output string
		switch route {
		case "concise":
			skipped, err = SkipNode(ctx, "detailed", "singleton", routeNode, []NodeRef{routeNode}, "route-not-selected")
			if err == nil {
				selected, output, err = ExecuteActivity(ctx, concise, "singleton", routeNode, []NodeRef{routeNode}, input, "")
			}
		case "detailed":
			skipped, err = SkipNode(ctx, "concise", "singleton", routeNode, []NodeRef{routeNode}, "route-not-selected")
			if err == nil {
				selected, output, err = ExecuteActivity(ctx, detailed, "singleton", routeNode, []NodeRef{routeNode}, input, "")
			}
		default:
			return "", ErrInvalidRoute
		}
		if err != nil {
			return "", err
		}
		if _, err := CompleteSemantic(ctx, "finalize", "singleton", selected, []NodeRef{selected, skipped}); err != nil {
			return "", err
		}
		return output, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowDefinition.workflow)
	env.RegisterActivityWithOptions(determine.handler, temporalactivity.RegisterOptions{Name: determine.Name})
	env.RegisterActivityWithOptions(concise.handler, temporalactivity.RegisterOptions{Name: concise.Name})
	env.RegisterActivityWithOptions(detailed.handler, temporalactivity.RegisterOptions{Name: detailed.Name})
	env.ExecuteWorkflow(workflowDefinition.workflow, "concise")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if detailedCalls != 0 {
		t.Fatalf("unselected Activity ran %d times", detailedCalls)
	}
	value, err := env.QueryWorkflow(ReservedProjectionQuery)
	if err != nil {
		t.Fatal(err)
	}
	var projection Projection
	if err := value.Get(&projection); err != nil {
		t.Fatal(err)
	}
	conciseID := RuntimeNodeID("decision", RuntimeNodeID("decision", "", "determine-route", "singleton"), "concise", "singleton")
	detailedID := RuntimeNodeID("decision", RuntimeNodeID("decision", "", "determine-route", "singleton"), "detailed", "singleton")
	if projection.Node(conciseID).Status != NodeStatusCompleted || projection.Node(detailedID).Status != NodeStatusSkipped {
		t.Fatalf("decision projection = %#v", projection)
	}
}

func TestActivityWrapperInjectsStableIdempotencyContextAndHooks(t *testing.T) {
	var before, after ActivityHookEvent
	policy := ActivityPolicy{
		SideEffect:  SideEffectWrite,
		Retry:       RetryPolicy{MaximumAttempts: 2, StartToCloseTimeout: time.Second},
		Idempotency: &IdempotencyPolicy{BusinessKeyRequired: true, PropagationField: "requestId"},
	}
	activity := NewActivity("write", policy, func(ctx ActivityContext, input string) (string, error) {
		if ctx.IdempotencyKey == "" || ctx.ActivityID == "" || ctx.BusinessKey != "business-42" {
			t.Fatalf("Activity context = %#v", ctx)
		}
		return input, nil
	}, WithActivityHook(ActivityHookFuncs{
		Before: func(_ context.Context, event ActivityHookEvent) { before = event },
		After:  func(_ context.Context, event ActivityHookEvent) { after = event },
	}))
	definition := Definition{
		Name: "write-flow", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 2, MaxRuntimeNodes: 2, MaxProjectionBytes: 4096},
		Templates: []NodeTemplate{{ID: "write", Label: "Write", Type: NodeTypeActivity, Activity: &policy}},
	}
	workflowDefinition, err := NewWorkflowDefinition("WriteWorkflow", "v1", definition, func(ctx *WorkflowContext, input string) (string, error) {
		_, output, err := ExecuteActivity(ctx, activity, "singleton", NodeRef{}, nil, input, "business-42")
		return output, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowDefinition.workflow)
	env.RegisterActivityWithOptions(activity.handler, temporalactivity.RegisterOptions{Name: activity.Name})
	env.ExecuteWorkflow(workflowDefinition.workflow, "payload")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if before.ActivityID == "" || before.IdempotencyKey == "" || after.IdempotencyKey != before.IdempotencyKey || after.Outcome != "completed" {
		t.Fatalf("before=%#v after=%#v", before, after)
	}
}

func TestWriteActivityRetryAfterExternalSuccessUsesSameIdempotencyKey(t *testing.T) {
	policy := ActivityPolicy{
		SideEffect: SideEffectWrite, Retry: RetryPolicy{MaximumAttempts: 2, StartToCloseTimeout: time.Second},
		Idempotency: &IdempotencyPolicy{BusinessKeyRequired: true, PropagationField: "operationId"},
	}
	seen := map[string]bool{}
	attempts, effects := 0, 0
	write := NewActivity("write", policy, func(ctx ActivityContext, input string) (string, error) {
		attempts++
		if !seen[ctx.IdempotencyKey] {
			seen[ctx.IdempotencyKey] = true
			effects++
		}
		if attempts == 1 {
			return "", errors.New("simulated Worker crash after external success")
		}
		return input, nil
	})
	definition := Definition{
		Name: "crash-safe-write", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 1, MaxProjectionBytes: 4096},
		Templates: []NodeTemplate{{ID: "write", Label: "Write", Type: NodeTypeActivity, Activity: &policy}},
	}
	workflowDefinition, err := NewWorkflowDefinition("CrashSafeWorkflow", "v1", definition, func(ctx *WorkflowContext, input string) (string, error) {
		_, output, err := ExecuteActivity(ctx, write, "singleton", NodeRef{}, nil, input, "business-42")
		return output, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowDefinition.workflow)
	env.RegisterActivityWithOptions(write.handler, temporalactivity.RegisterOptions{Name: write.Name})
	env.ExecuteWorkflow(workflowDefinition.workflow, "payload")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || effects != 1 || len(seen) != 1 {
		t.Fatalf("attempts=%d external effects=%d idempotency keys=%d", attempts, effects, len(seen))
	}
}

func TestActivityExecutionRejectsAnyPolicyDriftFromDefinition(t *testing.T) {
	declared := ActivityPolicy{SideEffect: SideEffectRead, Retry: RetryPolicy{MaximumAttempts: 3, StartToCloseTimeout: time.Second}}
	registered := ActivityPolicy{SideEffect: SideEffectRead, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}
	activity := NewActivity("read", registered, func(_ ActivityContext, input string) (string, error) { return input, nil })
	definition := Definition{
		Name: "policy-drift", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 1, MaxProjectionBytes: 4096},
		Templates: []NodeTemplate{{ID: "read", Label: "Read", Type: NodeTypeActivity, Activity: &declared}},
	}
	workflowDefinition, err := NewWorkflowDefinition("PolicyWorkflow", "v1", definition, func(ctx *WorkflowContext, input string) (string, error) {
		_, output, err := ExecuteActivity(ctx, activity, "singleton", NodeRef{}, nil, input, "")
		return output, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowDefinition.workflow)
	env.RegisterActivityWithOptions(activity.handler, temporalactivity.RegisterOptions{Name: activity.Name})
	env.ExecuteWorkflow(workflowDefinition.workflow, "payload")
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), "policy does not match") {
		t.Fatalf("policy drift error = %v", err)
	}
}

func TestParallelActivitiesAreBothScheduledBeforeJoin(t *testing.T) {
	policy := ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: 5 * time.Second}}
	branch := NewActivity("branch", policy, func(_ ActivityContext, input string) (string, error) { return input, nil })
	definition := Definition{
		Name: "parallel", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 4, MaxRuntimeNodes: 4, MaxProjectionBytes: 8192},
		Templates: []NodeTemplate{
			{ID: "branch", Label: "Branch", Type: NodeTypeActivity, Cardinality: CardinalityRepeated, Activity: &policy},
			{ID: "join", Label: "Join", Type: NodeTypeSemantic},
		},
	}
	workflowDefinition, err := NewWorkflowDefinition("ParallelWorkflow", "v1", definition, func(ctx *WorkflowContext, _ string) (string, error) {
		left, err := StartActivity(ctx, branch, "left", NodeRef{}, nil, "left", "")
		if err != nil {
			return "", err
		}
		right, err := StartActivity(ctx, branch, "right", NodeRef{}, nil, "right", "")
		if err != nil {
			return "", err
		}
		leftNode, leftValue, err := left.Get()
		if err != nil {
			return "", err
		}
		rightNode, rightValue, err := right.Get()
		if err != nil {
			return "", err
		}
		if _, err := CompleteSemantic(ctx, "join", "singleton", NodeRef{}, []NodeRef{leftNode, rightNode}); err != nil {
			return "", err
		}
		return leftValue + "+" + rightValue, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowDefinition.workflow)
	env.RegisterActivityWithOptions(branch.handler, temporalactivity.RegisterOptions{Name: branch.Name})
	env.OnActivity(branch.Name, mock.Anything, mock.Anything).Return(func(_ context.Context, input activityInvocation[string]) (string, error) {
		return input.Value, nil
	}).After(2 * time.Second).Twice()
	env.RegisterDelayedCallback(func() {
		value, queryErr := env.QueryWorkflow(ReservedProjectionQuery)
		if queryErr != nil {
			t.Errorf("query parallel projection: %v", queryErr)
			return
		}
		var projection Projection
		if err := value.Get(&projection); err != nil {
			t.Errorf("decode projection: %v", err)
			return
		}
		if len(projection.CurrentNodeIDs) != 2 || projection.Node(projection.CurrentNodeIDs[0]).Status != NodeStatusRunning || projection.Node(projection.CurrentNodeIDs[1]).Status != NodeStatusRunning {
			t.Errorf("parallel projection = %#v", projection)
		}
	}, time.Second)
	env.ExecuteWorkflow(workflowDefinition.workflow, "")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}
