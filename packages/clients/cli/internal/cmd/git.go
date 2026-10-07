package cmd

import (
	"net"
	"net/url"
	"strings"
)

// Beside is the host of one part of nvoi (git, mcp) next to an API endpoint, with its scheme and port:
// <part>.nvoi.to beside nvoi.to, <part>-dev.nvoi.to beside dev.nvoi.to, <part>.localhost beside a loopback
// address (a test stack). The server names its hosts the same way (config/*.exs).
func Beside(endpoint, part string) string {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || u.Host == "" {
		return endpoint
	}
	// As the server names them (Nvoi.Hosts): a domain's apex puts the part before it, a name under a domain puts it
	// beside it; a loopback address or localhost has the part under localhost.
	host := u.Hostname()
	labels := strings.Split(host, ".")
	switch {
	case host == "localhost" || net.ParseIP(host) != nil:
		host = part + ".localhost"
	case len(labels) == 2:
		host = part + "." + host
	default:
		host = part + "-" + labels[0] + "." + strings.Join(labels[1:], ".")
	}
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	return u.Scheme + "://" + host
}

// GitURL is where git reaches the repositories an endpoint hosts, as a prefix: how the CLI tells an nvoi remote
// (Repository) from any other. Git itself talks to it directly, with an nvoi token as its password.
func GitURL(endpoint string) string { return Beside(endpoint, "git") + "/" }
