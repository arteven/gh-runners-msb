package spec

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	microsandbox "github.com/superradcompany/microsandbox/sdk/go"
)

func TestLocalDefaults(t *testing.T) {
	got := Local(Defaults, "img:1", "abc123")
	want := Spec{
		Image:       "img:1",
		PullPolicy:  microsandbox.PullPolicyIfMissing,
		CPUs:        2,
		MemoryMiB:   4096,
		RootMiB:     3072, // 5 GiB disk - 2 GiB docker disk
		DockerMiB:   2048,
		Labels:      map[string]string{"gh-runners-msb.pool": "abc123"},
		MaxDuration: 24 * time.Hour,
		Ephemeral:   true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestLocalFollowsSizes(t *testing.T) {
	got := Local(Sizes{CPUs: 4, MemoryMiB: 8192, DiskMiB: 20480, DockerMiB: 10240}, "img:1", "abc123")
	if got.CPUs != 4 || got.MemoryMiB != 8192 || got.RootMiB != 10240 || got.DockerMiB != 10240 {
		t.Errorf("got %+v", got)
	}
}

func TestOptionsCarrySizes(t *testing.T) {
	sizes := Sizes{CPUs: 4, MemoryMiB: 8192, DiskMiB: 20480, DockerMiB: 6144}
	var cfg microsandbox.SandboxConfig
	for i, o := range Local(sizes, "img:1", "abc123").Options() {
		if o == nil {
			t.Fatalf("option %d is nil", i)
		}
		o(&cfg)
	}
	if cfg.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", cfg.CPUs)
	}
	if cfg.MemoryMiB != 8192 {
		t.Errorf("MemoryMiB = %d, want 8192", cfg.MemoryMiB)
	}
	if cfg.RootDisk == nil || cfg.RootDisk.SizeMiB != 14336 { // 20 GiB - 6 GiB
		t.Errorf("RootDisk = %+v, want SizeMiB 14336", cfg.RootDisk)
	}
	vol, ok := cfg.Volumes["/var/lib/docker"]
	if !ok {
		t.Fatalf("no /var/lib/docker volume in %+v", cfg.Volumes)
	}
	if vol.SizeMiB != 6144 {
		t.Errorf("docker volume SizeMiB = %d, want 6144", vol.SizeMiB)
	}
	if vol.Owned != string(microsandbox.VolumeKindDisk) {
		t.Errorf("docker volume Owned = %q, want %q", vol.Owned, microsandbox.VolumeKindDisk)
	}
}

// The Runner network policy is explicit rules only: group names and CIDRs,
// never domains (they would need TLS interception, which is out of scope).
func TestRunnerNetworkPolicyIsPublicOnly(t *testing.T) {
	groups := map[string]bool{"host": true, "loopback": true, "private": true, "link-local": true, "metadata": true, "multicast": true, "public": true}
	// One row per Backend constructor; Remote joins when it lands.
	for name, s := range map[string]Spec{
		"local": Local(Defaults, "img:1", "abc123"),
	} {
		t.Run(name, func(t *testing.T) {
			var cfg microsandbox.SandboxConfig
			for _, o := range s.Options() {
				o(&cfg)
			}
			n := cfg.Network
			if n == nil {
				t.Fatal("no network config")
			}
			if len(n.DenyDomains) != 0 || len(n.DenyDomainSuffixes) != 0 {
				t.Errorf("domain rules set: %v %v", n.DenyDomains, n.DenyDomainSuffixes)
			}
			if n.DefaultEgress != microsandbox.PolicyActionDeny {
				t.Errorf("DefaultEgress = %q, want deny", n.DefaultEgress)
			}
			if len(n.Rules) == 0 || !reflect.DeepEqual(n.Rules[0], microsandbox.Rule.AllowDNS()) {
				t.Fatalf("first rule must be the DNS allow to the host, rules: %+v", n.Rules)
			}
			allowPublic := -1
			denied := map[string]int{}
			for i, r := range n.Rules {
				if r.Direction != microsandbox.PolicyDirectionEgress {
					t.Errorf("rule %d is not egress: %+v", i, r)
				}
				if !groups[r.Destination] {
					if _, perr := netip.ParsePrefix(r.Destination); perr != nil {
						if _, aerr := netip.ParseAddr(r.Destination); aerr != nil {
							t.Errorf("rule %d destination %q is neither a group nor a CIDR/IP", i, r.Destination)
						}
					}
				}
				switch {
				case r.Action == microsandbox.PolicyActionAllow && r.Destination == "public":
					allowPublic = i
				case r.Action == microsandbox.PolicyActionDeny:
					if _, ok := denied[r.Destination]; !ok {
						denied[r.Destination] = i
					}
				}
			}
			if allowPublic < 0 {
				t.Fatal("no allow rule for public")
			}
			for _, g := range []string{"loopback", "private", "link-local", "metadata", "host"} {
				i, ok := denied[g]
				if !ok {
					t.Errorf("no deny rule for %s", g)
				} else if i > allowPublic {
					t.Errorf("deny %s (rule %d) comes after allow public (rule %d)", g, i, allowPublic)
				}
			}
		})
	}
}
