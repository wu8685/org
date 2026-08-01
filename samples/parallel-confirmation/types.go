package parallelconfirmation

const (
	WorkerName   = "parallel-confirmation-worker"
	WorkflowName = "ParallelConfirmationWorkflow"
)

type Input struct {
	Subject string `json:"subject"`
}

type Branch struct {
	Key  string `json:"key"`
	Task string `json:"task"`
}

type Plan struct {
	Subject  string   `json:"subject"`
	Branches []Branch `json:"branches"`
}

type BranchResult struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
}

type FinalizeInput struct {
	Subject       string         `json:"subject"`
	Branches      []BranchResult `json:"branches"`
	WorkerVersion string         `json:"workerVersion"`
}

type Result struct {
	Subject       string         `json:"subject"`
	Branches      []BranchResult `json:"branches"`
	WorkerVersion string         `json:"workerVersion"`
}
