package cmd

import "testing"

func TestBeside(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://nvoi.to":        "https://git.nvoi.to/",
		"https://nvoi.to/":       "https://git.nvoi.to/",
		"https://dev.nvoi.to":    "https://git-dev.nvoi.to/",
		"http://127.0.0.1:4790/": "http://git.localhost:4790/",
		"http://localhost:4000":  "http://git.localhost:4000/",
		"https://acme.dev":       "https://git.acme.dev/",
		"https://nvoi.acme.com":  "https://git-nvoi.acme.com/",
	} {
		if got := GitURL(endpoint); got != want {
			t.Errorf("%s: %s, want %s", endpoint, got, want)
		}
	}
}
