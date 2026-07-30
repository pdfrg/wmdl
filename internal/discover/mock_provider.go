package discover

import "github.com/stretchr/testify/mock"

type MockReleaseProvider struct {
	mock.Mock
}

func (m *MockReleaseProvider) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockReleaseProvider) Scrape() ([]ScrapedItem, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ScrapedItem), args.Error(1)
}

type MockWeekSettable struct {
	mock.Mock
}

func (m *MockWeekSettable) SetWeekRange(year, week int) {
	m.Called(year, week)
}
