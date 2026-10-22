package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// ResolveHost returns the Docker endpoint the docker CLI would use:
// DOCKER_HOST, then DOCKER_CONTEXT, then the CLI's current context.
// An empty result means the SDK default (/var/run/docker.sock).
func ResolveHost() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".docker")
	}
	name := os.Getenv("DOCKER_CONTEXT")
	if name == "" {
		name = currentContext(dir)
	}
	if name == "" || name == "default" {
		return ""
	}
	return contextHost(dir, name)
}

func currentContext(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	return cfg.CurrentContext
}

func contextHost(configDir, name string) string {
	sum := sha256.Sum256([]byte(name))
	data, err := os.ReadFile(filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	return meta.Endpoints["docker"].Host
}
