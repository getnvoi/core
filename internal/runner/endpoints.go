package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Server is one entry in the `output "servers"` map. Mirrors the HCL
// shape every infra provider's emitter declares.
type Server struct {
	Role    string `json:"role"`
	IPv4    string `json:"ipv4"`
	Private string `json:"private,omitempty"` // empty for orphan-imported servers w/o network attachment
}

// APIEndpoint is the kube-apiserver entry point — single master IP in
// the non-HA case, LB IP in HA. Always emitted by the provider.
type APIEndpoint struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// Endpoints is the typed view of a provider's terraform outputs.
// Construction is uniform across providers; consumers (deploy.go, the
// install package) never branch on provider name.
type Endpoints struct {
	Servers     map[string]Server
	APIEndpoint APIEndpoint
	HA          bool
}

// Masters returns master server names sorted alphabetically.
func (e *Endpoints) Masters() []string {
	names := make([]string, 0, len(e.Servers))
	for name, s := range e.Servers {
		if s.Role == "master" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Workers returns worker server names sorted alphabetically.
func (e *Endpoints) Workers() []string {
	names := make([]string, 0, len(e.Servers))
	for name, s := range e.Servers {
		if s.Role == "worker" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// WorkerJoinTarget returns the address workers should join via:
// LB private IP when HA, primary master's private IP otherwise. Always
// a private IP — workers never reach the apiserver via public network.
func (e *Endpoints) WorkerJoinTarget(primaryMaster string) string {
	if e.HA {
		return e.APIEndpoint.Private
	}
	return e.Servers[primaryMaster].Private
}

// Endpoints reads `terraform output -json` and parses our uniform
// schema. Called after Apply.
func (r *Runner) Endpoints(ctx context.Context) (*Endpoints, error) {
	out, err := r.tf.Output(ctx)
	if err != nil {
		return nil, fmt.Errorf("terraform output: %w", err)
	}

	srvOut, ok := out["servers"]
	if !ok {
		return nil, fmt.Errorf(`terraform output "servers" missing — provider emitter must declare it`)
	}
	var servers map[string]Server
	if err := json.Unmarshal(srvOut.Value, &servers); err != nil {
		return nil, fmt.Errorf(`decode "servers" output: %w`, err)
	}

	apiOut, ok := out["api_endpoint"]
	if !ok {
		return nil, fmt.Errorf(`terraform output "api_endpoint" missing — provider emitter must declare it`)
	}
	var api APIEndpoint
	if err := json.Unmarshal(apiOut.Value, &api); err != nil {
		return nil, fmt.Errorf(`decode "api_endpoint" output: %w`, err)
	}

	ha := false
	if haOut, ok := out["ha"]; ok {
		_ = json.Unmarshal(haOut.Value, &ha)
	}

	return &Endpoints{Servers: servers, APIEndpoint: api, HA: ha}, nil
}
