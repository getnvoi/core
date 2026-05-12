# PR 7 — `nvoi monitor` verb + kube port-forward

## Goal

Add the operator-facing `nvoi monitor` command. Opens an SSH-tunneled
port-forward from the operator's `localhost:<port>` to the in-cluster
Grafana Service. Blocks until ctrl-C. No public network exposure
required.

After this PR ships: operator runs `nvoi monitor`, browser opens to
`http://localhost:3000`, Grafana dashboard renders with everything
PR 6 provisioned.

## Scope

In-scope:
- `pkg/internal/kube/client.go` — `PortForward` method.
- `pkg/deploy/monitor.go` — `deploy.Monitor` lifecycle.
- `cmd/cli/monitor.go` — cobra adapter.
- `cmd/cli/root.go` — register the command.
- Bucket-aware `nvoi destroy` — drain observability buckets before
  tf-destroy (follow-up identified in PR 6).

Out of scope:
- Any new top-level config field.
- Auth changes beyond what PR 4 already configured (anonymous viewer
  when no domain, admin password when domain set).

## kube port-forward

`pkg/internal/kube/portforward.go` (new file in the `kube` package):

```go
package kube

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "net/url"

    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/tools/portforward"
    "k8s.io/client-go/transport/spdy"
)

// PortForward forwards localPort on the operator's machine to
// remotePort on a Pod backing svcName in ns. Resolves the Service to
// one of its Pods (first Ready endpoint), then opens a SPDY-over-HTTPS
// port-forward through the apiserver (which the kube.Client is
// already tunneling via SSH).
//
// Blocks until ctx is cancelled or the forward errors. Cleanly closes
// on ctx cancellation.
//
// stdout/stderr are sinks for the portforwarder's "Forwarding from
// ..." messages — pass nil to discard, or io.Discard for production
// use (we route operator-facing status via lg upstream of this).
func (c *Client) PortForward(ctx context.Context, ns, svcName string, localPort, remotePort int) error {
    // Resolve Service → backing Pod (first Ready).
    podName, err := c.firstReadyPodFor(ctx, ns, svcName)
    if err != nil { return fmt.Errorf("resolve pod for %s/%s: %w", ns, svcName, err) }

    // Build SPDY transport using the existing tunneled RESTClient.
    req := c.CS.CoreV1().RESTClient().Post().
        Resource("pods").
        Namespace(ns).
        Name(podName).
        SubResource("portforward")

    transport, upgrader, err := spdy.RoundTripperFor(c.cfg)
    if err != nil { return fmt.Errorf("spdy roundtripper: %w", err) }

    dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport},
        http.MethodPost, &url.URL{Scheme: "https", Path: req.URL().Path, Host: req.URL().Host})

    stopCh := make(chan struct{})
    readyCh := make(chan struct{})
    defer close(stopCh)

    fw, err := portforward.New(dialer,
        []string{fmt.Sprintf("%d:%d", localPort, remotePort)},
        stopCh, readyCh,
        io.Discard, io.Discard)
    if err != nil { return fmt.Errorf("create port-forwarder: %w", err) }

    errCh := make(chan error, 1)
    go func() { errCh <- fw.ForwardPorts() }()

    select {
    case <-ctx.Done():
        return ctx.Err()
    case err := <-errCh:
        return err
    case <-readyCh:
        // Forwarding established; now wait on ctx OR forwarder error.
        select {
        case <-ctx.Done():
            return ctx.Err()
        case err := <-errCh:
            return err
        }
    }
}

// firstReadyPodFor returns the name of a Ready Pod backing svcName.
// Reads Service.Spec.Selector and lists matching pods filtered to
// Ready=True.
func (c *Client) firstReadyPodFor(ctx context.Context, ns, svcName string) (string, error) {
    svc, err := c.CS.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
    if err != nil { return "", err }
    if len(svc.Spec.Selector) == 0 {
        return "", fmt.Errorf("service %s/%s has no selector", ns, svcName)
    }

    // Build label selector from svc.Spec.Selector.
    sel := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: svc.Spec.Selector})
    pods, err := c.CS.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
    if err != nil { return "", err }

    for _, p := range pods.Items {
        if isPodReady(p) { return p.Name, nil }
    }
    return "", fmt.Errorf("no Ready pod for service %s/%s", ns, svcName)
}

func isPodReady(p corev1.Pod) bool {
    for _, c := range p.Status.Conditions {
        if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
            return true
        }
    }
    return false
}
```

