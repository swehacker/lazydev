// Package config loads and validates lazydev.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/labels"
)

const FileName = "lazydev.yaml"

type Config struct {
	Cluster    Cluster   `yaml:"cluster"`
	Compose    Compose   `yaml:"compose"`
	Namespaces []string  `yaml:"namespaces"`
	Health     Health    `yaml:"health"`
	Services   []Service `yaml:"services"`

	// Path is the file the config was loaded from; empty in auto-discovery mode.
	Path string `yaml:"-"`
}

type Cluster struct {
	K3dName     string `yaml:"k3dName"`
	KubeContext string `yaml:"kubeContext"`
}

type Compose struct {
	Project string `yaml:"project"`
}

type Health struct {
	Interval           time.Duration `yaml:"interval"`
	Timeout            time.Duration `yaml:"timeout"`
	InsecureSkipVerify *bool         `yaml:"insecureSkipVerify"`
}

// SkipVerify defaults to true because local stacks typically use mkcert or self-signed certificates.
func (h Health) SkipVerify() bool {
	return h.InsecureSkipVerify == nil || *h.InsecureSkipVerify
}

type Service struct {
	Name    string  `yaml:"name"`
	K8s     *K8sRef `yaml:"k8s"`
	Compose string  `yaml:"compose"`
	Image   string  `yaml:"image"`
	Health  string  `yaml:"health"`
	Build   *Build  `yaml:"build"`
}

type K8sRef struct {
	Namespace  string `yaml:"namespace"`
	Selector   string `yaml:"selector"`
	Deployment string `yaml:"deployment"`
}

// Build is reserved for the rebuild/import/rollout dev loop.
type Build struct {
	Dir string `yaml:"dir"`
	Cmd string `yaml:"cmd"`
}

// AutoDiscover reports whether logical services should be derived from the cluster and compose state.
func (c *Config) AutoDiscover() bool { return len(c.Services) == 0 }

// Load reads the config at path, or searches the default locations when path is empty.
// When no file is found, it returns a default config in auto-discovery mode.
func Load(path string) (*Config, error) {
	if path == "" {
		path = Find()
	}
	if path == "" {
		cfg := &Config{}
		cfg.applyDefaults()
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	for i := range cfg.Services {
		if b := cfg.Services[i].Build; b != nil && b.Dir != "" && !filepath.IsAbs(b.Dir) {
			b.Dir = filepath.Join(filepath.Dir(path), b.Dir)
		}
	}
	return cfg, nil
}

// Parse decodes, defaults and validates config data.
func Parse(data []byte) (*Config, error) {
	cfg := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Find returns the first existing config file in the default lookup order, or "".
func Find() string {
	var candidates []string
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, FileName))
		if root := gitRoot(wd); root != "" {
			candidates = append(candidates, filepath.Join(root, FileName))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "lazydev", "config.yaml"))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func gitRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (c *Config) applyDefaults() {
	if c.Health.Interval == 0 {
		c.Health.Interval = 10 * time.Second
	}
	if c.Health.Timeout == 0 {
		c.Health.Timeout = 3 * time.Second
	}
	if c.Cluster.KubeContext == "" && c.Cluster.K3dName != "" {
		c.Cluster.KubeContext = "k3d-" + c.Cluster.K3dName
	}
	if c.Cluster.K3dName == "" {
		c.Cluster.K3dName = strings.TrimPrefix(c.Cluster.KubeContext, "k3d-")
		if c.Cluster.K3dName == c.Cluster.KubeContext {
			c.Cluster.K3dName = ""
		}
	}
}

func (c *Config) Validate() error {
	var errs []error
	if c.Health.Interval < 0 || c.Health.Timeout < 0 {
		errs = append(errs, errors.New("health: interval and timeout must be positive"))
	}
	seen := map[string]bool{}
	for i, s := range c.Services {
		where := fmt.Sprintf("services[%d]", i)
		if s.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", where))
		} else {
			where = fmt.Sprintf("service %q", s.Name)
			if seen[s.Name] {
				errs = append(errs, fmt.Errorf("%s: duplicate name", where))
			}
			seen[s.Name] = true
		}
		if s.K8s == nil && s.Compose == "" {
			errs = append(errs, fmt.Errorf("%s: needs k8s and/or compose", where))
		}
		if s.K8s != nil {
			if s.K8s.Namespace == "" {
				errs = append(errs, fmt.Errorf("%s: k8s.namespace is required", where))
			}
			if s.K8s.Selector == "" {
				errs = append(errs, fmt.Errorf("%s: k8s.selector is required", where))
			} else if _, err := labels.Parse(s.K8s.Selector); err != nil {
				errs = append(errs, fmt.Errorf("%s: k8s.selector: %w", where, err))
			}
		}
		if s.Health != "" {
			if err := validateHealthURL(s.Health); err != nil {
				errs = append(errs, fmt.Errorf("%s: health: %w", where, err))
			}
		}
	}
	return errors.Join(errs...)
}

func validateHealthURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "http", "https":
		if u.Host == "" {
			return errors.New("missing host")
		}
	case "tcp":
		if u.Host == "" || u.Port() == "" {
			return errors.New("tcp health needs host:port")
		}
	default:
		return fmt.Errorf("unsupported scheme %q (use http, https or tcp)", u.Scheme)
	}
	return nil
}
