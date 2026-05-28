package download

import "context"

type Option func(*AddOptions)

type AddOptions struct {
	SavePath string
	Category string
	Tags     []string
	Paused   bool
}

func WithSavePath(path string) Option {
	return func(o *AddOptions) {
		o.SavePath = path
	}
}

func WithCategory(cat string) Option {
	return func(o *AddOptions) {
		o.Category = cat
	}
}

func WithPaused(paused bool) Option {
	return func(o *AddOptions) {
		o.Paused = paused
	}
}

type Client interface {
	AddTorrent(ctx context.Context, url string, opts ...Option) (torrentID string, err error)
	AddMagnet(ctx context.Context, uri string, opts ...Option) (torrentID string, err error)
	Ping(ctx context.Context) error
}
