package orgsdk

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const ReservedContractProbeWorkflow = "org.sdk/contract-probe"

var manifestDigestPattern = regexp.MustCompile("^sha256:[a-f0-9]{64}$")

type WorkerConfig struct {
	TemporalAddress   string
	TemporalNamespace string
	TaskQueue         string
	DeploymentName    string
	BuildID           string
	ManifestDigest    string
}

func (c WorkerConfig) Validate() error {
	var missing []string
	if strings.TrimSpace(c.TemporalAddress) == "" {
		missing = append(missing, "Temporal address")
	}
	if strings.TrimSpace(c.TemporalNamespace) == "" {
		missing = append(missing, "platform Temporal Namespace")
	}
	if strings.TrimSpace(c.TaskQueue) == "" {
		missing = append(missing, "Task Queue")
	}
	if strings.TrimSpace(c.DeploymentName) == "" {
		missing = append(missing, "Worker Deployment")
	}
	if strings.TrimSpace(c.BuildID) == "" {
		missing = append(missing, "Worker Build ID")
	}
	if !manifestDigestPattern.MatchString(c.ManifestDigest) {
		missing = append(missing, "manifest digest")
	}
	if len(missing) > 0 {
		return errors.New(strings.Join(missing, ", ") + " are required")
	}
	return nil
}

type registrationSink interface {
	registerActivity(string, any) error
	registerWorkflow(string, any) error
}

type Registration interface {
	register(registrationSink) error
}

func (a ActivityDefinition[I, O]) register(sink registrationSink) error {
	return sink.registerActivity(a.Name, a.handler)
}

func (d WorkflowDefinition[I, O]) register(sink registrationSink) error {
	return sink.registerWorkflow(d.Name, d.workflow)
}

func registerAll(sink registrationSink, registrations []Registration) error {
	seen := map[string]bool{}
	for _, registration := range registrations {
		name := registrationName(registration)
		if name == "" {
			return errors.New("unsupported SDK registration")
		}
		if seen[name] {
			return errors.New("duplicate SDK registration " + name)
		}
		seen[name] = true
		if err := registration.register(sink); err != nil {
			return err
		}
	}
	return nil
}

func registrationName(registration Registration) string {
	switch value := registration.(type) {
	case interface{ registrationName() string }:
		return value.registrationName()
	default:
		return ""
	}
}

func (a ActivityDefinition[I, O]) registrationName() string { return "activity:" + a.Name }
func (d WorkflowDefinition[I, O]) registrationName() string { return "workflow:" + d.Name }

type temporalRegistrationSink struct {
	worker worker.Worker
}

func (s temporalRegistrationSink) registerActivity(name string, handler any) error {
	s.worker.RegisterActivityWithOptions(handler, activity.RegisterOptions{Name: name})
	return nil
}

func (s temporalRegistrationSink) registerWorkflow(name string, handler any) error {
	s.worker.RegisterWorkflowWithOptions(handler, workflow.RegisterOptions{Name: name, VersioningBehavior: workflow.VersioningBehaviorPinned})
	return nil
}

type ContractProbe struct {
	ManifestDigest         string `json:"manifestDigest"`
	SDKModuleVersion       string `json:"sdkModuleVersion"`
	RuntimeProtocolVersion string `json:"runtimeProtocolVersion"`
	WorkerBuildID          string `json:"workerBuildId"`
}

type WorkerRuntime struct {
	client client.Client
	worker worker.Worker
}

func NewWorkerRuntime(config WorkerConfig, registrations ...Registration) (*WorkerRuntime, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	temporalClient, err := client.Dial(client.Options{HostPort: config.TemporalAddress, Namespace: config.TemporalNamespace})
	if err != nil {
		return nil, err
	}
	temporalWorker := worker.New(temporalClient, config.TaskQueue, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version: worker.WorkerDeploymentVersion{
				DeploymentName: config.DeploymentName,
				BuildID:        config.BuildID,
			},
		},
	})
	if err := registerAll(temporalRegistrationSink{worker: temporalWorker}, registrations); err != nil {
		temporalClient.Close()
		return nil, err
	}
	probe := ContractProbe{
		ManifestDigest: config.ManifestDigest, SDKModuleVersion: SDKModuleVersion,
		RuntimeProtocolVersion: RuntimeProtocolVersion, WorkerBuildID: config.BuildID,
	}
	temporalWorker.RegisterWorkflowWithOptions(func(workflow.Context) (ContractProbe, error) {
		return probe, nil
	}, workflow.RegisterOptions{Name: ReservedContractProbeWorkflow, VersioningBehavior: workflow.VersioningBehaviorPinned})
	return &WorkerRuntime{client: temporalClient, worker: temporalWorker}, nil
}

func (r *WorkerRuntime) Run(ctx context.Context) error {
	if err := r.worker.Start(); err != nil {
		r.client.Close()
		return err
	}
	<-ctx.Done()
	r.worker.Stop()
	r.client.Close()
	return nil
}

func (r *WorkerRuntime) Close() {
	r.worker.Stop()
	r.client.Close()
}
