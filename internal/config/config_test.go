package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseValidConfigAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
cluster: { k3dName: dev }
compose: { project: dev-infra }
namespaces: [apps]
services:
  - name: order-service
    k8s: { namespace: platform, selector: app.kubernetes.io/name=order-service }
    health: https://api.example.local/order-service/actuator/health
  - name: postgres
    compose: postgresql
    health: tcp://localhost:5432
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Cluster.KubeContext != "k3d-dev" {
		t.Errorf("kube context = %q", cfg.Cluster.KubeContext)
	}
	if cfg.Health.Interval != 10*time.Second || cfg.Health.Timeout != 3*time.Second {
		t.Errorf("health defaults = %+v", cfg.Health)
	}
	if !cfg.Health.SkipVerify() {
		t.Error("expected insecureSkipVerify to default to true")
	}
	if cfg.AutoDiscover() {
		t.Error("configured services should disable auto-discovery")
	}
}

func TestParseDerivesK3dNameFromContext(t *testing.T) {
	cfg, err := Parse([]byte("cluster: { kubeContext: k3d-dev }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cluster.K3dName != "dev" {
		t.Errorf("k3d name = %q", cfg.Cluster.K3dName)
	}
	cfg, err = Parse([]byte("cluster: { kubeContext: minikube }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cluster.K3dName != "" {
		t.Errorf("non-k3d context should not set k3d name, got %q", cfg.Cluster.K3dName)
	}
}

func TestParseEmptyIsAutoDiscovery(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoDiscover() {
		t.Error("empty config should auto-discover")
	}
}

func TestParseRejectsInvalidServices(t *testing.T) {
	_, err := Parse([]byte(`
services:
  - name: a
  - name: a
    compose: x
  - name: b
    k8s: { namespace: "", selector: "app in (" }
  - name: c
    compose: c
    health: ftp://x
  - name: d
    compose: d
    health: tcp://localhost
`))
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{
		`service "a": needs k8s and/or compose`,
		`service "a": duplicate name`,
		`service "b": k8s.namespace is required`,
		`service "b": k8s.selector`,
		`unsupported scheme "ftp"`,
		`tcp health needs host:port`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	if _, err := Parse([]byte("servces: []\n")); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestLoadSearchesWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("namespaces: [a]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path == "" || len(cfg.Namespaces) != 1 {
		t.Errorf("config not loaded from working dir: %+v", cfg)
	}
}

func TestLoadSearchesGitRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("namespaces: [x, y]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Namespaces) != 2 {
		t.Errorf("expected config from git root, got %+v", cfg)
	}
}
