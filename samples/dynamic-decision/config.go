package dynamicdecision

import (
	"fmt"
	"sort"
	"strings"
)

type Config struct {
	TemporalAddress          string
	TemporalNamespace        string
	TemporalTaskQueue        string
	TemporalWorkerDeployment string
	TemporalWorkerBuildID    string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		TemporalAddress: strings.TrimSpace(getenv("TEMPORAL_ADDRESS")), TemporalNamespace: strings.TrimSpace(getenv("TEMPORAL_NAMESPACE")),
		TemporalTaskQueue: strings.TrimSpace(getenv("TEMPORAL_TASK_QUEUE")), TemporalWorkerDeployment: strings.TrimSpace(getenv("TEMPORAL_WORKER_DEPLOYMENT")),
		TemporalWorkerBuildID: strings.TrimSpace(getenv("TEMPORAL_WORKER_BUILD_ID")),
	}
	if cfg.TemporalNamespace == "" {
		cfg.TemporalNamespace = "default"
	}
	required := map[string]string{
		"TEMPORAL_ADDRESS": cfg.TemporalAddress, "TEMPORAL_TASK_QUEUE": cfg.TemporalTaskQueue,
		"TEMPORAL_WORKER_DEPLOYMENT": cfg.TemporalWorkerDeployment, "TEMPORAL_WORKER_BUILD_ID": cfg.TemporalWorkerBuildID,
	}
	var missing []string
	for name, value := range required {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return Config{}, fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
