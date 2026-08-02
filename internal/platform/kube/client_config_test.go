package kube

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildRESTConfigUsesExplicitKubeconfigAndContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	contents := []byte(`apiVersion: v1
kind: Config
clusters:
- name: first
  cluster: {server: https://first.example.test}
- name: second
  cluster: {server: https://second.example.test}
contexts:
- name: first
  context: {cluster: first, user: test}
- name: second
  context: {cluster: second, user: test}
current-context: first
users:
- name: test
  user: {token: secret-token}
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildRESTConfig("second", path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "https://second.example.test" || cfg.BearerToken != "secret-token" {
		t.Fatalf("REST config = %#v", cfg)
	}
}

func TestBuildRESTConfigRejectsMissingContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRESTConfig("missing", path); err == nil {
		t.Fatal("missing context was accepted")
	}
}
