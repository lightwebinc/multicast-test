package env

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// burstTable is the nftables table ApplyBurstDrop installs in a container's
// network namespace.
const burstTable = "harness_burst"

// ApplyBurstDrop drops the next n data-plane datagrams (UDP destination port
// 9001) that reach the named container, as one contiguous run: a counter in
// the prerouting hook drops while it reads below n, then passes everything.
// netem drops at random; this is the shape a NIC ring or a socket overflow
// leaves, a burst of consecutive frames of every flow on the port. The host's
// nft runs in the container's network namespace, as ApplyNetemLoss runs tc.
func ApplyBurstDrop(ctx context.Context, containerName string, n int) error {
	return addBurstRule(ctx, containerName, nil, n)
}

// ApplyBurstDropFrom is ApplyBurstDrop for the datagrams from one source
// address only, with its own counter: a retransmission endpoint's repair
// copies, say, apart from the emitter's frames. Several rules may be added;
// each drops its own first n.
func ApplyBurstDropFrom(ctx context.Context, containerName, source string, n int) error {
	return addBurstRule(ctx, containerName, []string{"ip6", "saddr", source}, n)
}

func addBurstRule(ctx context.Context, containerName string, match []string, n int) error {
	if n <= 0 {
		return fmt.Errorf("burst size %d", n)
	}
	rule := append([]string{"nft", "add", "rule", "inet", burstTable, "pre"}, match...)
	rule = append(rule, "udp", "dport", "9001", "numgen", "inc", "mod", "4000000000", "<", strconv.Itoa(n), "counter", "drop")
	steps := [][]string{
		{"nft", "add", "table", "inet", burstTable},
		{"nft", "add", "chain", "inet", burstTable, "pre", "{", "type", "filter", "hook", "prerouting", "priority", "-300", ";", "policy", "accept", ";", "}"},
		rule,
	}
	for _, s := range steps {
		if err := nsenterRun(ctx, containerName, s...); err != nil {
			return err
		}
	}
	return nil
}

var nftPackets = regexp.MustCompile(`counter packets (\d+)`)

// BurstDropped reports how many datagrams the burst rules have dropped so far,
// one count per rule in the order they were added.
func BurstDropped(ctx context.Context, containerName string) ([]int, error) {
	out, err := nsenterOutput(ctx, containerName, "nft", "list", "table", "inet", burstTable)
	if err != nil {
		return nil, err
	}
	var counts []int
	for _, m := range nftPackets.FindAllStringSubmatch(out, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, err
		}
		counts = append(counts, n)
	}
	if len(counts) == 0 {
		return nil, fmt.Errorf("no counter in %q", out)
	}
	return counts, nil
}

// RemoveBurstDrop deletes the burst rule and checks that it is gone.
func RemoveBurstDrop(ctx context.Context, containerName string) error {
	_ = nsenterRun(ctx, containerName, "nft", "delete", "table", "inet", burstTable)
	out, err := nsenterOutput(ctx, containerName, "nft", "list", "tables")
	if err != nil {
		return err
	}
	if strings.Contains(out, burstTable) {
		return fmt.Errorf("burst rule still present in %s", containerName)
	}
	return nil
}

// PauseContainer freezes every process of the named container (docker pause,
// the cgroup freezer). The kernel keeps receiving for it: datagrams queue in
// its sockets until UnpauseContainer, as they do for a receiver that has fallen
// behind.
func PauseContainer(ctx context.Context, containerName string) error {
	return dockerRun(ctx, "pause", containerName)
}

// UnpauseContainer resumes a container frozen by PauseContainer.
func UnpauseContainer(ctx context.Context, containerName string) error {
	return dockerRun(ctx, "unpause", containerName)
}

func dockerRun(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %v: %w: %s", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// nsenterOutput is nsenterRun returning the command's output.
func nsenterOutput(ctx context.Context, containerName string, args ...string) (string, error) {
	pid, err := containerPID(ctx, containerName)
	if err != nil {
		return "", err
	}
	nsArgs := append([]string{"--net=/proc/" + pid + "/ns/net"}, args...)
	out, err := exec.CommandContext(ctx, "nsenter", nsArgs...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("nsenter %v: %w\n%s", args, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
