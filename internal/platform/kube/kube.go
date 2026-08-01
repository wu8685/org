package kube

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/wu8685/org/internal/domain"
)

type Config struct {
	Namespace, Context, Kubeconfig, WorkerTemporalAddress, TemporalNamespace string
	ReadinessTimeout                                                         time.Duration
	NetworkPolicyEnabled, NetworkPolicyEnforced                              bool
}
type Runner interface {
	Run(context.Context, string, string, ...string) ([]byte, error)
}
type Client struct {
	cfg    Config
	runner Runner
}

func New(cfg Config, runner Runner) *Client {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Client{cfg: cfg, runner: runner}
}

func (c *Client) Apply(ctx context.Context, d domain.WorkerVersion) error {
	manifest, err := RenderManifest(d, c.cfg)
	if err != nil {
		return err
	}
	_, err = c.runner.Run(ctx, manifest, "kubectl", append(c.flags(), "apply", "-f", "-")...)
	return err
}

func (c *Client) WaitReady(ctx context.Context, d domain.WorkerVersion) error {
	timeout := c.cfg.ReadinessTimeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	_, err := c.runner.Run(ctx, "", "kubectl", append(c.flags(), "-n", c.cfg.Namespace, "rollout", "status", "deployment/"+workloadName(d), "--timeout="+timeout.String())...)
	return err
}

func (c *Client) flags() []string {
	out := []string{}
	if c.cfg.Context != "" {
		out = append(out, "--context", c.cfg.Context)
	}
	if c.cfg.Kubeconfig != "" {
		out = append(out, "--kubeconfig", c.cfg.Kubeconfig)
	}
	return out
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func RenderManifest(d domain.WorkerVersion, cfg Config) (string, error) {
	if cfg.Namespace == "" || cfg.WorkerTemporalAddress == "" || cfg.TemporalNamespace == "" {
		return "", errors.New("kubernetes namespace and Worker Temporal connection are required")
	}
	if d.TenantID == "" || d.TenantSlug == "" || d.TenantHash == "" || d.VersionHash == "" || d.KubernetesDeployment == "" || d.KubernetesServiceAccount == "" {
		return "", errors.New("tenant-qualified canonical Kubernetes identity is required")
	}
	name := workloadName(d)
	var extraEnv strings.Builder
	for _, env := range d.Runtime.Environment {
		fmt.Fprintf(&extraEnv, "        - name: %s\n          valueFrom:\n            secretKeyRef:\n              name: %s\n              key: %s\n", env.Name, env.Secret, env.SecretKey)
	}
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: org
    org.wu8685.dev/tenant: %s
    org.wu8685.dev/tenant-hash: %s
    org.wu8685.dev/worker: %s
    org.wu8685.dev/version: %s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: org
    org.wu8685.dev/tenant: %s
    org.wu8685.dev/tenant-hash: %s
    org.wu8685.dev/worker: %s
    org.wu8685.dev/version: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: %s
  template:
    metadata:
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/managed-by: org
        org.wu8685.dev/tenant: %s
        org.wu8685.dev/tenant-hash: %s
        org.wu8685.dev/worker: %s
        org.wu8685.dev/version: %s
    spec:
      serviceAccountName: %s
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
      - name: worker
        image: %s
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: ["ALL"]
        resources:
          requests: {cpu: %s, memory: %s}
          limits: {cpu: %s, memory: %s}
        volumeMounts:
        - {name: tmp, mountPath: /tmp}
        env:
        - {name: TEMPORAL_ADDRESS, value: %s}
        - {name: TEMPORAL_NAMESPACE, value: %s}
        - {name: TEMPORAL_TASK_QUEUE, value: %s}
        - {name: TEMPORAL_WORKER_DEPLOYMENT, value: %s}
        - {name: TEMPORAL_WORKER_BUILD_ID, value: %s}
%s      volumes:
      - name: tmp
        emptyDir: {}
`, cfg.Namespace, d.KubernetesServiceAccount, cfg.Namespace, d.TenantSlug, d.TenantHash, d.WorkerName, d.VersionHash, name, cfg.Namespace, d.TenantSlug, d.TenantHash, d.WorkerName, d.VersionHash, name, name, d.TenantSlug, d.TenantHash, d.WorkerName, d.VersionHash, d.KubernetesServiceAccount, d.Image, d.Runtime.CPU, d.Runtime.Memory, d.Runtime.CPU, d.Runtime.Memory, cfg.WorkerTemporalAddress, cfg.TemporalNamespace, d.TaskQueue, d.WorkerDeployment, d.Version, extraEnv.String())
	if cfg.NetworkPolicyEnabled {
		manifest += fmt.Sprintf(`---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: org
    org.wu8685.dev/tenant: %s
    org.wu8685.dev/tenant-hash: %s
    org.wu8685.dev/worker: %s
    org.wu8685.dev/version: %s
spec:
  podSelector:
    matchLabels:
      org.wu8685.dev/tenant-hash: %s
      org.wu8685.dev/worker: %s
      org.wu8685.dev/version: %s
  policyTypes: [Ingress]
`, d.KubernetesNetworkPolicy, cfg.Namespace, d.TenantSlug, d.TenantHash, d.WorkerName, d.VersionHash, d.TenantHash, d.WorkerName, d.VersionHash)
	}
	return manifest, nil
}

func workloadName(d domain.WorkerVersion) string {
	if d.KubernetesDeployment != "" {
		return d.KubernetesDeployment
	}
	return ""
}

func NetworkPolicyStatus(cfg Config) string {
	if !cfg.NetworkPolicyEnabled {
		return "disabled"
	}
	if !cfg.NetworkPolicyEnforced {
		return "manifest_only_not_enforced"
	}
	return "enforced_by_cluster_cni"
}
