package orgsdk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	ReservedProjectionQuery = "org.sdk/projection"
	ReservedActionSignal    = "org.sdk/action"
)

var (
	ErrActionRejected = errors.New("action rejected")
	ErrActionTimedOut = errors.New("action timed out")
	ErrInvalidRoute   = errors.New("invalid route")
)

type NodeRef struct {
	ID string
}

type WorkflowContext struct {
	temporal            workflow.Context
	graph               *Graph
	processedOperations map[string]ActionResult
}

type WorkflowDefinition[I, O any] struct {
	Name     string
	Version  string
	Contract Definition
	Run      func(*WorkflowContext, I) (O, error)
	workflow func(workflow.Context, I) (O, error)
}

func NewWorkflowDefinition[I, O any](name, version string, contract Definition, run func(*WorkflowContext, I) (O, error)) (WorkflowDefinition[I, O], error) {
	if name == "" || version == "" || run == nil {
		return WorkflowDefinition[I, O]{}, errors.New("workflow name, version, and run function are required")
	}
	if err := contract.Validate(); err != nil {
		return WorkflowDefinition[I, O]{}, err
	}
	definition := WorkflowDefinition[I, O]{Name: name, Version: version, Contract: contract, Run: run}
	definition.workflow = func(ctx workflow.Context, input I) (O, error) {
		var zero O
		graph, err := NewGraph(contract, name, version, workflow.Now(ctx))
		if err != nil {
			return zero, err
		}
		runtimeContext := &WorkflowContext{temporal: ctx, graph: graph, processedOperations: map[string]ActionResult{}}
		if err := workflow.SetQueryHandler(ctx, ReservedProjectionQuery, func() (Projection, error) {
			return graph.Snapshot(), nil
		}); err != nil {
			return zero, err
		}
		output, runErr := run(runtimeContext, input)
		if runErr != nil {
			_ = graph.Fail("workflow-error", workflow.Now(ctx))
			return output, runErr
		}
		if err := graph.Complete(workflow.Now(ctx)); err != nil {
			return zero, err
		}
		return output, nil
	}
	return definition, nil
}

type ActivityContext struct {
	Context        context.Context
	ActivityID     string
	WorkflowID     string
	BusinessKey    string
	IdempotencyKey string
	Attempt        int32
}

type ActivityHookEvent struct {
	ActivityID     string
	WorkflowID     string
	BusinessKey    string
	IdempotencyKey string
	Attempt        int32
	Outcome        string
}

type ActivityHook interface {
	BeforeActivity(context.Context, ActivityHookEvent)
	AfterActivity(context.Context, ActivityHookEvent)
}

type ActivityHookFuncs struct {
	Before func(context.Context, ActivityHookEvent)
	After  func(context.Context, ActivityHookEvent)
}

func (f ActivityHookFuncs) BeforeActivity(ctx context.Context, event ActivityHookEvent) {
	if f.Before != nil {
		f.Before(ctx, event)
	}
}

func (f ActivityHookFuncs) AfterActivity(ctx context.Context, event ActivityHookEvent) {
	if f.After != nil {
		f.After(ctx, event)
	}
}

type activityOptions struct {
	hook ActivityHook
}

type ActivityOption func(*activityOptions)

func WithActivityHook(hook ActivityHook) ActivityOption {
	return func(options *activityOptions) { options.hook = hook }
}

type activityInvocation[I any] struct {
	Value       I
	BusinessKey string
}

type ActivityDefinition[I, O any] struct {
	Name         string
	Policy       ActivityPolicy
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	handler      func(context.Context, activityInvocation[I]) (O, error)
}

