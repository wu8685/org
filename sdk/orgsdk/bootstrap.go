package orgsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrBootstrapRegistrationRejected = errors.New("org bootstrap registration rejected")

type BootstrapRegistrationState string

const (
	BootstrapAccepted BootstrapRegistrationState = "accepted"
	BootstrapRejected BootstrapRegistrationState = "rejected"
)

type BootstrapRegistrationRequest struct {
	ManifestDigest string          `json:"manifestDigest"`
	Contract       json.RawMessage `json:"contract"`
	BuildID        string          `json:"buildId"`
}

type BootstrapRegistrationResult struct {
	State     BootstrapRegistrationState `json:"state"`
	ReceiptID string                     `json:"receiptId,omitempty"`
	Reason    string                     `json:"reason,omitempty"`
}

type BootstrapRegistrar interface {
	Register(context.Context, BootstrapConfig, BootstrapRegistrationRequest) (BootstrapRegistrationResult, error)
}

type BootstrapConfig struct {
	Endpoint            string
	Credential          string
	WorkloadToken       string
	PodUID              string
	CredentialExpiresAt time.Time
	HTTPClient          *http.Client
	Registrar           BootstrapRegistrar
}

type HostedWorkerConfig struct {
	Worker    WorkerConfig
	Bootstrap BootstrapConfig
}

func LoadHostedWorkerConfig(getenv func(string) string, readFile func(string) ([]byte, error)) (HostedWorkerConfig, error) {
	if getenv == nil || readFile == nil {
		return HostedWorkerConfig{}, errors.New("configuration readers are required")
	}
	token, err := readFile(strings.TrimSpace(getenv("ORG_BOOTSTRAP_TOKEN_FILE")))
	if err != nil {
		return HostedWorkerConfig{}, fmt.Errorf("read bootstrap token: %w", err)
	}
	workloadToken, err := readFile(strings.TrimSpace(getenv("ORG_BOOTSTRAP_WORKLOAD_TOKEN_FILE")))
	if err != nil {
		return HostedWorkerConfig{}, fmt.Errorf("read bootstrap workload token: %w", err)
	}
	config := HostedWorkerConfig{
		Worker:    WorkerConfig{TemporalAddress: strings.TrimSpace(getenv("TEMPORAL_ADDRESS")), TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")), TaskQueue: strings.TrimSpace(getenv("TEMPORAL_TASK_QUEUE")), DeploymentName: strings.TrimSpace(getenv("TEMPORAL_WORKER_DEPLOYMENT")), BuildID: strings.TrimSpace(getenv("TEMPORAL_WORKER_BUILD_ID"))},
		Bootstrap: BootstrapConfig{Endpoint: strings.TrimSpace(getenv("ORG_BOOTSTRAP_ENDPOINT")), Credential: strings.TrimSpace(string(token)), WorkloadToken: strings.TrimSpace(string(workloadToken)), PodUID: strings.TrimSpace(getenv("ORG_BOOTSTRAP_POD_UID"))},
	}
	if value := strings.TrimSpace(getenv("ORG_BOOTSTRAP_EXPIRES_AT")); value != "" {
		expires, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return HostedWorkerConfig{}, errors.New("invalid bootstrap expiry")
		}
		config.Bootstrap.CredentialExpiresAt = expires
	}
	if config.Bootstrap.Endpoint == "" || config.Bootstrap.Credential == "" || config.Bootstrap.WorkloadToken == "" || config.Bootstrap.PodUID == "" {
		return HostedWorkerConfig{}, errors.New("complete bootstrap configuration is required")
	}
	return config, nil
}

// RunHostedWorker is the production startup entrypoint. It constructs the
// contract from typed Workflow Definitions, registers it, and only then dials
// Temporal and starts polling.
func RunHostedWorker(ctx context.Context, config HostedWorkerConfig, registrations ...Registration) error {
	base := config.Worker
	base.ManifestDigest = "sha256:" + strings.Repeat("0", 64)
	if err := base.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(config.Bootstrap.Credential) == "" {
		return errors.New("bootstrap credential is required")
	}
	workflowName, definition, err := bootstrapDefinition(registrations)
	if err != nil {
		return err
	}
	contract, digest, err := GenerateManifest(workflowName, definition)
	if err != nil {
		return err
	}
	request := BootstrapRegistrationRequest{ManifestDigest: digest, Contract: contract, BuildID: config.Worker.BuildID}
	registrar := config.Bootstrap.Registrar
	if registrar == nil {
		registrar = HTTPBootstrapRegistrar{}
	}
	result, err := registrar.Register(ctx, config.Bootstrap, request)
	if err != nil {
		return err
	}
	if result.State != BootstrapAccepted || result.ReceiptID == "" {
		return fmt.Errorf("%w: %s", ErrBootstrapRegistrationRejected, result.Reason)
	}
	config.Worker.ManifestDigest = digest
	runtime, err := NewWorkerRuntime(config.Worker, registrations...)
	if err != nil {
		return err
	}
	return runtime.Run(ctx)
}

func bootstrapDefinition(registrations []Registration) (string, Definition, error) {
	var name string
	var definition Definition
	for _, registration := range registrations {
		candidate, ok := registration.(interface{ bootstrapContract() (string, Definition) })
		if !ok {
			continue
		}
		if name != "" {
			return "", Definition{}, errors.New("bootstrap currently supports exactly one Workflow Definition per Worker")
		}
		name, definition = candidate.bootstrapContract()
	}
	if name == "" {
		return "", Definition{}, errors.New("a typed Workflow Definition is required for bootstrap")
	}
	return name, definition, nil
}

type HTTPBootstrapRegistrar struct{}

func (HTTPBootstrapRegistrar) Register(ctx context.Context, config BootstrapConfig, request BootstrapRegistrationRequest) (BootstrapRegistrationResult, error) {
	if strings.TrimSpace(config.Endpoint) == "" {
		return BootstrapRegistrationResult{}, errors.New("bootstrap endpoint is required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return BootstrapRegistrationResult{}, err
	}
	for attempt := 0; ; attempt++ {
		if !config.CredentialExpiresAt.IsZero() && !time.Now().Before(config.CredentialExpiresAt) {
			return BootstrapRegistrationResult{}, ErrBootstrapRegistrationRejected
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.Endpoint, bytes.NewReader(body))
		if err != nil {
			return BootstrapRegistrationResult{}, err
		}
		req.Header.Set("Authorization", "Bearer "+config.Credential)
		req.Header.Set("X-Org-Workload-Token", config.WorkloadToken)
		req.Header.Set("X-Org-Pod-UID", config.PodUID)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err == nil && response.StatusCode < 500 {
			defer response.Body.Close()
			limited, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
			if readErr != nil {
				return BootstrapRegistrationResult{}, readErr
			}
			var result BootstrapRegistrationResult
			if err := json.Unmarshal(limited, &result); err != nil {
				return BootstrapRegistrationResult{}, err
			}
			if response.StatusCode != http.StatusOK {
				return result, fmt.Errorf("%w: %s", ErrBootstrapRegistrationRejected, result.Reason)
			}
			return result, nil
		}
		if response != nil {
			response.Body.Close()
		}
		if attempt >= 4 {
			if err != nil {
				return BootstrapRegistrationResult{}, err
			}
			return BootstrapRegistrationResult{}, errors.New("bootstrap registration unavailable")
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return BootstrapRegistrationResult{}, ctx.Err()
		case <-timer.C:
		}
	}
}
