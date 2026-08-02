package worker

const (
	WorkflowName      = "MyWorkflow"
	processActivityID = "process"
)

type Input struct {
	Value string `json:"value"`
}

type Result struct {
	Value string `json:"value"`
}
