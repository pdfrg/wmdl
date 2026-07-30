package download

import (
	"context"

	"github.com/stretchr/testify/mock"
)

type MockClient struct {
	mock.Mock
}

func (m *MockClient) AddTorrent(ctx context.Context, url string, opts ...Option) (string, error) {
	args := m.Called(ctx, url, opts)
	return args.String(0), args.Error(1)
}

func (m *MockClient) AddMagnet(ctx context.Context, uri string, opts ...Option) (string, error) {
	args := m.Called(ctx, uri, opts)
	return args.String(0), args.Error(1)
}

func (m *MockClient) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}
