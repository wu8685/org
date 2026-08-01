package orgsdk

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type NodeStatus string

const (
	NodeStatusPending        NodeStatus = "pending"
	NodeStatusRunning        NodeStatus = "running"
	NodeStatusWaitingForUser NodeStatus = "waiting-for-user"
	NodeStatusCompleted      NodeStatus = "completed"
	NodeStatusFailed         NodeStatus = "failed"
	NodeStatusCanceled       NodeStatus = "canceled"
	NodeStatusSkipped        NodeStatus = "skipped"
	NodeStatusTimedOut       NodeStatus = "timed-out"
)

type EventType string

const (
	EventGraphInitialized  EventType = "graph-initialized"
	EventNodeCreated       EventType = "node-created"
	EventDependencyAdded   EventType = "dependency-added"
	EventNodeStatusChanged EventType = "node-status-changed"
	EventNodeSkipped       EventType = "node-skipped"
	EventActionOffered     EventType = "action-offered"
	EventActionWithdrawn   EventType = "action-withdrawn"
	EventActionOutcome     EventType = "action-outcome-recorded"
	EventGraphCompleted    EventType = "graph-completed"
	EventGraphFailed       EventType = "graph-failed"
)

type ProjectionEvent struct {
	Sequence                uint64     `json:"sequence"`
	Type                    EventType  `json:"type"`
	RuntimeNodeID           string     `json:"runtimeNodeId,omitempty"`
	TemplateID              string     `json:"templateId,omitempty"`
	DependencyRuntimeNodeID string     `json:"dependencyRuntimeNodeId,omitempty"`
	FromStatus              NodeStatus `json:"fromStatus,omitempty"`
	ToStatus                NodeStatus `json:"toStatus,omitempty"`
	ReasonCode              string     `json:"reasonCode,omitempty"`
	ActionName              string     `json:"actionName,omitempty"`
	Timestamp               time.Time  `json:"timestamp"`
}

