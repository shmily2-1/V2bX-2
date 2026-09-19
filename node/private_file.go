package node

import (
	"os"
	"path/filepath"
)

// Write through a private sibling file; never expose partial account/key content.
func writePrivateFile(name string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".v2bx-private-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}
