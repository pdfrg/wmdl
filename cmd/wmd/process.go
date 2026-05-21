package main

import (
	"github.com/spf13/cobra"
)

func newProcessCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "process",
		Short: "Search and download approved releases",
		Long:  "Grab browser tabs, search Prowlarr, select releases in TUI picker, download, and add to Radarr/Sonarr.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
}
