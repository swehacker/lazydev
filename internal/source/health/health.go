// Package health probes HTTP and TCP endpoints of logical services.
package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/state"
)

type Target struct {
	Service string
	URL     string
}

type Prober struct {
	store    *state.Store
	targets  []Target
	interval time.Duration
	timeout  time.Duration
	client   *http.Client
	trigger  chan struct{}
}

func New(store *state.Store, targets []Target, interval, timeout time.Duration, skipVerify bool) *Prober {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: skipVerify} //nolint:gosec // local dev certificates
	transport.DisableKeepAlives = true
	transport.DialContext = dial
	return &Prober{
		store:    store,
		targets:  targets,
		interval: interval,
		timeout:  timeout,
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		trigger: make(chan struct{}, 1),
	}
}

// Trigger requests an immediate probe round.
func (p *Prober) Trigger() {
	select {
	case p.trigger <- struct{}{}:
	default:
	}
}

func (p *Prober) Run(ctx context.Context) {
	if len(p.targets) == 0 {
		return
	}
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		p.probeAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.trigger:
		}
	}
}

func (p *Prober) probeAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, t := range p.targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := p.Probe(ctx, t.URL)
			if ctx.Err() == nil {
				p.store.Apply(state.SetHealth(t.Service, r))
			}
		}()
	}
	wg.Wait()
}

var (
	systemDialer = &net.Dialer{}
	// The macOS system resolver waits ~5s for multicast DNS before consulting
	// /etc/hosts for .local names, which exceeds typical probe timeouts.
	goDialer = &net.Dialer{Resolver: &net.Resolver{PreferGo: true}}
)

func dialerFor(addr string) *net.Dialer {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if strings.HasSuffix(strings.TrimSuffix(host, "."), ".local") {
		return goDialer
	}
	return systemDialer
}

func dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return dialerFor(addr).DialContext(ctx, network, addr)
}

// Probe checks one target. HTTP succeeds on 2xx/3xx, TCP on a successful connect.
func (p *Prober) Probe(ctx context.Context, target string) (r model.HealthResult) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	start := time.Now()
	r.Target = target
	defer func() {
		r.Latency = time.Since(start)
		r.Checked = time.Now()
	}()

	u, err := url.Parse(target)
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	if u.Scheme == "tcp" {
		conn, err := dial(ctx, "tcp", u.Host)
		if err != nil {
			r.Detail = err.Error()
			return r
		}
		_ = conn.Close()
		r.OK, r.Detail = true, "connected"
		return r
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	resp, err := p.client.Do(req)
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	r.OK = resp.StatusCode >= 200 && resp.StatusCode < 400
	r.Detail = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	return r
}
