package conf

import (
	"testing"
)

func TestConf_LoadFromPath(t *testing.T) {
	c := New()
	if err := c.LoadFromPath("../example/config.json"); err != nil {
		t.Fatal(err)
	}
}

func TestConf_MissingFile(t *testing.T) {
	c := New()
	if err := c.LoadFromPath(t.TempDir() + "/missing.json"); err == nil {
		t.Fatal("expected an error for a missing configuration")
	}
}
