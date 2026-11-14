package panel

import "testing"

func TestRealityMultipleNamesAndShortIDs(t *testing.T) {
	settings := TlsSettings{ServerName: "one.test", ServerNames: []string{"one.test", "two.test"}, ShortId: "ABCD", ShortIds: []string{"abcd", "1234"}}
	names, err := settings.EffectiveServerNames()
	if err != nil || len(names) != 2 {
		t.Fatal(names, err)
	}
	ids, err := settings.EffectiveShortIDs()
	if err != nil || len(ids) != 2 || ids[0] != "abcd" {
		t.Fatal(ids, err)
	}
	settings.ServerName = "conflict.test"
	if _, err := settings.EffectiveServerNames(); err == nil {
		t.Fatal("ignored name conflict")
	}
	settings.ShortId = "ffff"
	if _, err := settings.EffectiveShortIDs(); err == nil {
		t.Fatal("ignored short ID conflict")
	}
	for _, bad := range []string{"123", "nothex", "0123456789abcdef00"} {
		if _, err := (TlsSettings{ShortId: bad}).EffectiveShortIDs(); err == nil {
			t.Fatal("accepted invalid short ID")
		}
	}
}
