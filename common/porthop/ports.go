// Package porthop redirects Hysteria2's public UDP ports to a single listener.
// It never installs filter/ACCEPT rules or changes the host's default policies.
package porthop

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Range struct{ First, Last uint16 }

// Parse accepts decimal ports and inclusive ranges, separated by commas.
// Reject rather than reinterpret typos: port 0, reversed ranges, wildcards,
// empty elements, signs and command syntax are not accepted.
func Parse(value string) ([]Range, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 128 {
		return nil, fmt.Errorf("ports: at most 128 ranges are allowed")
	}
	parsePort := func(s string) (uint16, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, fmt.Errorf("empty port")
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("not a decimal port: %q", s)
			}
		}
		n, err := strconv.ParseUint(s, 10, 16)
		if err != nil || n == 0 {
			return 0, fmt.Errorf("port must be between 1 and 65535: %q", s)
		}
		return uint16(n), nil
	}
	var ranges []Range
	for _, part := range parts {
		ends := strings.Split(part, "-")
		if len(ends) > 2 {
			return nil, fmt.Errorf("ports: invalid range %q", part)
		}
		first, err := parsePort(ends[0])
		if err != nil {
			return nil, fmt.Errorf("ports: %w", err)
		}
		last := first
		if len(ends) == 2 {
			last, err = parsePort(ends[1])
			if err != nil {
				return nil, fmt.Errorf("ports: %w", err)
			}
		}
		if first > last {
			return nil, fmt.Errorf("ports: reversed range %q", part)
		}
		ranges = append(ranges, Range{first, last})
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].First < ranges[j].First })
	result := []Range{ranges[0]}
	for _, r := range ranges[1:] {
		last := &result[len(result)-1]
		if uint32(r.First) <= uint32(last.Last)+1 {
			if r.Last > last.Last {
				last.Last = r.Last
			}
		} else {
			result = append(result, r)
		}
	}
	return result, nil
}

func withoutPort(ranges []Range, port uint16) []Range {
	var result []Range
	for _, r := range ranges {
		if port < r.First || port > r.Last {
			result = append(result, r)
			continue
		}
		if port > r.First {
			result = append(result, Range{r.First, port - 1})
		}
		if port < r.Last {
			result = append(result, Range{port + 1, r.Last})
		}
	}
	return result
}

func portExpr(r Range, separator string) string {
	if r.First == r.Last {
		return strconv.Itoa(int(r.First))
	}
	return fmt.Sprintf("%d%s%d", r.First, separator, r.Last)
}
