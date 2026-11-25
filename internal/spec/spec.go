// Package spec builds the sandbox spec every Runner is created from.
package spec

import (
	"time"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/arteven/gh-runners-msb/internal/identity"
)

// DefaultImage is the published Runner image (Image contract 1).
const DefaultImage = "ghcr.io/arteven/gh-runners-msb:1"

// Sizes are the per-Runner resources, set by the up sizing flags. Defaults is
// what pool.Run falls back to when Sizes is zero.
type Sizes struct {
	CPUs      uint8
	MemoryMiB uint32
	DiskMiB   uint32 // writable budget: root + docker disk
	DockerMiB uint32
}

// Defaults are the default Runner sizes.
var Defaults = Sizes{CPUs: 2, MemoryMiB: 4096, DiskMiB: 5120, DockerMiB: 2048}

// Spec is a plain value so tests can compare it; Options maps it onto the SDK.
type Spec struct {
	Image       string
	PullPolicy  microsandbox.PullPolicy
	CPUs        uint8
	MemoryMiB   uint32
	RootMiB     uint32
	DockerMiB   uint32
	Labels      map[string]string
	MaxDuration time.Duration
	Ephemeral   bool
}

// Local is the spec for a Runner on the local Backend: root is the disk minus
// the docker disk, which is a separate owned ext4 volume at /var/lib/docker.
// The subtraction is only safe because the CLI guarantees DockerMiB < DiskMiB.
func Local(s Sizes, image, poolID string) Spec {
	return Spec{
		Image:       image,
		PullPolicy:  microsandbox.PullPolicyIfMissing,
		CPUs:        s.CPUs,
		MemoryMiB:   s.MemoryMiB,
		RootMiB:     s.DiskMiB - s.DockerMiB,
		DockerMiB:   s.DockerMiB,
		Labels:      map[string]string{identity.LabelKey: poolID},
		MaxDuration: 24 * time.Hour,
		Ephemeral:   true,
	}
}

// Options maps the Spec onto SDK options.
//
// Deliberately never used: WithDetached (a Runner must die with the Pool),
// WithEnv and WithSecrets (the gh token and JIT config never enter a VM as
// env), WithReplace (names are unique; a collision is a bug), WithHostname.
func (s Spec) Options() []microsandbox.SandboxOption {
	opts := []microsandbox.SandboxOption{
		microsandbox.WithImage(s.Image),
		microsandbox.WithPullPolicy(s.PullPolicy),
		microsandbox.WithCPUs(s.CPUs),
		microsandbox.WithMemory(s.MemoryMiB),
		microsandbox.WithRootDisk(microsandbox.RootDisk.Managed(s.RootMiB)),
		microsandbox.WithMounts(map[string]microsandbox.MountConfig{
			"/var/lib/docker": microsandbox.Mount.Owned(microsandbox.OwnedVolumeOptions{
				Kind:    microsandbox.VolumeKindDisk,
				SizeMiB: s.DockerMiB,
			}),
		}),
		microsandbox.WithEphemeral(s.Ephemeral),
		microsandbox.WithMaxDuration(s.MaxDuration),
		publicEgress(),
	}
	for k, v := range s.Labels {
		opts = append(opts, microsandbox.WithLabel(k, v))
	}
	return opts
}

// publicEgress confines a Runner to the public internet: it can reach GitHub
// and registries, but not the host, other Runners, the LAN, loopback or the
// cloud metadata endpoint.
//
// Rules are first match wins, so the order is the policy:
//  1. DNS to the host's gateway resolver. It must precede the host deny, or the
//     in-VM DNS proxy could not resolve anything.
//  2. Deny the reserved groups. "private" also covers CGNAT, fc00::/7 and the
//     host's on-link prefixes, which is what keeps Runners apart.
//  3. Allow "public", the complement of the reserved ranges.
//
// Only groups and CIDRs, never DenyDomains: domain rules would need TLS
// interception, which is out of scope. DefaultIngress stays allow, as in the
// SDK's profiles; no ports are published.
func publicEgress() microsandbox.SandboxOption {
	rules := []microsandbox.PolicyRule{microsandbox.Rule.AllowDNS()}
	for _, group := range []string{"loopback", "private", "link-local", "metadata", "host", "multicast"} {
		rules = append(rules, microsandbox.PolicyRule{
			Action:      microsandbox.PolicyActionDeny,
			Direction:   microsandbox.PolicyDirectionEgress,
			Destination: group,
		})
	}
	rules = append(rules, microsandbox.PolicyRule{
		Action:      microsandbox.PolicyActionAllow,
		Direction:   microsandbox.PolicyDirectionEgress,
		Destination: "public",
	})
	return microsandbox.WithNetwork(&microsandbox.NetworkConfig{
		Rules:          rules,
		DefaultEgress:  microsandbox.PolicyActionDeny,
		DefaultIngress: microsandbox.PolicyActionAllow,
	})
}
