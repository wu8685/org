package parallelconfirmation

import "testing"

func TestBuildPlanReturnsStableNeutralBranches(t *testing.T) {
	plan, err := BuildPlan(Input{Subject: "  release notes "})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Subject != "release notes" || len(plan.Branches) != 2 || plan.Branches[0].Key != "summary" || plan.Branches[1].Key != "readiness" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestBuildPlanRejectsEmptySubject(t *testing.T) {
	if _, err := BuildPlan(Input{}); err == nil {
		t.Fatal("empty subject was accepted")
	}
}