func NewActivity[I, O any](name string, policy ActivityPolicy, handler func(ActivityContext, I) (O, error), options ...ActivityOption) ActivityDefinition[I, O] {
	config := activityOptions{}
	for _, option := range options {
		option(&config)
	}
	definition := ActivityDefinition[I, O]{Name: name, Policy: policy, InputSchema: schemaFor[I](), OutputSchema: schemaFor[O]()}
	definition.handler = func(ctx context.Context, invocation activityInvocation[I]) (output O, err error) {
		info := activity.GetInfo(ctx)
		idempotencyKey := ""
		if policy.SideEffect == SideEffectWrite {
			idempotencyKey = stableIdempotencyKey(info.WorkflowExecution.ID, info.ActivityID, invocation.BusinessKey)
		}
		activityContext := ActivityContext{
			Context: ctx, ActivityID: info.ActivityID, WorkflowID: info.WorkflowExecution.ID,
			BusinessKey: invocation.BusinessKey, IdempotencyKey: idempotencyKey, Attempt: info.Attempt,
		}
		event := ActivityHookEvent{
			ActivityID: info.ActivityID, WorkflowID: info.WorkflowExecution.ID, BusinessKey: invocation.BusinessKey,
			IdempotencyKey: idempotencyKey, Attempt: info.Attempt, Outcome: "started",
		}
		if config.hook != nil {
			config.hook.BeforeActivity(ctx, event)
			defer func() {
				if err != nil {
					event.Outcome = "failed"
				} else {
					event.Outcome = "completed"
				}
				config.hook.AfterActivity(ctx, event)
			}()
		}
		return handler(activityContext, invocation.Value)
	}
	return definition
}

func ActivityNode[I, O any](activity ActivityDefinition[I, O], label string, cardinality Cardinality) NodeTemplate {
	return NodeTemplate{
		ID: activity.Name, Label: label, Type: NodeTypeActivity, Cardinality: cardinality,
		InputSchema: append(json.RawMessage(nil), activity.InputSchema...), OutputSchema: append(json.RawMessage(nil), activity.OutputSchema...),
		Activity: cloneActivityPolicy(activity.Policy),
	}
}

func cloneActivityPolicy(policy ActivityPolicy) *ActivityPolicy {
	cloned := policy
	if policy.Idempotency != nil {
		idempotency := *policy.Idempotency
		cloned.Idempotency = &idempotency
	}
	return &cloned
}

func stableIdempotencyKey(workflowID, activityID, businessKey string) string {
	sum := sha256.Sum256([]byte("org-activity-v1\x00" + workflowID + "\x00" + activityID + "\x00" + businessKey))
	return hex.EncodeToString(sum[:])
}

type ActivityFuture[O any] struct {
	ctx    *WorkflowContext
	node   NodeRef
	future workflow.Future
}

func StartActivity[I, O any](ctx *WorkflowContext, definition ActivityDefinition[I, O], occurrenceKey string, parent NodeRef, dependencies []NodeRef, input I, businessKey string) (ActivityFuture[O], error) {
	template, ok := ctx.graph.Definition.template(definition.Name)
	if !ok || template.Type != NodeTypeActivity || template.Activity == nil {
		return ActivityFuture[O]{}, fmt.Errorf("activity %q is not declared by the Workflow Definition", definition.Name)
	}
	if !activityPoliciesEqual(*template.Activity, definition.Policy) {
		return ActivityFuture[O]{}, fmt.Errorf("activity %q policy does not match the Workflow Definition", definition.Name)
	}
	if definition.Policy.SideEffect == SideEffectWrite && definition.Policy.Idempotency != nil && definition.Policy.Idempotency.BusinessKeyRequired && businessKey == "" {
		return ActivityFuture[O]{}, errors.New("write Activity business key is required")
	}
	nodeID, err := ctx.graph.CreateNode(definition.Name, parent.ID, occurrenceKey, dependencyIDs(dependencies), workflow.Now(ctx.temporal))
	if err != nil {
		return ActivityFuture[O]{}, err
	}
	if err := ctx.graph.Transition(nodeID, NodeStatusRunning, "", workflow.Now(ctx.temporal)); err != nil {
		return ActivityFuture[O]{}, err
	}
	retry := definition.Policy.Retry
	activityOptions := workflow.ActivityOptions{
		ActivityID:          StableActivityID(nodeID),
		StartToCloseTimeout: retry.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retry.InitialInterval,
			BackoffCoefficient: retry.BackoffCoefficient,
			MaximumInterval:    retry.MaximumInterval,
			MaximumAttempts:    retry.MaximumAttempts,
		},
	}
	activityContext := workflow.WithActivityOptions(ctx.temporal, activityOptions)
	future := workflow.ExecuteActivity(activityContext, definition.Name, activityInvocation[I]{Value: input, BusinessKey: businessKey})
	return ActivityFuture[O]{ctx: ctx, node: NodeRef{ID: nodeID}, future: future}, nil
}

