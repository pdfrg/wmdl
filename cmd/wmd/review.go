package main

import (
	"github.com/spf13/cobra"
)

func newReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review",
		Short: "Review pending releases in TUI",
		Long:  "Open interactive TUI to approve/reject pending releases and open titles in browser.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
}
