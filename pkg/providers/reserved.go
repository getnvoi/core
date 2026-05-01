package providers

// Per-infra reserved-name registry. Each infra provider's init()
// declares which YAML server keys would collide with non-server
// resources in its rendered HCL (hetzner uses "default" for the
// network/firewall/subnet, "cp" for the load balancer, etc.). The
// validator queries this registry instead of holding the list itself
// so config/ stays free of provider-specific knowledge.
//
// Lives in providers/ (not compile/) because compile imports config —
// adding a config → compile dependency would create a cycle. Pattern
// mirrors RegisterBucket / IsRegisteredBucket: a tiny parallel
// registry populated via init().

var reservedServerNames = map[string]map[string]bool{}

// RegisterReservedServerNames is called from an infra provider's
// init() to declare YAML server keys that must not appear in a
// `servers:` block. Names are scoped per provider — the validator
// only consults the set for the active providers.infra.
//
// Empty / nil sets are valid: a provider with no reserved keys can
// skip registration entirely (the lookup returns nil).
func RegisterReservedServerNames(provider string, names map[string]bool) {
	if len(names) == 0 {
		return
	}
	reservedServerNames[provider] = names
}

// ReservedServerNames returns the reserved-name set for the named
// infra provider. Returns nil when no set was registered — interpret
// that as "no reservations." Validator treats nil as a permissive
// (empty) set.
func ReservedServerNames(provider string) map[string]bool {
	return reservedServerNames[provider]
}
