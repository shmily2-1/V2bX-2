package cmd

import (
	log "github.com/sirupsen/logrus"

	_ "github.com/shmily2-1/V2bX-2/core/imports"
	"github.com/spf13/cobra"
)

var command = &cobra.Command{
	Use: "V2bX",
}

func Run() {
	err := command.Execute()
	if err != nil {
		log.WithField("err", err).Error("Execute command failed")
	}
}
