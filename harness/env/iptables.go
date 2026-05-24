package env

import (
	"context"
	"fmt"
)

// BlockUDPIngress adds an ip6tables rule in the container's netns that drops
// all incoming UDP packets to the specified port. Used to simulate a retry
// endpoint whose multicast ingress is blocked (cache stays empty → MISS).
func BlockUDPIngress(ctx context.Context, containerName string, port int) error {
	return nsenterRun(ctx, containerName,
		"ip6tables", "-I", "INPUT", "1",
		"-p", "udp", "--dport", fmt.Sprintf("%d", port),
		"-j", "DROP")
}

// UnblockUDPIngress removes the ip6tables DROP rule added by BlockUDPIngress.
func UnblockUDPIngress(ctx context.Context, containerName string, port int) error {
	return nsenterRun(ctx, containerName,
		"ip6tables", "-D", "INPUT",
		"-p", "udp", "--dport", fmt.Sprintf("%d", port),
		"-j", "DROP")
}
