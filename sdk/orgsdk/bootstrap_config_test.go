package orgsdk

import (
	"os"
	"testing"
)

func TestLoadHostedWorkerConfigReadsInjectedBootstrapFiles(t *testing.T) {
	env := map[string]string{
		"TEMPORAL_ADDRESS": "host.docker.internal:7233", "TEMPORAL_NAMESPACE": "default", "TEMPORAL_TASK_QUEUE": "org-local-hello", "TEMPORAL_WORKER_DEPLOYMENT": "org-local-hello", "TEMPORAL_WORKER_BUILD_ID": "v1",
		"ORG_BOOTSTRAP_ENDPOINT": "http://host.docker.internal:8090/internal/v1/bootstrap/register", "ORG_BOOTSTRAP_TOKEN_FILE": "/bootstrap/token", "ORG_BOOTSTRAP_WORKLOAD_TOKEN_FILE": "/workload/token", "ORG_BOOTSTRAP_POD_UID": "pod-1",
	}
	files := map[string][]byte{"/bootstrap/token": []byte("opaque\n"), "/workload/token": []byte("bound\n")}
	config, err := LoadHostedWorkerConfig(func(key string) string { return env[key] }, func(path string) ([]byte, error) {
		value, ok := files[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Bootstrap.Credential != "opaque" || config.Bootstrap.WorkloadToken != "bound" || config.Bootstrap.PodUID != "pod-1" || config.Worker.BuildID != "v1" {
		t.Fatalf("config = %#v", config)
	}
}
