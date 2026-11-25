package size

import "testing"

func TestParseMiBAccepted(t *testing.T) {
	for in, want := range map[string]uint32{
		"512M":        512,
		"512m":        512,
		"512MB":       512,
		"512MiB":      512,
		"1G":          1024,
		"4G":          4096,
		"4g":          4096,
		"4GB":         4096,
		"4gb":         4096,
		"4GiB":        4096,
		"4gib":        4096,
		"4Gib":        4096,
		"1T":          1 << 20,
		"1TiB":        1 << 20,
		"4095T":       4095 << 20,
		"4294967295M": 4294967295,
		"007G":        7168,
	} {
		got, err := ParseMiB(in)
		if err != nil {
			t.Errorf("ParseMiB(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseMiB(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseMiBRejected(t *testing.T) {
	for _, in := range []string{
		"", "4", "0G", "0M", "-1G", "+1G", "1.5G", "4K", "4P", "4 G", " 4G", "4G ",
		"4GiBB", "4iB", "4B", "G", "iB", "4096T", "4294967296M", "4194304G",
		"99999999999999999999999G",
	} {
		if got, err := ParseMiB(in); err == nil {
			t.Errorf("ParseMiB(%q) = %d, want error", in, got)
		}
	}
}

func TestParseMiBErrorSaysWhatIsAccepted(t *testing.T) {
	_, err := ParseMiB("4X")
	want := `invalid size "4X": want a whole number followed by M, G or T (e.g. 4G)`
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}
