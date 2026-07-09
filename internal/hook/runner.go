package hook

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const timeout = 30 * time.Second

func Run(ctx context.Context, cmd string) error {
	if cmd == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func RunSoft(ctx context.Context, cmd, label string) {
	if cmd == "" {
		return
	}
	if err := Run(ctx, cmd); err != nil {
		log.Warn().Err(err).Str("hook", label).Msg("hook failed (continuing)")
	} else {
		log.Debug().Str("hook", label).Msg("hook completed")
	}
}

func RunDeferred(ctx context.Context, cmd, label string) {
	if cmd == "" {
		return
	}
	if err := Run(ctx, cmd); err != nil {
		log.Warn().Err(err).Str("hook", label).Msg("hook failed")
	} else {
		log.Debug().Str("hook", label).Msg("hook completed")
	}
}
