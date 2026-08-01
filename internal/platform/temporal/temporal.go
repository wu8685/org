package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
	"github.com/wu8685/org/sdk/orgsdk"
)

type Config struct {
	Address, Namespace        string
	PollTimeout, PollInterval time.Duration
}
type Client struct {
	sdk client.Client
	cfg Config
}

func Dial(cfg Config) (*Client, error) {
	if cfg.Address == "" {
		cfg.Address = client.DefaultHostPort
	}
	if cfg.Namespace == "" {
		cfg.Namespace = client.DefaultNamespace
	}
	sdk, err := client.Dial(client.Options{HostPort: cfg.Address, Namespace: cfg.Namespace})
	if err != nil {
		return nil, err
	}
	return &Client{sdk: sdk, cfg: cfg}, nil
}
func (c *Client) Close() { c.sdk.Close() }

func (c *Client) WaitForPoller(ctx context.Context, d domain.WorkerVersion) error {
	timeout := c.cfg.PollTimeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	interval := c.cfg.PollInterval
	if interval == 0 {
		interval = time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	handle := c.sdk.WorkerDeploymentClient().GetHandle(d.WorkerDeployment)
	for {
		description, err := handle.DescribeVersion(waitCtx, client.WorkerDeploymentDescribeVersionOptions{BuildID: d.Version})
		if err == nil && hasWorkflowPoller(description.Info.TaskQueuesInfos, d.TaskQueue) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("Worker version %s did not poll Task Queue %s: %w", d.Version, d.TaskQueue, waitCtx.Err())
		case <-time.After(interval):
		}
	}
}

func hasWorkflowPoller(queues []client.WorkerDeploymentTaskQueueInfo, taskQueue string) bool {
	for _, queue := range queues {
		if queue.Name == taskQueue && queue.Type == client.TaskQueueTypeWorkflow {
			return true
		}
	}
	return false
}

func (c *Client) Probe(ctx context.Context, version domain.WorkerVersion) (service.RuntimeIdentity, error) {
	probe, err := runContractProbe(ctx, c.sdk, version)
	if err != nil {
		return service.RuntimeIdentity{}, err
	}
	return service.RuntimeIdentity{
		ManifestDigest: probe.ManifestDigest, SDKModuleVersion: probe.SDKModuleVersion,
		RuntimeProtocolVersion: probe.RuntimeProtocolVersion, WorkerBuildID: probe.WorkerBuildID,
	}, nil
}

type contractProbeClient interface {
	ExecuteWorkflow(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)
	GetWorkflow(context.Context, string, string) client.WorkflowRun
}

func runContractProbe(ctx context.Context, sdk contractProbeClient, version domain.WorkerVersion) (orgsdk.ContractProbe, error) {
	options := buildProbeStartOptions(version)
	run, err := sdk.ExecuteWorkflow(ctx, options, orgsdk.ReservedContractProbeWorkflow)
	if err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if !errors.As(err, &alreadyStarted) {
			return orgsdk.ContractProbe{}, err
		}
		run = sdk.GetWorkflow(ctx, options.ID, alreadyStarted.RunId)
	}
	var probe orgsdk.ContractProbe
	if err := run.Get(ctx, &probe); err != nil {
		return orgsdk.ContractProbe{}, err
	}
	return probe, nil
}

func buildProbeStartOptions(version domain.WorkerVersion) client.StartWorkflowOptions {
	attemptID := version.PromotionAttemptID
	if attemptID == "" {
		attemptID = "direct"
	}
	return client.StartWorkflowOptions{
		ID: "org-sdk-probe-" + version.ID + "-" + attemptID, TaskQueue: version.TaskQueue,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
		VersioningOverride: &client.PinnedVersioningOverride{Version: worker.WorkerDeploymentVersion{
			DeploymentName: version.WorkerDeployment, BuildID: version.Version,
		}},
	}
}

