package postgres

import "github.com/getnvoi/core/pkg/providers"

// init registers the postgres engine with the database provider
// registry. Singleton — every call to ResolveDatabase("postgres", …)
// returns the same Provider value (stateless).
//
// No credential schema: postgres is selfhosted, so there are no
// vendor API tokens to resolve. The operator's $VAR-resolved
// credentials (databases.X.credentials.{user,password,database})
// flow through req.Spec, not through the registry's creds map.
func init() {
	providers.RegisterDatabase("postgres", providers.CredentialSchema{
		Name:   "postgres",
		Fields: nil,
	}, func(_ map[string]string) providers.DatabaseProvider {
		return &Provider{}
	})
}
