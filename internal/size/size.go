// Package size parses human sizes such as 4G or 512MiB into MiB.
package size

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// A whole number, a unit (M, G or T) and an optional B or iB suffix.
var sizeRE = regexp.MustCompile(`(?i)^([0-9]+)([MGT])(B|IB)?$`)

// MiB per unit; always binary.
var unitMiB = map[string]uint64{"M": 1, "G": 1024, "T": 1024 * 1024}

// ParseMiB converts s to MiB. The grammar is a positive integer, then M, G or
// T, then an optional B or iB, case-insensitive: 4G, 4GiB and 4gb are equal.
// uint32 matches every SDK size field, so overflow is rejected here.
func ParseMiB(s string) (uint32, error) {
	bad := fmt.Errorf("invalid size %q: want a whole number followed by M, G or T (e.g. 4G)", s)
	m := sizeRE.FindStringSubmatch(s)
	if m == nil {
		return 0, bad
	}
	n, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, fmt.Errorf("invalid size %q: too large", s)
		}
		return 0, bad
	}
	if n == 0 {
		return 0, fmt.Errorf("invalid size %q: must be greater than zero", s)
	}
	mib := n * unitMiB[strings.ToUpper(m[2])]
	if mib > math.MaxUint32 {
		return 0, fmt.Errorf("invalid size %q: too large", s)
	}
	return uint32(mib), nil
}
