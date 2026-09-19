package cmd

import "testing"

func TestRun(t *testing.T) {
	command.SetArgs([]string{"version"})
	t.Cleanup(func() { command.SetArgs(nil) })
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
}