func activityPoliciesEqual(left, right ActivityPolicy) bool {
	if left.SideEffect != right.SideEffect || left.Retry != right.Retry || left.Reconciliation != right.Reconciliation || left.Compensation != right.Compensation {
		return false
	}
	if left.Idempotency == nil || right.Idempotency == nil {
		return left.Idempotency == nil && right.Idempotency == nil
	}
	return *left.Idempotency == *right.Idempotency
}

func (f ActivityFuture[O]) Get() (NodeRef, O, error) {
	var output O
	err := f.future.Get(f.ctx.temporal, &output)
	now := workflow.Now(f.ctx.temporal)
	if err != nil {
		_ = f.ctx.graph.Transition(f.node.ID, NodeStatusFailed, "activity-failed", now)
		return f.node, output, err
	}
	if err := f.ctx.graph.Transition(f.node.ID, NodeStatusCompleted, "", now); err != nil {
		return f.node, output, err
	}
	return f.node, output, nil
}

func ExecuteActivity[I, O any](ctx *WorkflowContext, definition ActivityDefinition[I, O], occurrenceKey string, parent NodeRef, dependencies []NodeRef, input I, businessKey string) (NodeRef, O, error) {
	future, err := StartActivity(ctx, definition, occurrenceKey, parent, dependencies, input, businessKey)
	if err != nil {
		var zero O
		return NodeRef{}, zero, err
	}
	return future.Get()
}

func SkipNode(ctx *WorkflowContext, templateID, occurrenceKey string, parent NodeRef, dependencies []NodeRef, reason string) (NodeRef, error) {
	id, err := ctx.graph.CreateNode(templateID, parent.ID, occurrenceKey, dependencyIDs(dependencies), workflow.Now(ctx.temporal))
	if err != nil {
		return NodeRef{}, err
	}
	if err := ctx.graph.Skip(id, reason, workflow.Now(ctx.temporal)); err != nil {
		return NodeRef{}, err
	}
	return NodeRef{ID: id}, nil
}

func CompleteSemantic(ctx *WorkflowContext, templateID, occurrenceKey string, parent NodeRef, dependencies []NodeRef) (NodeRef, error) {
	id, err := ctx.graph.CreateNode(templateID, parent.ID, occurrenceKey, dependencyIDs(dependencies), workflow.Now(ctx.temporal))
	if err != nil {
		return NodeRef{}, err
	}
	if err := ctx.graph.Transition(id, NodeStatusRunning, "", workflow.Now(ctx.temporal)); err != nil {
		return NodeRef{}, err
	}
	if err := ctx.graph.Transition(id, NodeStatusCompleted, "", workflow.Now(ctx.temporal)); err != nil {
		return NodeRef{}, err
	}
	return NodeRef{ID: id}, nil
}

type ActionEnvelope struct {
	OperationID string          `json:"operationId"`
	NodeID      string          `json:"nodeId"`
	Action      string          `json:"action"`
	Input       json.RawMessage `json:"input,omitempty"`
}

type ActionResult struct {
	OperationID string          `json:"operationId"`
	Action      string          `json:"action"`
	Input       json.RawMessage `json:"input,omitempty"`
	Node        NodeRef         `json:"node"`
}

func AwaitConfirmation(ctx *WorkflowContext, templateID, occurrenceKey string, dependencies []NodeRef, timeout time.Duration) (ActionResult, error) {
	return waitForAction(ctx, templateID, occurrenceKey, dependencies, timeout, func(json.RawMessage) error { return nil })
}

func WaitForAction[I any](ctx *WorkflowContext, templateID, occurrenceKey string, dependencies []NodeRef, timeout time.Duration) (I, ActionResult, error) {
	var decoded I
	result, err := waitForAction(ctx, templateID, occurrenceKey, dependencies, timeout, func(input json.RawMessage) error {
		return json.Unmarshal(input, &decoded)
	})
	return decoded, result, err
}

