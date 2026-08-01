package dynamicdecision

import "testing"

func TestLoadConfigDefaultsPlatformTemporalNamespace(t *testing.T) {
	values := map[string]string{
		"TEMPORAL_ADDRESS": "host.docker.internal:7233", "TEMPORAL_TASK_QUEUE": "tenant-worker",
		"TEMPORAL_WORKER_DEPLOYMENT": "tenant-worker", "TEMPORAL_WORKER_BUILD_ID": "v1",
	}
	cfg, err := LoadConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TemporalNamespace != "default" || cfg.TemporalWorkerBuildID != "v1" {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestLoadConfigRejectsMissingWorkerVersion(t *testing.T) {
	values := map[string]string{
		"TEMPORAL_ADDRESS": "host.docker.internal:7233", "TEMPORAL_TASK_QUEUE": "tenant-worker",
		"TEMPORAL_WORKER_DEPLOYMENT": "tenant-worker",
	}
	if _, err := LoadConfig(func(key string) string { return values[key] }); err == nil {
		t.Fatal("expected missing TEMPORAL_WORKER_BUILD_ID error")
	}
}
