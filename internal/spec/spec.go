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

// publicEgress lets a Runner reach GitHub and registries on the public
// internet. msb's default is egress deny, which would leave a Runner unable to
// register. The full deny list is a later ticket (#33); keep this one function.
func publicEgress() microsandbox.SandboxOption {
	return microsandbox.WithNetwork(microsandbox.NetworkPolicy.FromProfiles(microsandbox.NetworkProfilePublic))
}
