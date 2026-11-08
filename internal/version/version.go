package version

import (
	"strconv"
	"strings"
)

type Version []int

func Parse(text string) (Version, bool) {
	parts := strings.Split(text, ".")
	if len(parts) > 3 {
		return nil, false
	}
	version := make(Version, len(parts))
	for index, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return nil, false
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		version[index] = number
	}
	return version, true
}

func (v Version) AtLeast(minimum Version) bool {
	for index, part := range minimum {
		actual := 0
		if index < len(v) {
			actual = v[index]
		}
		if actual != part {
			return actual > part
		}
	}
	return true
}