type NodeProjection struct {
	RuntimeNodeID string     `json:"runtimeNodeId"`
	TemplateID    string     `json:"templateId"`
	Label         string     `json:"label"`
	Dependencies  []string   `json:"dependencies"`
	Status        NodeStatus `json:"status"`
	ReasonCode    string     `json:"reasonCode,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	StartedAt     time.Time  `json:"startedAt,omitempty"`
	CompletedAt   time.Time  `json:"completedAt,omitempty"`
}

type Projection struct {
	ContractVersion string            `json:"contractVersion"`
	WorkflowName    string            `json:"workflowName"`
	WorkerVersion   string            `json:"workerVersion"`
	Revision        uint64            `json:"projectionRevision"`
	Status          string            `json:"runStatus"`
	Nodes           []NodeProjection  `json:"nodes"`
	CurrentNodeIDs  []string          `json:"currentNodeIds"`
	AllowedActions  []AllowedAction   `json:"allowedActions"`
	ActionOutcomes  []ActionOutcome   `json:"actionOutcomes,omitempty"`
	RecentEvents    []ProjectionEvent `json:"recentEvents,omitempty"`
}

type AllowedAction struct {
	RuntimeNodeID string `json:"runtimeNodeId"`
	Name          string `json:"name"`
	Label         string `json:"label"`
}

type ActionOutcomeState string

const (
	ActionOutcomeAccepted  ActionOutcomeState = "accepted"
	ActionOutcomeRejected  ActionOutcomeState = "rejected"
	ActionOutcomeDuplicate ActionOutcomeState = "duplicate"
	ActionOutcomeExpired   ActionOutcomeState = "expired"
)

type ActionOutcome struct {
	OperationID   string             `json:"operationId"`
	RuntimeNodeID string             `json:"runtimeNodeId"`
	Action        string             `json:"action"`
	State         ActionOutcomeState `json:"state"`
	ReasonCode    string             `json:"reasonCode,omitempty"`
}

func (p Projection) Node(id string) NodeProjection {
	for _, node := range p.Nodes {
		if node.RuntimeNodeID == id {
			return node
		}
	}
	return NodeProjection{}
}

type Graph struct {
	Definition       Definition
	WorkflowName     string
	WorkerVersion    string
	Nodes            []NodeProjection
	ProjectionEvents []ProjectionEvent
	ActionRecords    []ActionOutcome
	terminalStatus   string
}

func NewGraph(definition Definition, workflowName, workerVersion string, now time.Time) (*Graph, error) {
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	if workflowName == "" || workerVersion == "" {
		return nil, errors.New("workflow name and worker version are required")
	}
	graph := &Graph{Definition: definition, WorkflowName: workflowName, WorkerVersion: workerVersion}
	graph.append(ProjectionEvent{Type: EventGraphInitialized, Timestamp: now})
	if !graph.withinProjectionBound() {
		return nil, errors.New("maximum projection bytes exceeded")
	}
	return graph, nil
}

func RuntimeNodeID(workflowContractID, parentRuntimeNodeID, templateID, occurrenceKey string) string {
	material := workflowContractID + "\x00" + parentRuntimeNodeID + "\x00" + templateID + "\x00" + occurrenceKey
	sum := sha256.Sum256([]byte(material))
	hash := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10]))
	prefix := strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(templateID)
	return prefix + "-" + hash
}

func StableActivityID(runtimeNodeID string) string { return "org/activity/" + runtimeNodeID }

func (g *Graph) CreateNode(templateID, parentRuntimeNodeID, occurrenceKey string, dependencies []string, now time.Time) (string, error) {
	template, ok := g.Definition.template(templateID)
	if !ok {
		return "", fmt.Errorf("unknown template %q", templateID)
	}
	if occurrenceKey == "" {
		return "", errors.New("stable occurrence key is required")
	}
	if template.Cardinality != CardinalityRepeated && occurrenceKey != "singleton" {
		return "", fmt.Errorf("singleton template %q requires singleton occurrence key", templateID)
	}
	if len(g.Nodes) >= g.Definition.Bounds.MaxRuntimeNodes {
		return "", errors.New("maximum runtime nodes exceeded")
	}
	id := RuntimeNodeID(g.Definition.Name, parentRuntimeNodeID, templateID, occurrenceKey)
	if g.nodeIndex(id) >= 0 {
		return "", fmt.Errorf("duplicate runtime node %q", id)
	}
	if template.Cardinality == CardinalityRepeated {
		instances := 0
		for _, node := range g.Nodes {
			if node.TemplateID == templateID {
				instances++
			}
		}
		if instances >= g.Definition.Bounds.MaxInstancesPerFanOut {
			return "", errors.New("maximum fan-out instances exceeded")
		}
	}
	seenDependency := map[string]bool{}
	for _, dependency := range dependencies {
		if dependency == id {
			return "", errors.New("node cannot depend on itself")
		}
		if g.nodeIndex(dependency) < 0 {
			return "", fmt.Errorf("unknown dependency %q", dependency)
		}
		if seenDependency[dependency] {
			return "", fmt.Errorf("duplicate dependency %q", dependency)
		}
		seenDependency[dependency] = true
	}
	eventCount := len(g.ProjectionEvents)
	node := NodeProjection{RuntimeNodeID: id, TemplateID: templateID, Label: template.Label, Dependencies: append([]string(nil), dependencies...), Status: NodeStatusPending, CreatedAt: now}
	g.Nodes = append(g.Nodes, node)
	g.append(ProjectionEvent{Type: EventNodeCreated, RuntimeNodeID: id, TemplateID: templateID, ToStatus: NodeStatusPending, Timestamp: now})
	for _, dependency := range dependencies {
		g.append(ProjectionEvent{Type: EventDependencyAdded, RuntimeNodeID: id, TemplateID: templateID, DependencyRuntimeNodeID: dependency, Timestamp: now})
	}
	if !g.withinProjectionBound() {
		g.Nodes = g.Nodes[:len(g.Nodes)-1]
		g.ProjectionEvents = g.ProjectionEvents[:eventCount]
		return "", errors.New("maximum projection bytes exceeded")
	}
	return id, nil
}

func (g *Graph) Transition(id string, next NodeStatus, reason string, now time.Time) error {
	index := g.nodeIndex(id)
	if index < 0 {
		return fmt.Errorf("unknown runtime node %q", id)
	}
	current := g.Nodes[index].Status
	if !allowedTransition(current, next) {
		return fmt.Errorf("invalid node transition %s -> %s", current, next)
	}
	if current == NodeStatusPending && (next == NodeStatusRunning || next == NodeStatusWaitingForUser) {
		for _, dependency := range g.Nodes[index].Dependencies {
			dependencyIndex := g.nodeIndex(dependency)
			if dependencyIndex < 0 || !terminalStatus(g.Nodes[dependencyIndex].Status) {
				return fmt.Errorf("dependency %q is not terminal", dependency)
			}
		}
	}
	previous := g.Nodes[index]
	eventCount := len(g.ProjectionEvents)
	g.Nodes[index].Status = next
	g.Nodes[index].ReasonCode = reason
	if next == NodeStatusRunning || next == NodeStatusWaitingForUser {
		g.Nodes[index].StartedAt = now
	}
	if terminalStatus(next) {
		g.Nodes[index].CompletedAt = now
	}
	eventType := EventNodeStatusChanged
	if next == NodeStatusSkipped {
		eventType = EventNodeSkipped
	}
	g.append(ProjectionEvent{Type: eventType, RuntimeNodeID: id, TemplateID: g.Nodes[index].TemplateID, FromStatus: current, ToStatus: next, ReasonCode: reason, Timestamp: now})
	template, _ := g.Definition.template(g.Nodes[index].TemplateID)
	if next == NodeStatusWaitingForUser {
		for _, action := range template.Actions {
			g.append(ProjectionEvent{Type: EventActionOffered, RuntimeNodeID: id, TemplateID: template.ID, ActionName: action.Name, Timestamp: now})
		}
	} else if current == NodeStatusWaitingForUser {
		for _, action := range template.Actions {
			g.append(ProjectionEvent{Type: EventActionWithdrawn, RuntimeNodeID: id, TemplateID: template.ID, ActionName: action.Name, Timestamp: now})
		}
	}
	if !g.withinProjectionBound() {
		g.Nodes[index] = previous
		g.ProjectionEvents = g.ProjectionEvents[:eventCount]
		return errors.New("maximum projection bytes exceeded")
	}
	return nil
}

func (g *Graph) Complete(now time.Time) error {
	if g.terminalStatus != "" {
		return errors.New("graph is already terminal")
	}
	for _, node := range g.Nodes {
		if !terminalStatus(node.Status) {
			return fmt.Errorf("node %q is not terminal", node.RuntimeNodeID)
		}
	}
	eventCount := len(g.ProjectionEvents)
	g.terminalStatus = "completed"
	g.append(ProjectionEvent{Type: EventGraphCompleted, Timestamp: now})
	if !g.withinProjectionBound() {
		g.terminalStatus = ""
		g.ProjectionEvents = g.ProjectionEvents[:eventCount]
		return errors.New("maximum projection bytes exceeded")
	}
	return nil
}

func (g *Graph) Fail(reason string, now time.Time) error {
	if g.terminalStatus != "" {
		return errors.New("graph is already terminal")
	}
	eventCount := len(g.ProjectionEvents)
	g.terminalStatus = "failed"
	g.append(ProjectionEvent{Type: EventGraphFailed, ReasonCode: reason, Timestamp: now})
	if !g.withinProjectionBound() {
		g.terminalStatus = ""
		g.ProjectionEvents = g.ProjectionEvents[:eventCount]
		return errors.New("maximum projection bytes exceeded")
	}
	return nil
}

func (g *Graph) Skip(id, reason string, now time.Time) error {
	return g.Transition(id, NodeStatusSkipped, reason, now)
}

func (g *Graph) Events() []ProjectionEvent {
	return append([]ProjectionEvent(nil), g.ProjectionEvents...)
}

func (g *Graph) RecordActionOutcome(outcome ActionOutcome, now time.Time) error {
	eventCount := len(g.ProjectionEvents)
	g.ActionRecords = append(g.ActionRecords, outcome)
	g.append(ProjectionEvent{
		Type: EventActionOutcome, RuntimeNodeID: outcome.RuntimeNodeID,
		ReasonCode: string(outcome.State), Timestamp: now,
	})
	if !g.withinProjectionBound() {
		g.ActionRecords = g.ActionRecords[:len(g.ActionRecords)-1]
		g.ProjectionEvents = g.ProjectionEvents[:eventCount]
		return errors.New("maximum projection bytes exceeded")
	}
	return nil
}

func (g *Graph) Snapshot() Projection {
	nodes := make([]NodeProjection, len(g.Nodes))
	copy(nodes, g.Nodes)
	current := make([]string, 0)
	actions := make([]AllowedAction, 0)
	status := "running"
	if g.terminalStatus != "" {
		status = g.terminalStatus
	}
	for _, node := range nodes {
		if node.Status == NodeStatusRunning || node.Status == NodeStatusWaitingForUser {
			current = append(current, node.RuntimeNodeID)
		}
		if node.Status == NodeStatusWaitingForUser {
			template, _ := g.Definition.template(node.TemplateID)
			for _, action := range template.Actions {
				actions = append(actions, AllowedAction{RuntimeNodeID: node.RuntimeNodeID, Name: action.Name, Label: action.Label})
			}
		}
		if g.terminalStatus == "" && (node.Status == NodeStatusFailed || node.Status == NodeStatusTimedOut) {
			status = "failed"
		}
	}
	if g.terminalStatus == "" && len(nodes) > 0 && status == "running" {
		allTerminal := true
		for _, node := range nodes {
			if !terminalStatus(node.Status) {
				allTerminal = false
				break
			}
		}
		if allTerminal {
			status = "completed"
		}
	}
	outcomes := append([]ActionOutcome(nil), g.ActionRecords...)
	eventStart := 0
	if len(g.ProjectionEvents) > 64 {
		eventStart = len(g.ProjectionEvents) - 64
	}
	recentEvents := append([]ProjectionEvent(nil), g.ProjectionEvents[eventStart:]...)
	return Projection{ContractVersion: "org.worker/v1", WorkflowName: g.WorkflowName, WorkerVersion: g.WorkerVersion, Revision: uint64(len(g.ProjectionEvents)), Status: status, Nodes: nodes, CurrentNodeIDs: current, AllowedActions: actions, ActionOutcomes: outcomes, RecentEvents: recentEvents}
}

func (g *Graph) nodeIndex(id string) int {
	for index := range g.Nodes {
		if g.Nodes[index].RuntimeNodeID == id {
			return index
		}
	}
	return -1
}

func (g *Graph) append(event ProjectionEvent) {
	event.Sequence = uint64(len(g.ProjectionEvents) + 1)
	g.ProjectionEvents = append(g.ProjectionEvents, event)
}

func (g *Graph) withinProjectionBound() bool {
	encoded, err := json.Marshal(g.Snapshot())
	return err == nil && len(encoded) <= g.Definition.Bounds.MaxProjectionBytes
}

func allowedTransition(current, next NodeStatus) bool {
	switch current {
	case NodeStatusPending:
		return next == NodeStatusRunning || next == NodeStatusWaitingForUser || next == NodeStatusSkipped || next == NodeStatusCanceled
	case NodeStatusRunning:
		return next == NodeStatusCompleted || next == NodeStatusFailed || next == NodeStatusCanceled || next == NodeStatusTimedOut
	case NodeStatusWaitingForUser:
		return next == NodeStatusCompleted || next == NodeStatusFailed || next == NodeStatusCanceled || next == NodeStatusTimedOut
	default:
		return false
	}
}

func terminalStatus(status NodeStatus) bool {
	switch status {
	case NodeStatusCompleted, NodeStatusFailed, NodeStatusCanceled, NodeStatusSkipped, NodeStatusTimedOut:
		return true
	default:
		return false
	}
}
