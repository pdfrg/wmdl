package library

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/pdfrg/wmdl/internal/model"
)

type MockBookClient struct {
	mock.Mock
}

func (m *MockBookClient) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockBookClient) AddAuthor(ctx context.Context, authorID string, fetchBooks bool) (*model.AuthorResult, error) {
	args := m.Called(ctx, authorID, fetchBooks)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.AuthorResult), args.Error(1)
}

func (m *MockBookClient) AddBook(ctx context.Context, bookID string) (*model.BookResult, error) {
	args := m.Called(ctx, bookID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.BookResult), args.Error(1)
}

func (m *MockBookClient) QueueBook(ctx context.Context, bookID string, format model.BookFormat) error {
	args := m.Called(ctx, bookID, format)
	return args.Error(0)
}

func (m *MockBookClient) UnqueueBook(ctx context.Context, bookID string, format model.BookFormat) error {
	args := m.Called(ctx, bookID, format)
	return args.Error(0)
}

func (m *MockBookClient) GetBookStatus(ctx context.Context, bookID string) (*model.BookStatus, error) {
	args := m.Called(ctx, bookID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.BookStatus), args.Error(1)
}

func (m *MockBookClient) GetAllBooks(ctx context.Context) ([]model.BookStatus, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]model.BookStatus), args.Error(1)
}

func (m *MockBookClient) SetAllBooks(books []model.BookStatus) {
	m.Called(books)
}

func (m *MockBookClient) GetSeriesMembers(ctx context.Context, seriesID string) ([]*model.SeriesMember, error) {
	args := m.Called(ctx, seriesID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*model.SeriesMember), args.Error(1)
}

func (m *MockBookClient) ImportAlternate(ctx context.Context, dir string, format model.BookFormat) error {
	args := m.Called(ctx, dir, format)
	return args.Error(0)
}
