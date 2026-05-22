package notifier

import "github.com/pdfrg/wmd/internal/config"

type Notifier interface {
	Send(title, message string, priority int) error
}

func New(cfg config.NotifierConfig) Notifier {
	return NewWebhook(cfg)
}
