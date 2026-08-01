package dynamicdecision

const (
	WorkerName   = "dynamic-decision-worker"
	WorkflowName = "DynamicDecisionWorkflow"
)

type Input struct {
	Mode    string `json:"mode"`
	Subject string `json:"subject"`
}

type Route struct {
	Name    string `json:"name"`
	Subject string `json:"subject"`
}

type BranchInput struct {
	Subject string `json:"subject"`
}

type BranchResult struct {
	Route   string `json:"route"`
	Content string `json:"content"`
}

type FinalizeInput struct {
	Subject       string       `json:"subject"`
	Selected      BranchResult `json:"selected"`
	WorkerVersion string       `json:"workerVersion"`
}

type Result struct {
	Subject       string `json:"subject"`
	Route         string `json:"route"`
	Content       string `json:"content"`
	WorkerVersion string `json:"workerVersion"`
}