The `kube.Client` already holds `c.cfg *rest.Config` for the
SSH-tunneled apiserver connection — that config is reused for the SPDY
upgrade. The whole port-forward rides through the SAME SSH tunnel
that did `kubectl apply` earlier. No new network paths.

## Deploy lifecycle

`pkg/deploy/monitor.go`:

```go
package deploy

import (
    "context"
    "fmt"

    "github.com/getnvoi/core/pkg/internal/kube"
    "github.com/getnvoi/core/pkg/internal/observability"
    "github.com/getnvoi/core/pkg/log"
    "github.com/getnvoi/core/pkg/runtime"
    "github.com/getnvoi/core/pkg/ssh"
)

// Monitor opens a local tunnel to the in-cluster Grafana Service.
// Blocks until ctx is cancelled (typically by ctrl-C).
//
// localPort is the operator's chosen local port (default 3000 —
// matches Grafana's in-pod port for memorability).
//
// Pre-conditions:
//   - cfg.Monitor must be non-nil. Errors clearly otherwise.
//   - The cluster must exist (tofu state must report endpoints).
//     Errors with "cluster not deployed" otherwise.
func Monitor(ctx context.Context, rt *runtime.Runtime, localPort int) error {
    if rt.Cfg.Monitor == nil {
        return fmt.Errorf("nvoi monitor: monitor: not configured in nvoi.yaml")
    }

    return RunWithSession(ctx, rt, log.KindMonitor, func(ctx context.Context, s *Session) error {
        return s.OnPrimary(ctx, func(ctx context.Context, sh ssh.Shell) error {
            kc, err := kube.New(ctx, sh)
            if err != nil { return fmt.Errorf("kube tunnel: %w", err) }
            defer kc.Close()

            s.Lg.Info(fmt.Sprintf("forwarding http://localhost:%d → grafana.%s.svc:3000",
                localPort, observability.Namespace))
            s.Lg.Info("open http://localhost:" + fmt.Sprint(localPort) + " in your browser")
            s.Lg.Info("press ctrl-c to disconnect")

            return kc.PortForward(ctx, observability.Namespace, "grafana", localPort, 3000)
        })
    })
}
```

`RunWithSession` already handles compile + tofu init + endpoint read —
same path as other verbs. `OnPrimary` opens the SSH session to the
primary master.

## CLI verb

`cmd/cli/monitor.go`:

```go
package main

import (
    "github.com/spf13/cobra"

    "github.com/getnvoi/core/internal/cli"
    "github.com/getnvoi/core/pkg/deploy"
)

var monitorLocalPort int

func newMonitorCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "monitor",
        Short: "Open a local tunnel to the in-cluster Grafana dashboard",
        Long: `Tunnels http://localhost:<port> to the cluster's Grafana.
Requires monitor: to be configured in nvoi.yaml and a successful prior
deploy. Blocks until ctrl-C.`,
        Args: cobra.NoArgs,
        RunE: func(cmd *cobra.Command, _ []string) error {
            rt, err := cli.PrepareRuntime(cmd.Context(), configPath, jsonOutput)
            if err != nil { return err }
            return deploy.Monitor(cmd.Context(), rt, monitorLocalPort)
        },
    }
    cmd.Flags().IntVar(&monitorLocalPort, "port", 3000, "local port to bind")
    return cmd
}
```

`cmd/cli/root.go` — register:

