package hello

const (
	WorkerName                = "hello-worker"
	WorkflowName              = "HelloWorkflow"
	prepareGreetingActivityID = "prepare-greeting"
	composeGreetingActivityID = "compose-greeting"
)

type GreetingInput struct {
	Name string `json:"name"`
}

type GreetingContext struct {
	Name string `json:"name"`
}

type GreetingResult struct {
	Message        string `json:"message"`
	WorkerVersion  string `json:"workerVersion"`
	IdempotencyKey string `json:"idempotencyKey"`
}
