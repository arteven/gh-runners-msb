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

func TestOptionsBuild(t *testing.T) {
	opts := Local(Defaults, "img:1", "abc123").Options()
	if len(opts) == 0 {
		t.Fatal("no options")
	}
	for i, o := range opts {
		if o == nil {
			t.Errorf("option %d is nil", i)
		}
	}
}
