package notifier

import "github.com/pdfrg/wmdl/internal/config"

type Notifier interface {
	Send(title, message string, priority int) error
}

func New(cfg config.NotifierConfig) (Notifier, error) {
	return NewWebhook(cfg)
}
