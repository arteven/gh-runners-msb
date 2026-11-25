package spec

import (
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
