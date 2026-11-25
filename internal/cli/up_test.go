package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arteven/gh-runners-msb/internal/spec"
)

func TestParseSizesDefaultsMatchSpecDefaults(t *testing.T) {
	// The flag defaults are strings; pin them to spec.Defaults so they can't drift.
	cmd := newUpCmd()
	f := cmd.Flags()
	cpus, _ := f.GetInt("cpus")
	memory, _ := f.GetString("memory")
	disk, _ := f.GetString("disk")
	dockerDisk, _ := f.GetString("docker-disk")
	got, err := parseSizes(cpus, memory, disk, dockerDisk)
	if err != nil {
		t.Fatal(err)
	}
	if got != spec.Defaults {
		t.Errorf("flag defaults give %+v, want spec.Defaults %+v", got, spec.Defaults)
	}
}

func TestParseSizes(t *testing.T) {
	tests := []struct {
		name                     string
		cpus                     int
		memory, disk, dockerDisk string
		want                     spec.Sizes
		wantErr                  string // substring; empty means success
	}{
		{name: "custom", cpus: 4, memory: "8GiB", disk: "20G", dockerDisk: "10g",
			want: spec.Sizes{CPUs: 4, MemoryMiB: 8192, DiskMiB: 20480, DockerMiB: 10240}},
		{name: "max cpus", cpus: 255, memory: "4G", disk: "5G", dockerDisk: "2G",
			want: spec.Sizes{CPUs: 255, MemoryMiB: 4096, DiskMiB: 5120, DockerMiB: 2048}},
		{name: "cpus zero", cpus: 0, memory: "4G", disk: "5G", dockerDisk: "2G",
			wantErr: "--cpus must be at least 1, got 0"},
		{name: "cpus negative", cpus: -1, memory: "4G", disk: "5G", dockerDisk: "2G",
			wantErr: "--cpus must be at least 1, got -1"},
		{name: "cpus too many", cpus: 256, memory: "4G", disk: "5G", dockerDisk: "2G",
			wantErr: "--cpus must be at most 255, got 256"},
		{name: "bad memory", cpus: 2, memory: "4X", disk: "5G", dockerDisk: "2G",
			wantErr: `--memory: invalid size "4X"`},
		{name: "bad disk", cpus: 2, memory: "4G", disk: "0G", dockerDisk: "2G",
			wantErr: `--disk: invalid size "0G"`},
		{name: "bad docker-disk", cpus: 2, memory: "4G", disk: "5G", dockerDisk: "1.5G",
			wantErr: `--docker-disk: invalid size "1.5G"`},
		{name: "docker-disk equals disk", cpus: 2, memory: "4G", disk: "2G", dockerDisk: "2G",
			wantErr: "--docker-disk (2G) must be smaller than --disk (2G)"},
		{name: "docker-disk equals disk in MiB", cpus: 2, memory: "4G", disk: "2048M", dockerDisk: "2G",
			wantErr: "--docker-disk (2G) must be smaller than --disk (2048M)"},
		{name: "docker-disk greater than disk", cpus: 2, memory: "4G", disk: "2048M", dockerDisk: "3G",
			wantErr: "--docker-disk (3G) must be smaller than --disk (2048M)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSizes(tt.cpus, tt.memory, tt.disk, tt.dockerDisk)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveLogDir(t *testing.T) {
	if f := newUpCmd().Flags().Lookup("logs"); f == nil || f.DefValue != "" {
		t.Fatalf("--logs flag = %v, want one defaulting to empty", f)
	}
	got, err := resolveLogDir("rel/logs")
	if err != nil || !filepath.IsAbs(got) || !strings.HasSuffix(got, filepath.FromSlash("rel/logs")) {
		t.Errorf("resolveLogDir(rel/logs) = %q, %v; want an absolute path", got, err)
	}
	t.Setenv("XDG_CACHE_HOME", "/cache")
	t.Setenv("HOME", "/home/x")
	got, err = resolveLogDir("")
	if err != nil || got != filepath.Join(userCache(t), "gh-runners-msb", "logs") {
		t.Errorf("resolveLogDir(\"\") = %q, %v", got, err)
	}
}

func userCache(t *testing.T) string {
	t.Helper()
	d, err := os.UserCacheDir()
	if err != nil {
		t.Skip(err)
	}
	return d
}
