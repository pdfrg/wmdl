package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/pdfrg/wmdl/internal/config"
)

const (
	maxLogSize    = 10 * 1024 * 1024 // trim when file exceeds this
	keepAfterTrim = 5 * 1024 * 1024  // keep this much from the tail
)

var (
	cfgFile string
	verbose bool
	version = "dev"
)

func main() {
	console := zerolog.ConsoleWriter{Out: os.Stderr}
	logFile := openLogFile()
	// Console first so terminal output isn't blocked by a slow/failing file write.
	// io.MultiWriter stops on first error, so file-before-console means a file
	// write error silently suppresses terminal output.
	var writers []io.Writer
	writers = append(writers, console)
	if logFile != nil {
		writers = append(writers, logFile)
	}
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
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip log-level resolution for the completion help command
			if cmd.Name() == "completion" || cmd.Name() == "help" {
				return nil
			}
			// Resolve log level:
			//   --verbose flag > config log.level > default InfoLevel
			if verbose {
				log.Logger = log.Logger.Level(zerolog.DebugLevel)
			} else if cfg, err := config.Load(); err == nil {
				switch cfg.Log.Level {
				case "debug":
					log.Logger = log.Logger.Level(zerolog.DebugLevel)
				case "warn":
					log.Logger = log.Logger.Level(zerolog.WarnLevel)
				case "error":
					log.Logger = log.Logger.Level(zerolog.ErrorLevel)
				}
			}
			return nil
		},
	}
	cmd.SetContext(ctx)

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
	cmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")

	cmd.AddCommand(newDiscoverCmd())
	cmd.AddCommand(newReviewCmd())
	cmd.AddCommand(newProcessCmd())
	cmd.AddCommand(newAllCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newCatchupCmd())
	cmd.AddCommand(newMarkDownloadedCmd())
	cmd.AddCommand(searchCmd)
	cmd.AddCommand(addCmd)

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
	path := filepath.Join(dir, "wmdl.log")
	trimLogFile(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	return f
}

func trimLogFile(path string) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	size := fi.Size()
	if size <= maxLogSize {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	readFrom := size - keepAfterTrim
	if _, err := f.Seek(readFrom, io.SeekStart); err != nil {
		_ = f.Close()
		return
	}

	// Skip partial line so we keep only whole lines
	br := bufio.NewReader(f)
	if _, err := br.ReadString('\n'); err != nil {
		_ = f.Close()
		return
	}

	rest, err := io.ReadAll(br)
	_ = f.Close()
	if err != nil {
		return
	}

	_ = os.WriteFile(path, rest, 0644)
}