```go
rootCmd.AddCommand(newMonitorCmd())
```

Goes next to `ssh.go`, `kubectl.go`, `exec.go`, `logs.go` in the
verbs section.

Signal handling: cobra's `cmd.Context()` already has the standard
signal-cancellation wired by `main.go`. ctrl-C → ctx cancel → PortForward
returns ctx.Err() → cobra returns it cleanly.

## `nvoi destroy` — bucket drain (follow-up from PR 6)

`pkg/deploy/destroy.go` — when `cfg.Monitor != nil` was set, drain the
two observability buckets BEFORE tf-destroy. Mirror the cert-manager
Certificate drain commit (`c6e4e19`) — buckets that own resources
in tofu state must be emptied before they can be deleted.

Add a helper:

```go
// drainObservabilityBuckets empties the -logs and -metrics buckets
// so the BucketProvider can delete them during tf-destroy. Best
// effort — buckets that don't exist (operator never ran a deploy
// with monitor:) are skipped silently.
func drainObservabilityBuckets(ctx context.Context, rt *runtime.Runtime, lg log.Log) error
```

Slotted into `Destroy` right after `tf-plan-destroy`, before
`tf-destroy`.

## Tests

`cmd/cli/monitor_test.go`:
- TestMonitorCmd_ErrorsWhenNoMonitorConfig — wire a fixture cfg with
  no `monitor:` → cmd returns the expected error message.
- TestMonitorCmd_FlagsRegistered — `--port` accepts an int.

`pkg/deploy/monitor_test.go`:
- TestMonitor_ErrorsOnNilMonitorConfig.
- TestMonitor_HappyPath — mock the Session's OnPrimary + a fake
  kube.Client; assert PortForward called with correct args.

`pkg/internal/kube/portforward_test.go`:
- TestFirstReadyPodFor — fake clientset with 3 pods (1 not-ready,
  2 ready); assert returns the first ready by name.
- TestFirstReadyPodFor_NoneReady — returns error.
- TestFirstReadyPodFor_NoSelector — returns error.

Full integration test (portforward against a real apiserver) is
untested-by-design per the existing convention (kube tunnel,
runner/tfexec, cmd/cli cobra wiring).

## Acceptance

1. `bin/test` green.
2. Manual end-to-end:
   - Deploy `examples/ha.yaml` with `monitor: {}` added.
   - Run `nvoi monitor` from the operator's machine.
   - Open `http://localhost:3000` — Grafana login (or anonymous
     viewer when no domain) renders.
   - Navigate to dashboards → all 5 dashboards present.
   - Per-service dashboard shows live data for the workloads in YAML.
3. ctrl-C cleanly terminates the verb (no hung sockets, no log
   spam).
4. `nvoi destroy` against the same cluster succeeds — buckets drained
   before tf-destroy.

## Decisions deferred to implementation

- Default local port — 3000 (Grafana's native port). Some operators
  already have something on 3000; `--port` flag covers them.
- Whether to auto-open the operator's browser. Decision: NO. Surprise
  side-effect for a CLI tool. Operator opens browser themselves.
- Whether `nvoi monitor` should accept a service argument
  (`nvoi monitor prometheus` → port-forward to Prom directly).
  Decision: NO for v1. Grafana is the surface; if operators need
  Prom directly, they `nvoi kubectl -- port-forward ...`.

## Follow-ups (NOT in this PR; track separately)

- Optional `nvoi monitor test-alerts` subcommand that fires a synthetic
  alert through Grafana's test-receiver API to verify the
  Slack/email/SMS plumbing works end-to-end without waiting for a
  real alert.
- Operator-tweakable notification routing (`monitor.routing:`) for
  per-severity / per-channel filtering.
- kube-state-metrics if not landed in PR 4.
- Per-service `metrics:` annotation field in `cfg.Services` to opt
  workloads into Prometheus scraping (operators who want their own
  custom metrics — currently only system metrics flow).
