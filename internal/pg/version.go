package pg

import (
	"fmt"
	"strconv"
)

// ServerVersion is the PostgreSQL major.minor. Since PG10 the scheme is
// major*10000 + minor (e.g. 170004 = 17.4).
// src: https://www.postgresql.org/docs/current/functions-info.html (server_version_num)
type ServerVersion struct {
	Major int
	Minor int
}

// ParseServerVersionNum parses a server_version_num string such as "170004".
func ParseServerVersionNum(num string) (ServerVersion, error) {
	n, err := strconv.Atoi(num)
	if err != nil {
		return ServerVersion{}, fmt.Errorf("server_version_num %q: %w", num, err)
	}
	if n <= 0 {
		return ServerVersion{}, fmt.Errorf("server_version_num %q: must be positive", num)
	}
	return ServerVersion{Major: n / 10000, Minor: n % 10000}, nil
}

// AtLeast reports whether the server is at least major.minor.
func (v ServerVersion) AtLeast(major, minor int) bool {
	if v.Major != major {
		return v.Major > major
	}
	return v.Minor >= minor
}

func (v ServerVersion) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }
