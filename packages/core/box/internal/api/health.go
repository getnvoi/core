package api

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"
)

// Whether the daemon this image exists for is up: the substrate says `running` well before the
// entrypoint's dockerd answers. From in here that is one dial, not `docker info` over an exec.
const dockerSocket = "/var/run/docker.sock"

// Short: this is polled while a person waits for a checkout, and a daemon that
// has not answered in a second has not answered.
const dockerTimeout = time.Second

func dockerUp() bool {
	if _, err := os.Stat(dockerSocket); err != nil {
		return false
	}

	client := &http.Client{
		Timeout: dockerTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: dockerTimeout}).DialContext(ctx, "unix", dockerSocket)
			},
		},
	}

	// The socket existing is not the daemon answering: dockerd creates it and then spends
	// seconds loading plugins, and a connect succeeds throughout. `_ping` is the smallest thing
	// only a working daemon can answer.
	response, err := client.Get("http://docker/_ping")
	if err != nil {
		return false
	}
	defer response.Body.Close()

	return response.StatusCode == http.StatusOK
}
