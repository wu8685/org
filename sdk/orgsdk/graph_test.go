package orgsdk

import (
	"reflect"
	"testing"
	"time"
)

func TestDynamicGraphCreatesStableFanOutNodesAndEvents(t *testing.T) {
	definition := graphDefinition()
	graph, err := NewGraph(definition, "DynamicWorkflow", "v1", time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	root, err := graph.CreateNode("decide", "", "singleton", nil, time.Unix(101, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(root, NodeStatusRunning, "", time.Unix(102, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(root, NodeStatusCompleted, "", time.Unix(103, 0).UTC()); err != nil {
		t.Fatal(err)
	}

	left, err := graph.CreateNode("branch", root, "left", []string{root}, time.Unix(104, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	right, err := graph.CreateNode("branch", root, "right", []string{root}, time.Unix(104, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if left == right || left != RuntimeNodeID("dynamic", root, "branch", "left") {
		t.Fatalf("runtime IDs left=%q right=%q", left, right)
	}
	if err := graph.Transition(left, NodeStatusRunning, "", time.Unix(105, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(right, NodeStatusRunning, "", time.Unix(105, 0).UTC()); err != nil {
		t.Fatal(err)
	}

	snapshot := graph.Snapshot()
	if !reflect.DeepEqual(snapshot.CurrentNodeIDs, []string{left, right}) {
		t.Fatalf("current nodes = %#v", snapshot.CurrentNodeIDs)
	}
	if snapshot.Revision != uint64(len(graph.Events())) || snapshot.Revision < 9 {
		t.Fatalf("revision=%d events=%d", snapshot.Revision, len(graph.Events()))
	}
	if snapshot.Nodes[1].Dependencies[0] != root || snapshot.Nodes[2].Dependencies[0] != root {
		t.Fatalf("dependencies = %#v", snapshot.Nodes)
	}

	replayed, _ := NewGraph(definition, "DynamicWorkflow", "v1", time.Unix(100, 0).UTC())
	replayedRoot, _ := replayed.CreateNode("decide", "", "singleton", nil, time.Unix(101, 0).UTC())
	replayedLeft, _ := replayed.CreateNode("branch", replayedRoot, "left", []string{replayedRoot}, time.Unix(104, 0).UTC())
	if replayedLeft != left {
		t.Fatalf("replay ID = %q, want %q", replayedLeft, left)
	}
}

func TestDynamicGraphMarksUnselectedCandidateSkipped(t *testing.T) {
	graph, err := NewGraph(graphDefinition(), "DynamicWorkflow", "v1", time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := graph.CreateNode("concise", "", "singleton", nil, time.Unix(101, 0).UTC())
	skipped, _ := graph.CreateNode("detailed", "", "singleton", nil, time.Unix(101, 0).UTC())
	if err := graph.Transition(selected, NodeStatusRunning, "", time.Unix(102, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := graph.Skip(skipped, "route-not-selected", time.Unix(102, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	snapshot := graph.Snapshot()
	if snapshot.Node(selected).Status != NodeStatusRunning || snapshot.Node(skipped).Status != NodeStatusSkipped || snapshot.Node(skipped).ReasonCode != "route-not-selected" {
		t.Fatalf("projection = %#v", snapshot)
	}
}

func TestDynamicGraphRejectsDuplicateKeysCyclesAndBounds(t *testing.T) {
	definition := graphDefinition()
	definition.Bounds.MaxRuntimeNodes = 2
	graph, _ := NewGraph(definition, "DynamicWorkflow", "v1", time.Now())
	root, _ := graph.CreateNode("decide", "", "singleton", nil, time.Now())
	if _, err := graph.CreateNode("branch", root, "same", []string{root}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := graph.CreateNode("branch", root, "same", []string{root}, time.Now()); err == nil {
		t.Fatal("duplicate occurrence key was accepted")
	}
	if _, err := graph.CreateNode("branch", root, "third", []string{root}, time.Now()); err == nil {
		t.Fatal("runtime node bound was ignored")
	}
}

func TestNodeCannotStartBeforeDependenciesAreTerminal(t *testing.T) {
	graph, err := NewGraph(graphDefinition(), "DynamicWorkflow", "v1", time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	root, err := graph.CreateNode("decide", "", "singleton", nil, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	branch, err := graph.CreateNode("branch", root, "left", []string{root}, time.Unix(3, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(branch, NodeStatusRunning, "", time.Unix(4, 0).UTC()); err == nil {
		t.Fatal("dependent node started before its dependency completed")
	}
	if err := graph.Skip(root, "not-selected", time.Unix(5, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(branch, NodeStatusRunning, "", time.Unix(6, 0).UTC()); err != nil {
		t.Fatalf("terminal skipped dependency should unblock child: %v", err)
	}
}

func TestDynamicGraphRejectsProjectionBoundTooSmallForInitialSnapshot(t *testing.T) {
	definition := graphDefinition()
	definition.Bounds.MaxProjectionBytes = 1
	if _, err := NewGraph(definition, "DynamicWorkflow", "v1", time.Now()); err == nil {
		t.Fatal("projection byte bound was ignored")
	}
}

func TestGraphEmitsActionLifecycleAndTerminalEvents(t *testing.T) {
	definition := Definition{
		Name: "action-events", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 2, MaxProjectionBytes: 8192},
		Templates: []NodeTemplate{{
			ID: "approval", Label: "Approval", Type: NodeTypeWaitForAction,
			Actions: []ActionDefinition{{Name: "confirm", Label: "Confirm", RequiredPermission: "run:action:confirm"}},
		}},
	}
	graph, err := NewGraph(definition, "ActionWorkflow", "v1", time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := graph.CreateNode("approval", "", "singleton", nil, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(nodeID, NodeStatusWaitingForUser, "waiting-for-action", time.Unix(3, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(nodeID, NodeStatusCompleted, "", time.Unix(4, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := graph.Complete(time.Unix(5, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	events := graph.Events()
	want := []EventType{EventGraphInitialized, EventNodeCreated, EventNodeStatusChanged, EventActionOffered, EventNodeStatusChanged, EventActionWithdrawn, EventGraphCompleted}
	if len(events) != len(want) {
		t.Fatalf("events = %#v", events)
	}
	for i := range want {
		if events[i].Type != want[i] {
			t.Fatalf("event %d = %q, want %q", i, events[i].Type, want[i])
		}
	}
	snapshot := graph.Snapshot()
	if snapshot.Status != "completed" || len(snapshot.RecentEvents) == 0 || snapshot.RecentEvents[len(snapshot.RecentEvents)-1].Type != EventGraphCompleted {
		t.Fatalf("terminal projection = %#v", snapshot)
	}
}

func TestGraphCannotCompleteWithNonTerminalNodes(t *testing.T) {
	graph, err := NewGraph(graphDefinition(), "DynamicWorkflow", "v1", time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.CreateNode("decide", "", "singleton", nil, time.Unix(2, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := graph.Complete(time.Unix(3, 0).UTC()); err == nil {
		t.Fatal("graph completed with a pending node")
	}
	if err := graph.Fail("workflow-error", time.Unix(4, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if graph.Snapshot().Status != "failed" || graph.Events()[len(graph.Events())-1].Type != EventGraphFailed {
		t.Fatalf("failed projection/events = %#v / %#v", graph.Snapshot(), graph.Events())
	}
}

func graphDefinition() Definition {
	return Definition{
		Name:   "dynamic",
		Bounds: RuntimeBounds{MaxInstancesPerFanOut: 4, MaxRuntimeNodes: 8, MaxProjectionBytes: 64 << 10},
		Templates: []NodeTemplate{
			{ID: "decide", Label: "Decide", Type: NodeTypeActivity, Activity: &ActivityPolicy{SideEffect: SideEffectRead, Retry: RetryPolicy{MaximumAttempts: 2, StartToCloseTimeout: time.Second}}},
			{ID: "branch", Label: "Branch", Type: NodeTypeActivity, Cardinality: CardinalityRepeated, Activity: &ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 2, StartToCloseTimeout: time.Second}}},
			{ID: "concise", Label: "Concise", Type: NodeTypeActivity, Activity: &ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}},
			{ID: "detailed", Label: "Detailed", Type: NodeTypeActivity, Activity: &ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}},
		},
	}
}
