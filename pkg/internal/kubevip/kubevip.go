// Package kubevip renders the kube-vip ARP-mode static-pod manifest +
// RBAC for k3s HA in tunnel mode.
//
// Topology context: HA + tunnel mode drops the hcloud LB entirely.
// kube-vip on each master elects a leader via a kube-system Lease,
// the winner ARP-claims the private-subnet VIP, every kubelet/worker
// dials https://<vip>:6443 for the apiserver. Failover swings the
// VIP to another master in ~5-10s.
//
// Two artifacts:
//
//   Manifest(vip, iface) → []byte
//     Per-master Pod spec dropped at
//     /var/lib/rancher/k3s/server/manifests/kube-vip.yaml BEFORE
//     k3s starts. k3s auto-applies anything in that directory on
//     startup, so the kube-vip pod comes up alongside the apiserver.
//
//   RBAC() → []byte
//     Cluster-scoped ServiceAccount + ClusterRole + ClusterRoleBinding
//     applied ONCE after the primary master is up. Idempotent
//     re-apply on every deploy is fine.
//
// Pinned version in `Version` — bump via PR.
package kubevip

import (
	"bytes"
	"fmt"
	"text/template"
)

// Version is the pinned kube-vip image tag. The image lives at
// ghcr.io/kube-vip/kube-vip. Bumping is a one-line change; review
// the kube-vip release notes for breaking env-var renames before
// updating.
const Version = "v0.8.0"

// Image is the fully-qualified container reference.
const Image = "ghcr.io/kube-vip/kube-vip:" + Version

// staticPodTpl renders the per-master Pod. hostNetwork + NET_ADMIN/
// NET_RAW are required for ARP. The Lease tuning (5s lease, 3s renew
// deadline, 1s retry) keeps failover well under the 10s ballpark we
// promised in the architecture write-up.
const staticPodTpl = `apiVersion: v1
kind: Pod
metadata:
  name: kube-vip
  namespace: kube-system
spec:
  hostNetwork: true
  containers:
  - name: kube-vip
    image: {{ .Image }}
    imagePullPolicy: IfNotPresent
    args: ["manager"]
    securityContext:
      capabilities:
        add: ["NET_ADMIN", "NET_RAW"]
    env:
    - { name: vip_arp,            value: "true" }
    - { name: vip_leaderelection, value: "true" }
    - { name: cp_enable,          value: "true" }
    - { name: cp_namespace,       value: "kube-system" }
    - { name: svc_enable,         value: "false" }
    - { name: vip_leaseduration,  value: "5" }
    - { name: vip_renewdeadline,  value: "3" }
    - { name: vip_retryperiod,    value: "1" }
    - { name: port,               value: "6443" }
    - { name: vip_interface,      value: "{{ .Interface }}" }
    - { name: vip_cidr,           value: "32" }
    - { name: address,            value: "{{ .VIP }}" }
    volumeMounts:
    - { name: kubeconfig, mountPath: /etc/kubernetes/admin.conf }
  hostAliases:
  - ip: 127.0.0.1
    hostnames: ["kubernetes"]
  volumes:
  - name: kubeconfig
    hostPath:
      path: /etc/rancher/k3s/k3s.yaml
`

// rbacYAML is the cluster-scoped RBAC kube-vip needs to read Nodes,
// Services, Endpoints, EndpointSlices, and the coordination Lease for
// leader election. Applied once via kubectl post-bootstrap.
const rbacYAML = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-vip
  namespace: kube-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  annotations:
    rbac.authorization.kubernetes.io/autoupdate: "true"
  name: system:kube-vip-role
rules:
- apiGroups: [""]
  resources: ["services/status"]
  verbs: ["update"]
- apiGroups: [""]
  resources: ["services", "endpoints"]
  verbs: ["list", "get", "watch", "update", "create"]
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["list", "get", "watch", "update", "patch"]
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["list", "get", "watch", "update", "create"]
- apiGroups: ["discovery.k8s.io"]
  resources: ["endpointslices"]
  verbs: ["list", "get", "watch", "update"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:kube-vip-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:kube-vip-role
subjects:
- kind: ServiceAccount
  name: kube-vip
  namespace: kube-system
`

var staticPodT = template.Must(template.New("kubevip").Parse(staticPodTpl))

// ManifestSpec is the inputs Manifest renders into the static pod.
// Bundled so the signature stays narrow (>4-args rule).
type ManifestSpec struct {
	VIP       string // private-subnet IP kube-vip ARP-claims
	Interface string // network interface name (e.g. "eth0") on each master
}

// Manifest renders the per-master static-pod YAML. Pinned image,
// hostNetwork, ARP mode. The same manifest goes on EVERY master; they
// elect among themselves via Lease.
func Manifest(spec ManifestSpec) ([]byte, error) {
	if spec.VIP == "" {
		return nil, fmt.Errorf("kubevip.Manifest: VIP required")
	}
	if spec.Interface == "" {
		return nil, fmt.Errorf("kubevip.Manifest: Interface required")
	}
	var buf bytes.Buffer
	if err := staticPodT.Execute(&buf, struct {
		Image, VIP, Interface string
	}{Image, spec.VIP, spec.Interface}); err != nil {
		return nil, fmt.Errorf("render kube-vip manifest: %w", err)
	}
	return buf.Bytes(), nil
}

// RBAC returns the cluster-scoped RBAC YAML. Constant — no inputs.
// Applied once via kubectl after the primary master is up; subsequent
// deploys re-apply idempotently.
func RBAC() []byte { return []byte(rbacYAML) }
