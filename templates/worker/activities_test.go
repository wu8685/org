package worker

import "testing"

func TestProcessReturnsTrimmedValue(t *testing.T) {
	result, err := Process(Input{Value: "  first run  "})
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "first run" {
		t.Fatalf("result = %#v", result)
	}
}

func TestProcessRejectsEmptyValue(t *testing.T) {
	if _, err := Process(Input{Value: "   "}); err == nil {
		t.Fatal("expected empty value error")
	}
}
