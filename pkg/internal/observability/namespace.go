package observability

// Namespace is the dedicated Kubernetes namespace every observability
// resource lands in. Keeps the default namespace (where app workloads
// live) free of monitoring noise. Sweep scope is strictly per-namespace
// so a stale stack object can never be confused with a stale app
// object.
const Namespace = "nvoi-observability"
