package ui

import (
	"reflect"
	"testing"
)

func TestParseCommand(t *testing.T) {
	cases := map[string]command{
		"pods":         {typ: cmdResource, kind: kindPods},
		":po":          {typ: cmdResource, kind: kindPods},
		"deploy":       {typ: cmdResource, kind: kindDeployments},
		"img":          {typ: cmdResource, kind: kindImages},
		"svc":          {typ: cmdServices},
		"ns platform":  {typ: cmdNamespace, arg: "platform"},
		"  ns   all  ": {typ: cmdNamespace, arg: "all"},
		"q":            {typ: cmdQuit},
	}
	for in, want := range cases {
		got, err := parseCommand(in)
		if err != nil || got != want {
			t.Errorf("parseCommand(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "bogus", "ns"} {
		if _, err := parseCommand(bad); err == nil {
			t.Errorf("parseCommand(%q) should fail", bad)
		}
	}
}

func TestCompletions(t *testing.T) {
	if got := completions("de", nil); !reflect.DeepEqual(got, []string{"deploy"}) {
		t.Errorf("completions(de) = %v", got)
	}
	if got := completions("ns pl", []string{"platform", "billing"}); !reflect.DeepEqual(got, []string{"ns platform"}) {
		t.Errorf("completions(ns pl) = %v", got)
	}
	if got := completions("ns ", []string{"platform"}); !reflect.DeepEqual(got, []string{"ns all", "ns platform"}) {
		t.Errorf("completions(ns ) = %v", got)
	}
	if got := completions("pods x", nil); got != nil {
		t.Errorf("resource commands take no completion args, got %v", got)
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := humanBytes(1536); got != "1.5K" {
		t.Errorf("humanBytes = %q", got)
	}
	if got := humanBytes(17179869184); got != "16.0G" {
		t.Errorf("humanBytes = %q", got)
	}
	if got := humanCPU(250); got != "250m" {
		t.Errorf("humanCPU = %q", got)
	}
	if got := humanCPU(1500); got != "1.50" {
		t.Errorf("humanCPU = %q", got)
	}
	if got := sparkline([]float64{0, 5, 10}, 5, 0); got != "  ▁▄█" {
		t.Errorf("sparkline = %q", got)
	}
	if got := sparkline([]float64{500, 500}, 2, 0); got != "▄▄" {
		t.Errorf("flat sparkline = %q", got)
	}
	if got := sparkline([]float64{100, 120}, 2, 1000); got != "▁▁" {
		t.Errorf("ceiling sparkline = %q", got)
	}
	if got := fit("abcdef", 4); got != "abc…" {
		t.Errorf("fit = %q", got)
	}
	if got := fit("ab", 4); got != "ab  " {
		t.Errorf("fit = %q", got)
	}
}
