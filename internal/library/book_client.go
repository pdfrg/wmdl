package library

import (
	"context"

	"github.com/pdfrg/wmdl/internal/model"
)

type BookClient interface {
	Ping(ctx context.Context) error
	AddAuthor(ctx context.Context, authorID string, fetchBooks bool) (*model.AuthorResult, error)
	AddBook(ctx context.Context, bookID string) (*model.BookResult, error)
	QueueBook(ctx context.Context, bookID string, format model.BookFormat) error
	UnqueueBook(ctx context.Context, bookID string, format model.BookFormat) error
	GetBookStatus(ctx context.Context, bookID string) (*model.BookStatus, error)
	GetAllBooks(ctx context.Context) ([]model.BookStatus, error)
	SetAllBooks(books []model.BookStatus)
	GetSeriesMembers(ctx context.Context, seriesID string) ([]*model.SeriesMember, error)
	ImportAlternate(ctx context.Context, dir string, format model.BookFormat) error
}

func NewBookClient(backend, url, apiKey string, timeout int) BookClient {
	switch backend {
	case "lazylibrarian":
		return NewLazyLibrarianClient(url, apiKey, timeout)
	default:
		return nil
	}
}
