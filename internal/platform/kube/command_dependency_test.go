package kube

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKubernetesAPIAdapterDoesNotInvokeKubectl(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve kube package directory")
	}
	directory := filepath.Dir(current)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(contents), "kubectl") {
			t.Fatalf("production kube package still references kubectl in %s", entry.Name())
		}
		if entry.Name() != "kind_image_verifier.go" && strings.Contains(string(contents), `"os/exec"`) {
			t.Fatalf("Kubernetes API adapter starts external commands in %s", entry.Name())
		}
	}
}
