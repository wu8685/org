package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecCommandRunner struct{}

func (ExecCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return output, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return output, nil
}

type KindRuntimeImageVerifier struct {
	runner CommandRunner
}

func NewKindRuntimeImageVerifier(runner CommandRunner) *KindRuntimeImageVerifier {
	if runner == nil {
		runner = ExecCommandRunner{}
	}
	return &KindRuntimeImageVerifier{runner: runner}
}

func (v *KindRuntimeImageVerifier) Verify(ctx context.Context, node, declared, runtime string) error {
	if node == "" {
		return errors.New("kind Worker node name is unavailable")
	}
	output, err := v.runner.Run(ctx, "docker", "exec", node, "crictl", "inspecti", declared)
	if err != nil {
		return err
	}
	var result struct {
		Status struct {
			RepoDigests []string `json:"repoDigests"`
		} `json:"status"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return fmt.Errorf("decode kind runtime image linkage: %w", err)
	}
	if !containsString(result.Status.RepoDigests, declared) || !containsString(result.Status.RepoDigests, runtime) {
		return errors.New("kind runtime image is not exactly linked to the declared immutable image")
	}
	return nil
}
