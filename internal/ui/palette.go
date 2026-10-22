package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

type resourceKind int

const (
	kindPods resourceKind = iota
	kindDeployments
	kindHelm
	kindCompose
	kindContainers
	kindImages
	kindNodes
	kindEvents
)

var resourceKinds = []struct {
	kind    resourceKind
	name    string
	aliases []string
}{
	{kindPods, "pods", []string{"po", "pod"}},
	{kindDeployments, "deploy", []string{"deployments", "deployment", "dp"}},
	{kindHelm, "helm", []string{"releases", "hr"}},
	{kindCompose, "compose", []string{"dc"}},
	{kindContainers, "containers", []string{"ctr", "container", "docker"}},
	{kindImages, "images", []string{"img", "image"}},
	{kindNodes, "nodes", []string{"no", "node"}},
	{kindEvents, "events", []string{"ev", "event"}},
}

func (k resourceKind) String() string {
	for _, rk := range resourceKinds {
		if rk.kind == k {
			return rk.name
		}
	}
	return "?"
}

// namespaced reports whether the namespace filter applies to the kind.
func (k resourceKind) namespaced() bool {
	return k == kindPods || k == kindDeployments || k == kindHelm || k == kindEvents
}

type commandType int

const (
	cmdResource commandType = iota
	cmdServices
	cmdNamespace
	cmdHelp
	cmdQuit
)

type command struct {
	typ  commandType
	kind resourceKind
	arg  string
}

var otherCommands = map[string]commandType{
	"services": cmdServices, "svc": cmdServices, "service": cmdServices,
	"ns": cmdNamespace, "namespace": cmdNamespace,
	"help": cmdHelp, "?": cmdHelp,
	"quit": cmdQuit, "q": cmdQuit, "q!": cmdQuit,
}

// parseCommand interprets palette input such as "pods", "ns platform" or "ns all".
func parseCommand(input string) (command, error) {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(input), ":"))
	if len(fields) == 0 {
		return command{}, fmt.Errorf("empty command")
	}
	name := strings.ToLower(fields[0])
	arg := strings.Join(fields[1:], " ")
	for _, rk := range resourceKinds {
		if name == rk.name || contains(rk.aliases, name) {
			return command{typ: cmdResource, kind: rk.kind, arg: arg}, nil
		}
	}
	if typ, ok := otherCommands[name]; ok {
		if typ == cmdNamespace && arg == "" {
			return command{}, fmt.Errorf("usage: ns <name|all>")
		}
		return command{typ: typ, arg: arg}, nil
	}
	return command{}, fmt.Errorf("unknown command %q", name)
}

func commandNames() []string {
	names := []string{"services", "ns", "help", "quit"}
	for _, rk := range resourceKinds {
		names = append(names, rk.name)
	}
	sort.Strings(names)
	return names
}

// completions returns candidates for the current input; namespaces complete the ns argument.
func completions(input string, namespaces []string) []string {
	input = strings.TrimLeft(input, " ")
	if name, arg, hasArg := strings.Cut(input, " "); hasArg {
		if typ, ok := otherCommands[strings.ToLower(name)]; ok && typ == cmdNamespace {
			var out []string
			for _, ns := range append([]string{"all"}, namespaces...) {
				if strings.HasPrefix(ns, strings.TrimSpace(arg)) {
					out = append(out, name+" "+ns)
				}
			}
			return out
		}
		return nil
	}
	var out []string
	for _, n := range commandNames() {
		if strings.HasPrefix(n, strings.ToLower(input)) {
			out = append(out, n)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

type palette struct {
	input  textinput.Model
	active bool
	err    string
}

func newPalette() palette {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.CharLimit = 80
	ti.ShowSuggestions = true
	return palette{input: ti}
}

func (p *palette) open() {
	p.active, p.err = true, ""
	p.input.SetValue("")
	p.input.Focus()
}

func (p *palette) close() {
	p.active = false
	p.input.Blur()
}
