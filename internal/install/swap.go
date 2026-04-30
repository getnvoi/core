package install

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/ssh"
)

// EnsureSwap allocates /swapfile sized 5% of root disk, clamped
// 512MB-2GB, and registers it in /etc/fstab. Idempotent: skips if
// swap is already active. Mirrors nvoi/pkg/infra/cloudinit.go::EnsureSwap.
func EnsureSwap(ctx context.Context, sh *ssh.Client, lg log.Log) error {
	out, err := sh.Run(ctx, "swapon --show --noheadings")
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return nil // already active
	}

	out, err = sh.Run(ctx, "df --output=size / | tail -1")
	if err != nil {
		return fmt.Errorf("read root disk size: %w", err)
	}
	var diskKB int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &diskKB)
	swapMB := computeSwapMB(diskKB / 1024 / 1024)

	steps := []struct {
		label string
		cmd   string
	}{
		{"allocating /swapfile", fmt.Sprintf("sudo fallocate -l %dM /swapfile", swapMB)},
		{"chmod 600 /swapfile", "sudo chmod 600 /swapfile"},
		{"mkswap /swapfile", "sudo mkswap /swapfile"},
		{"swapon /swapfile", "sudo swapon /swapfile"},
		{"register in /etc/fstab", `sudo bash -c "grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab"`},
	}
	lg.Info(fmt.Sprintf("allocating %dMB swap on %s...", swapMB, sh.Addr()))
	for _, s := range steps {
		if err := sh.RunStream(ctx, s.cmd, lg.Stream(), lg.Stream()); err != nil {
			return fmt.Errorf("swap %s: %w", s.label, err)
		}
	}
	return nil
}

// computeSwapMB: 5% of disk, clamped 512-2048.
func computeSwapMB(diskGB int) int {
	if diskGB <= 0 {
		diskGB = 20
	}
	mb := diskGB * 1024 * 5 / 100
	if mb < 512 {
		mb = 512
	}
	if mb > 2048 {
		mb = 2048
	}
	return mb
}
