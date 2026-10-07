package proc

import (
	"fmt"
	"os"
	"os/user"
	"strings"
	"strconv"
	"syscall"
)

// Who a command runs as. Root by default, because dockerd needs it; the unprivileged user exists
// because the vendor cli refuses its autonomy modes under root. Groups come with the uid — dropping
// uid alone leaves a process in root's groups, which is not dropping.
func credentialFor(name string) (*syscall.Credential, *user.User, error) {
	if name == "" {
		return nil, nil, nil
	}

	u, err := user.Lookup(name)
	if err != nil {
		return nil, nil, fmt.Errorf("no user %q in this image: %w", name, err)
	}

	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, nil, err
	}

	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, nil, err
	}

	groups, err := u.GroupIds()
	if err != nil {
		return nil, nil, err
	}

	// Refused rather than skipped. A group quietly left out is a process running
	// with fewer than it was given — a missing `docker` gid surfaces later as a
	// permission error on a socket, blamed on the daemon.
	extra := make([]uint32, 0, len(groups))
	for _, g := range groups {
		id, err := strconv.ParseUint(g, 10, 32)
		if err != nil {
			return nil, nil, fmt.Errorf("user %q is in group %q, which is not a gid: %w", name, g, err)
		}
		extra = append(extra, uint32(id))
	}

	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: extra}, u, nil
}

// The user's own HOME, USER and LOGNAME over what the command was given: boxd's are root's, and a login
// shell of another user reading /root fails.
func envFor(env []string, u *user.User) []string {
	if u == nil {
		return env
	}
	if env == nil {
		env = os.Environ()
	}
	own := map[string]string{"HOME": u.HomeDir, "USER": u.Username, "LOGNAME": u.Username}
	out := make([]string, 0, len(env)+len(own))
	for _, kv := range env {
		if _, ok := own[strings.SplitN(kv, "=", 2)[0]]; !ok {
			out = append(out, kv)
		}
	}
	for k, v := range own {
		out = append(out, k+"="+v)
	}
	return out
}

// As is who a process outside the table runs as (boxd's shells): its credential, and env with the user's own HOME,
// USER and LOGNAME. Empty name: boxd's own user, env as given.
func As(name string, env []string) (*syscall.Credential, []string, error) {
	cred, u, err := credentialFor(name)
	if err != nil {
		return nil, nil, err
	}
	return cred, envFor(env, u), nil
}
