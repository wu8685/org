package orgsdk

import (
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type TestEnvironment struct {
	env *testsuite.TestWorkflowEnvironment
}

func NewTestEnvironment() *TestEnvironment {
	var suite testsuite.WorkflowTestSuite
	return &TestEnvironment{env: suite.NewTestWorkflowEnvironment()}
}

func (e *TestEnvironment) Register(registrations ...Registration) error {
	return registerAll(testRegistrationSink{env: e.env}, registrations)
}

func (e *TestEnvironment) ExecuteWorkflow(name string, input any) {
	e.env.ExecuteWorkflow(name, input)
}

func (e *TestEnvironment) WorkflowError() error {
	return e.env.GetWorkflowError()
}

func (e *TestEnvironment) Result(output any) error {
	return e.env.GetWorkflowResult(output)
}

func (e *TestEnvironment) Projection() (Projection, error) {
	value, err := e.env.QueryWorkflow(ReservedProjectionQuery)
	if err != nil {
		return Projection{}, err
	}
	var projection Projection
	if err := value.Get(&projection); err != nil {
		return Projection{}, err
	}
	return projection, nil
}

func (e *TestEnvironment) SignalAction(envelope ActionEnvelope) {
	e.env.SignalWorkflow(ReservedActionSignal, envelope)
}

func (e *TestEnvironment) After(delay time.Duration, callback func()) {
	e.env.RegisterDelayedCallback(callback, delay)
}

type testRegistrationSink struct {
	env *testsuite.TestWorkflowEnvironment
}

func (s testRegistrationSink) registerActivity(name string, handler any) error {
	s.env.RegisterActivityWithOptions(handler, activity.RegisterOptions{Name: name})
	return nil
}

func (s testRegistrationSink) registerWorkflow(name string, handler any) error {
	s.env.RegisterWorkflowWithOptions(handler, workflow.RegisterOptions{Name: name, VersioningBehavior: workflow.VersioningBehaviorPinned})
	return nil
}
