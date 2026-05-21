package main

import (
	"github.com/spf13/cobra"
)

func newDiscoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "discover",
		Short: "Scrape release sources and notify",
		Long:  "Scrape DVD/streaming release sites, enrich with TMDB/RT data, save to database, and send notification.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
}
