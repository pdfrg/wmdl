package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	version = "dev"
)

func main() {
	console := zerolog.ConsoleWriter{Out: os.Stderr}
	logFile := openLogFile()
	var writers []io.Writer
	if logFile != nil {
		writers = append(writers, logFile)
	}
	writers = append(writers, console)
	mw := io.MultiWriter(writers...)

	log.Logger = zerolog.New(mw).
		Level(zerolog.InfoLevel).
		With().
		Timestamp().
		Logger()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cmd := &cobra.Command{
		Use:     "wmdl",
		Short:   "Weekly Media Downloader",
		Version: version,
		Long: `Automated workflow for discovering, reviewing, and downloading 
weekly DVD/streaming releases via Prowlarr, your preferred torrent client, and Radarr/Sonarr.`,
	}
	cmd.SetContext(ctx)

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

func openLogFile() *os.File {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "wmdl")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "wmdl.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	return f
}
