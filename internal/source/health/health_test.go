package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/swehacker/lazydev/internal/state"
)

func TestProbeHTTP(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusNoContent)
		case "/redirect":
			http.Redirect(w, r, "/login", http.StatusFound)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	p := New(state.New(), nil, time.Second, time.Second, true)
	ctx := context.Background()
	if r := p.Probe(ctx, srv.URL+"/ok"); !r.OK || r.Detail != "204 No Content" || r.Latency <= 0 || r.Checked.IsZero() {
		t.Errorf("ok probe = %+v", r)
	}
	if r := p.Probe(ctx, srv.URL+"/redirect"); !r.OK || r.Detail != "302 Found" {
		t.Errorf("redirect probe = %+v", r)
	}
	if r := p.Probe(ctx, srv.URL+"/down"); r.OK || r.Detail != "503 Service Unavailable" {
		t.Errorf("down probe = %+v", r)
	}

	strict := New(state.New(), nil, time.Second, time.Second, false)
	if r := strict.Probe(ctx, srv.URL+"/ok"); r.OK {
		t.Error("self-signed certificate should fail without insecureSkipVerify")
	}
}

func TestProbeTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	p := New(state.New(), nil, time.Second, 500*time.Millisecond, true)
	if r := p.Probe(context.Background(), "tcp://"+addr); !r.OK {
		t.Errorf("tcp probe = %+v", r)
	}
	_ = ln.Close()
	if r := p.Probe(context.Background(), "tcp://"+addr); r.OK {
		t.Error("closed port should fail")
	}
}

func TestLocalNamesBypassSystemResolver(t *testing.T) {
	for addr, want := range map[string]*net.Dialer{
		"api.example.local:443":  goDialer,
		"api.example.local.:443": goDialer,
		"localhost:5432":         systemDialer,
		"example.com:443":        systemDialer,
		"127.0.0.1:80":           systemDialer,
	} {
		if dialerFor(addr) != want {
			t.Errorf("dialerFor(%q) picked the wrong resolver", addr)
		}
	}
}

func TestRunStoresResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	store := state.New()
	p := New(store, []Target{{Service: "api", URL: srv.URL}}, time.Hour, time.Second, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-store.Changed():
		case <-deadline:
			t.Fatal("no health result stored")
		}
		if r, ok := store.Snapshot().Health["api"]; ok {
			if !r.OK {
				t.Errorf("result = %+v", r)
			}
			break
		}
	}
	cancel()
	<-done
}
