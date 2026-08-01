package hello

import "testing"

func TestLoadConfigDefaultsNamespace(t *testing.T) {
	values := map[string]string{
		"TEMPORAL_ADDRESS":           "host.docker.internal:7233",
		"TEMPORAL_TASK_QUEUE":        "org-hello",
		"TEMPORAL_WORKER_DEPLOYMENT": "org-hello",
		"TEMPORAL_WORKER_BUILD_ID":   "v1",
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
		"TEMPORAL_ADDRESS":           "host.docker.internal:7233",
		"TEMPORAL_TASK_QUEUE":        "org-hello",
		"TEMPORAL_WORKER_DEPLOYMENT": "org-hello",
	}
	if _, err := LoadConfig(func(key string) string { return values[key] }); err == nil {
		t.Fatal("expected missing TEMPORAL_WORKER_BUILD_ID error")
	}
}
