package dynamicdecision

import (
	"strings"
	"testing"
)

func TestDetermineRouteNormalizesSupportedModes(t *testing.T) {
	for input, want := range map[string]string{" concise ": "concise", "DETAILED": "detailed"} {
		route, err := DetermineRoute(Input{Mode: input, Subject: "release notes"})
		if err != nil {
			t.Fatalf("DetermineRoute(%q): %v", input, err)
		}
		if route.Name != want || route.Subject != "release notes" {
			t.Fatalf("DetermineRoute(%q) = %#v", input, route)
		}
	}
}

func TestDetermineRouteRejectsUnsupportedMode(t *testing.T) {
	_, err := DetermineRoute(Input{Mode: "automatic-secret-value", Subject: "release notes"})
	if err == nil {
		t.Fatal("unsupported route was accepted")
	}
	if err.Error() != "Unsupported mode. Choose concise or detailed." || strings.Contains(err.Error(), "automatic-secret-value") {
		t.Fatalf("unsupported route did not return the bounded user-safe error: %q", err)
	}
}

func TestCandidateActivitiesAndFinalizeStayNeutral(t *testing.T) {
	concise, err := RunConcise(BranchInput{Subject: "release notes"})
	if err != nil {
		t.Fatal(err)
	}
	detailed, err := RunDetailed(BranchInput{Subject: "release notes"})
	if err != nil {
		t.Fatal(err)
	}
	if concise.Route != "concise" || detailed.Route != "detailed" || concise.Content == detailed.Content {
		t.Fatalf("concise=%#v detailed=%#v", concise, detailed)
	}
	result, err := Finalize(FinalizeInput{Subject: "release notes", Selected: concise, WorkerVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Route != "concise" || result.WorkerVersion != "v1" {
		t.Fatalf("result = %#v", result)
	}
}