func waitForAction(ctx *WorkflowContext, templateID, occurrenceKey string, dependencies []NodeRef, timeout time.Duration, decode func(json.RawMessage) error) (ActionResult, error) {
	if timeout <= 0 {
		return ActionResult{}, errors.New("WaitForAction requires a finite positive timeout")
	}
	parent := NodeRef{}
	if len(dependencies) > 0 {
		parent = dependencies[0]
	}
	nodeID, err := ctx.graph.CreateNode(templateID, parent.ID, occurrenceKey, dependencyIDs(dependencies), workflow.Now(ctx.temporal))
	if err != nil {
		return ActionResult{}, err
	}
	if err := ctx.graph.Transition(nodeID, NodeStatusWaitingForUser, "waiting-for-action", workflow.Now(ctx.temporal)); err != nil {
		return ActionResult{}, err
	}
	template, _ := ctx.graph.Definition.template(templateID)
	signal := workflow.GetSignalChannel(ctx.temporal, ReservedActionSignal)
	timer := workflow.NewTimer(ctx.temporal, timeout)
	for {
		var envelope ActionEnvelope
		timedOut := false
		canceled := false
		selector := workflow.NewSelector(ctx.temporal)
		selector.AddReceive(signal, func(channel workflow.ReceiveChannel, _ bool) {
			channel.Receive(ctx.temporal, &envelope)
		})
		selector.AddFuture(timer, func(workflow.Future) { timedOut = true })
		selector.AddReceive(ctx.temporal.Done(), func(workflow.ReceiveChannel, bool) { canceled = true })
		selector.Select(ctx.temporal)
		if canceled {
			_ = ctx.graph.Transition(nodeID, NodeStatusCanceled, "workflow-canceled", workflow.Now(ctx.temporal))
			return ActionResult{}, workflow.ErrCanceled
		}
		if timedOut {
			_ = ctx.graph.Transition(nodeID, NodeStatusTimedOut, "action-timeout", workflow.Now(ctx.temporal))
			return ActionResult{}, ErrActionTimedOut
		}
		if envelope.OperationID == "" || envelope.NodeID != nodeID || !declaresAction(template, envelope.Action) {
			continue
		}
		if _, duplicate := ctx.processedOperations[envelope.OperationID]; duplicate {
			if err := ctx.graph.RecordActionOutcome(ActionOutcome{OperationID: envelope.OperationID, RuntimeNodeID: nodeID, Action: envelope.Action, State: ActionOutcomeDuplicate}, workflow.Now(ctx.temporal)); err != nil {
				return ActionResult{}, err
			}
			continue
		}
		result := ActionResult{OperationID: envelope.OperationID, Action: envelope.Action, Input: envelope.Input, Node: NodeRef{ID: nodeID}}
		ctx.processedOperations[envelope.OperationID] = result
		if err := decode(envelope.Input); err != nil {
			if err := ctx.graph.RecordActionOutcome(ActionOutcome{OperationID: envelope.OperationID, RuntimeNodeID: nodeID, Action: envelope.Action, State: ActionOutcomeRejected, ReasonCode: "invalid-input"}, workflow.Now(ctx.temporal)); err != nil {
				return ActionResult{}, err
			}
			continue
		}
		if err := ctx.graph.RecordActionOutcome(ActionOutcome{OperationID: envelope.OperationID, RuntimeNodeID: nodeID, Action: envelope.Action, State: ActionOutcomeAccepted}, workflow.Now(ctx.temporal)); err != nil {
			return ActionResult{}, err
		}
		if err := ctx.graph.Transition(nodeID, NodeStatusCompleted, "", workflow.Now(ctx.temporal)); err != nil {
			return ActionResult{}, err
		}
		return result, nil
	}
}

func declaresAction(template NodeTemplate, name string) bool {
	for _, action := range template.Actions {
		if action.Name == name {
			return true
		}
	}
	return false
}

func dependencyIDs(references []NodeRef) []string {
	ids := make([]string, 0, len(references))
	for _, reference := range references {
		if reference.ID != "" {
			ids = append(ids, reference.ID)
		}
	}
	return ids
}
