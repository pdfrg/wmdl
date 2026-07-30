package notifier

import "github.com/stretchr/testify/mock"

type MockNotifier struct {
	mock.Mock
}

func (m *MockNotifier) Send(title, message string, priority int) error {
	args := m.Called(title, message, priority)
	return args.Error(0)
}
