// Package grafana is the ONLY place in the codebase where the Grafana
// wire format leaks. Portable providers.Receiver structs come in;
// Grafana provisioning YAML goes out. Swap Grafana for another
// notification system (Alertmanager direct, Vector, …) later → replace
// this package, not every provider impl.
//
// Three provisioning surfaces today:
//   - datasource.go     Prometheus (via Thanos Querier) + Loki
//                       datasources, mounted as a ConfigMap at
//                       /etc/grafana/provisioning/datasources/.
//   - contactpoint.go   Receiver → contact-point YAML; closed switch
//                       per Receiver.Type (slack, postmark, twilio).
//   - notificationpolicy.go  Default routing policy: every alert
//                            routes through every configured channel.
//
// All output is ConfigMaps in nvoi-observability namespace, owner-
// labeled by kc.ApplyOwned in the deploy phase.
package grafana
