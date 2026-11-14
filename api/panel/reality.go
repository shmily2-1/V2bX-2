package panel

import (
	"encoding/hex"
	"fmt"
	"strings"
)

func (s TlsSettings) EffectiveServerNames() ([]string, error) {
	if len(s.ServerNames) == 0 {
		return []string{s.ServerName}, nil
	}
	seen := make(map[string]bool)
	for _, name := range s.ServerNames {
		if name == "" || strings.ContainsAny(name, " \t\r\n") || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate REALITY server_names")
		}
		seen[name] = true
	}
	if s.ServerName != "" && !seen[s.ServerName] {
		return nil, fmt.Errorf("server_name conflicts with server_names")
	}
	return append([]string(nil), s.ServerNames...), nil
}

func (s TlsSettings) EffectiveShortIDs() ([]string, error) {
	ids := s.ShortIds
	if len(ids) == 0 {
		ids = []string{s.ShortId}
	}
	result := make([]string, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		id = strings.ToLower(id)
		if _, err := hex.DecodeString(id); err != nil || len(id) > 16 || seen[id] {
			return nil, fmt.Errorf("invalid or duplicate REALITY short_ids")
		}
		seen[id] = true
		result = append(result, id)
	}
	if s.ShortId != "" && !seen[strings.ToLower(s.ShortId)] {
		return nil, fmt.Errorf("short_id conflicts with short_ids")
	}
	return result, nil
}
