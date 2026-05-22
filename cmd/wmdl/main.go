package main

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var cfgFile string

func main() {
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).
		Level(zerolog.InfoLevel).
		With().
		Timestamp().
		Logger()

	cmd := &cobra.Command{
		Use:   "wmdl",
		Short: "Weekly Media Downloader",
		Long: `Automated workflow for discovering, reviewing, and downloading 
weekly DVD/streaming releases via Prowlarr, qBittorrent, and Radarr/Sonarr.`,
	}

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	cmd.AddCommand(newDiscoverCmd())
	cmd.AddCommand(newReviewCmd())
	cmd.AddCommand(newProcessCmd())
	cmd.AddCommand(newAllCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newCatchupCmd())

	if err := cmd.Execute(); err != nil {
		log.Fatal().Err(err).Msg("command failed")
		os.Exit(1)
	}
}