func (c *Client) SetCurrent(ctx context.Context, d domain.WorkerVersion) error {
	_, err := c.sdk.WorkerDeploymentClient().GetHandle(d.WorkerDeployment).SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{BuildID: d.Version})
	return err
}

func (c *Client) Start(ctx context.Context, start service.ExecutionStart) (string, error) {
	return runWorkflowStart(ctx, c.sdk, start)
}

func runWorkflowStart(ctx context.Context, sdk contractProbeClient, start service.ExecutionStart) (string, error) {
	input, err := decodeInput(start.Input)
	if err != nil {
		return "", err
	}
	options := buildStartOptions(start)
	run, err := sdk.ExecuteWorkflow(ctx, options, start.Workflow, input)
	if err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if !errors.As(err, &alreadyStarted) || alreadyStarted.RunId == "" {
			return "", err
		}
		run = sdk.GetWorkflow(ctx, options.ID, alreadyStarted.RunId)
	}
	return run.GetRunID(), nil
}

func buildStartOptions(start service.ExecutionStart) client.StartWorkflowOptions {
	opts := client.StartWorkflowOptions{ID: start.WorkflowID, TaskQueue: start.TaskQueue, WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE, WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL, WorkflowExecutionErrorWhenAlreadyStarted: true}
	if start.PinnedVersion != "" {
		opts.VersioningOverride = &client.PinnedVersioningOverride{Version: worker.WorkerDeploymentVersion{DeploymentName: start.DeploymentName, BuildID: start.PinnedVersion}}
	}
	return opts
}

func (c *Client) Describe(ctx context.Context, invocation domain.Invocation) (service.ExecutionState, error) {
	description, err := c.sdk.DescribeWorkflowExecution(ctx, invocation.TemporalWorkflowID, invocation.TemporalRunID)
	if err != nil {
		return service.ExecutionState{}, err
	}
	state := service.ExecutionState{Status: semanticStatus(description.WorkflowExecutionInfo.Status)}
	if description.WorkflowExecutionInfo.Status == enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		var result any
		if err := c.sdk.GetWorkflow(ctx, invocation.TemporalWorkflowID, invocation.TemporalRunID).Get(ctx, &result); err != nil {
			return state, err
		}
		b, err := json.Marshal(result)
		if err != nil {
			return state, err
		}
		state.Result = string(b)
	}
	return state, nil
}

func (c *Client) Query(ctx context.Context, invocation domain.Invocation, query string, input []byte) ([]byte, error) {
	var encoded converter.EncodedValue
	var err error
	if len(input) == 0 {
		encoded, err = c.sdk.QueryWorkflow(ctx, invocation.TemporalWorkflowID, invocation.TemporalRunID, query)
	} else {
		arg, decodeErr := decodeInput(input)
		if decodeErr != nil {
			return nil, decodeErr
		}
		encoded, err = c.sdk.QueryWorkflow(ctx, invocation.TemporalWorkflowID, invocation.TemporalRunID, query, arg)
	}
	if err != nil {
		return nil, err
	}
	var value any
	if err := encoded.Get(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (c *Client) Signal(ctx context.Context, invocation domain.Invocation, signal string, input []byte) error {
	arg, err := decodeInput(input)
	if err != nil {
		return err
	}
	return c.sdk.SignalWorkflow(ctx, invocation.TemporalWorkflowID, invocation.TemporalRunID, signal, arg)
}
func (c *Client) Cancel(ctx context.Context, invocation domain.Invocation) error {
	return c.sdk.CancelWorkflow(ctx, invocation.TemporalWorkflowID, invocation.TemporalRunID)
}

func decodeInput(input []byte) (any, error) {
	if len(input) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return nil, errors.New("input must be valid JSON")
	}
	return value, nil
}
func semanticStatus(status enumspb.WorkflowExecutionStatus) string {
	return strings.ToLower(strings.TrimPrefix(status.String(), "WORKFLOW_EXECUTION_STATUS_"))
}
