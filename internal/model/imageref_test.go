package model

import "testing"

func TestNormalizeImageRef(t *testing.T) {
	cases := map[string]string{
		"order-service:local":                     "docker.io/library/order-service:local",
		"order-service":                           "docker.io/library/order-service:latest",
		"grafana/grafana:11.4.0":                  "docker.io/grafana/grafana:11.4.0",
		"docker.io/library/traefik:v3":            "docker.io/library/traefik:v3",
		"quay.io/prometheus/node-exporter:v1.8.2": "quay.io/prometheus/node-exporter:v1.8.2",
		"localhost:5000/app:dev":                  "localhost:5000/app:dev",
		"localhost/app":                           "localhost/app:latest",
		"registry:5000/app":                       "registry:5000/app:latest",
		"busybox@sha256:abc":                      "docker.io/library/busybox@sha256:abc",
		"":                                        "",
	}
	for in, want := range cases {
		if got := NormalizeImageRef(in); got != want {
			t.Errorf("NormalizeImageRef(%q) = %q, want %q", in, got, want)
		}
	}
}
