package library

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pdfrg/wmdl/internal/model"
)

func TestLazyLibrarianClient_Ping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cmd") != "getVersion" {
			t.Errorf("expected cmd=getVersion, got %s", r.URL.Query().Get("cmd"))
		}
		if r.URL.Query().Get("apikey") != "test-key" {
			t.Errorf("expected apikey=test-key, got %s", r.URL.Query().Get("apikey"))
		}
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "test-key", 10)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
}

func TestLazyLibrarianClient_AddBook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cmd") != "addBook" {
			t.Errorf("expected cmd=addBook, got %s", r.URL.Query().Get("cmd"))
		}
		if r.URL.Query().Get("id") != "123456" {
			t.Errorf("expected id=123456, got %s", r.URL.Query().Get("id"))
		}
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	res, err := c.AddBook(context.Background(), "123456")
	if err != nil {
		t.Fatalf("AddBook() error: %v", err)
	}
	if res.BookID != "123456" {
		t.Fatalf("expected BookID 123456, got %s", res.BookID)
	}
}

func TestLazyLibrarianClient_UnqueueBook(t *testing.T) {
	tests := []struct {
		name   string
		format model.BookFormat
		want   string
	}{
		{"ebook", model.BookFormatEbook, "eBook"},
		{"audiobook", model.BookFormatAudiobook, "AudioBook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("cmd") != "unqueueBook" {
					t.Errorf("expected cmd=unqueueBook")
				}
				if r.URL.Query().Get("id") != "42" {
					t.Errorf("expected id=42")
				}
				if r.URL.Query().Get("type") != tt.want {
					t.Errorf("expected type=%s, got %s", tt.want, r.URL.Query().Get("type"))
				}
				w.Write([]byte("OK"))
			}))
			c := NewLazyLibrarianClient(srv.URL, "key", 10)
			if err := c.UnqueueBook(context.Background(), "42", tt.format); err != nil {
				t.Fatalf("UnqueueBook() error: %v", err)
			}
			srv.Close()
		})
	}
}

func TestLazyLibrarianClient_ImportAlternate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cmd") != "importAlternate" {
			t.Errorf("expected cmd=importAlternate")
		}
		if r.URL.Query().Get("library") != "eBook" {
			t.Errorf("expected library=eBook")
		}
		if r.URL.Query().Get("dir") != "/path/to/alt" {
			t.Errorf("expected dir=/path/to/alt")
		}
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	if err := c.ImportAlternate(context.Background(), "/path/to/alt", model.BookFormatEbook); err != nil {
		t.Fatalf("ImportAlternate() error: %v", err)
	}
}

func TestLazyLibrarianClient_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Something went wrong"))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error from server error response")
	}
}

func TestLazyLibrarianClient_GetSeriesMembers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"position":1,"title":"Book One","author_name":"Author A","author_id":"100","book_id":"1001","pubdate":"2026-01-15"},
			{"position":2,"title":"Book Two","author_name":"Author A","author_id":"100","book_id":"1002"}
		]`))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	members, err := c.GetSeriesMembers(context.Background(), "HCseries1")
	if err != nil {
		t.Fatalf("GetSeriesMembers() error: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	if members[0].Title != "Book One" || members[0].Position != 1 || members[0].PubDate != "2026-01-15" {
		t.Fatalf("unexpected first member: %+v", members[0])
	}
	if members[1].PubDate != "" {
		t.Fatalf("expected empty pubdate for second member, got %q", members[1].PubDate)
	}
}

func TestNewBookClient_Factory(t *testing.T) {
	if c := NewBookClient("none", "", "", 0); c != nil {
		t.Fatal("expected nil for 'none' backend")
	}
	// lazylibrarian backend should return a valid client
	c := NewBookClient("lazylibrarian", "http://test:5299", "key", 10)
	if c == nil {
		t.Fatal("expected non-nil client for lazylibrarian backend")
	}
}
