package library

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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

func TestLazyLibrarianClient_QueueBook(t *testing.T) {
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
				if r.URL.Query().Get("cmd") != "queueBook" {
					t.Errorf("expected cmd=queueBook, got %s", r.URL.Query().Get("cmd"))
				}
				if r.URL.Query().Get("id") != "42" {
					t.Errorf("expected id=42, got %s", r.URL.Query().Get("id"))
				}
				if r.URL.Query().Get("type") != tt.want {
					t.Errorf("expected type=%s, got %s", tt.want, r.URL.Query().Get("type"))
				}
				w.Write([]byte("OK"))
			}))
			c := NewLazyLibrarianClient(srv.URL, "key", 10)
			if err := c.QueueBook(context.Background(), "42", tt.format); err != nil {
				t.Fatalf("QueueBook() error: %v", err)
			}
			srv.Close()
		})
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

func TestLazyLibrarianClient_AddAuthor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cmd") != "addAuthorID" {
			t.Errorf("expected cmd=addAuthorID, got %s", r.URL.Query().Get("cmd"))
		}
		if r.URL.Query().Get("id") != "999" {
			t.Errorf("expected id=999, got %s", r.URL.Query().Get("id"))
		}
		if r.URL.Query().Get("books") != "true" {
			t.Errorf("expected books=true, got %s", r.URL.Query().Get("books"))
		}
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	res, err := c.AddAuthor(context.Background(), "999", true)
	if err != nil {
		t.Fatalf("AddAuthor() error: %v", err)
	}
	if res.AuthorID != "999" {
		t.Fatalf("expected AuthorID 999, got %s", res.AuthorID)
	}
}

func TestLazyLibrarianClient_AddAuthor_NoBooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("books") != "" {
			t.Errorf("expected books param to be absent, got %s", r.URL.Query().Get("books"))
		}
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	_, err := c.AddAuthor(context.Background(), "999", false)
	if err != nil {
		t.Fatalf("AddAuthor(fetchBooks=false) error: %v", err)
	}
}

func TestLazyLibrarianClient_GetBookStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"bookid":"100","bookname":"Found Book","status":"Skipped","audiostatus":"Wanted","bookfile":"","audiofile":""},
			{"bookid":"200","bookname":"Other Book","status":"Have","audiostatus":"Have","bookfile":"file.epub","audiofile":"file.m4b"}
		]`))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	status, err := c.GetBookStatus(context.Background(), "100")
	if err != nil {
		t.Fatalf("GetBookStatus() error: %v", err)
	}
	if status.BookID != "100" {
		t.Fatalf("expected BookID 100, got %s", status.BookID)
	}
	if status.Title != "Found Book" {
		t.Fatalf("expected Title 'Found Book', got %s", status.Title)
	}
	if status.Status != "Skipped" {
		t.Fatalf("expected Status 'Skipped', got %s", status.Status)
	}
	if status.AudioStatus != "Wanted" {
		t.Fatalf("expected AudioStatus 'Wanted', got %s", status.AudioStatus)
	}
}

func TestLazyLibrarianClient_GetBookStatus_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"bookid":"999","bookname":"Only Book"}]`))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	_, err := c.GetBookStatus(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent book")
	}
}

func TestLazyLibrarianClient_ImportAlternate(t *testing.T) {
	t.Run("ebook", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cmd") != "importAlternate" {
				t.Errorf("expected cmd=importAlternate")
			}
			if r.URL.Query().Get("library") != "eBook" {
				t.Errorf("expected library=eBook, got %s", r.URL.Query().Get("library"))
			}
			if r.URL.Query().Get("dir") != "/path/to/alt" {
				t.Errorf("expected dir=/path/to/alt, got %s", r.URL.Query().Get("dir"))
			}
			w.Write([]byte("OK"))
		}))
		defer srv.Close()

		c := NewLazyLibrarianClient(srv.URL, "key", 10)
		if err := c.ImportAlternate(context.Background(), "/path/to/alt", model.BookFormatEbook); err != nil {
			t.Fatalf("ImportAlternate() error: %v", err)
		}
	})

	t.Run("audiobook", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cmd") != "importAlternate" {
				t.Errorf("expected cmd=importAlternate")
			}
			if r.URL.Query().Get("library") != "AudioBook" {
				t.Errorf("expected library=AudioBook, got %s", r.URL.Query().Get("library"))
			}
			if r.URL.Query().Get("dir") != "" {
				t.Errorf("expected empty dir, got %s", r.URL.Query().Get("dir"))
			}
			w.Write([]byte("OK"))
		}))
		defer srv.Close()

		c := NewLazyLibrarianClient(srv.URL, "key", 10)
		if err := c.ImportAlternate(context.Background(), "", model.BookFormatAudiobook); err != nil {
			t.Fatalf("ImportAlternate() error: %v", err)
		}
	})
}

func TestLazyLibrarianClient_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Something went wrong"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	if err := c.Ping(ctx); err == nil {
		t.Fatal("expected error from server error response")
	}
}

func TestLazyLibrarianClient_GetSeriesMembers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Response format returned by LL: [[members...], total, prefix]
		w.Write([]byte(`[
			[[1,"Book One","Author A",1001,100,"2026-01-15","","",1001],[2,"Book Two","Author A",1002,100,"","","",1002]],
			0,
			"HC"
		]`))
	}))
	defer srv.Close()

	c := NewLazyLibrarianClient(srv.URL, "key", 10)
	members, err := c.GetSeriesMembers(context.Background(), "series1")
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

func TestLLErrorHint(t *testing.T) {
	msg := llError("addBook", 500, "TypeError: string indices must be integers, not 'str'")
	assert.Contains(t, msg, "update LazyLibrarian")

	plain := llError("queueBook", 500, "some other error")
	assert.NotContains(t, plain, "update LazyLibrarian")
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

func TestSanitizeLLBody(t *testing.T) {
	t.Run("extracts exception from traceback page", func(t *testing.T) {
		body := []byte(`<!DOCTYPE html>
<html><head><title>500 Internal Server Error</title></head>
<body>
<pre id="traceback">Traceback (most recent call last):
  File "/app/lazylibrarian/api.py", line 2388, in _addonebook
    api = source['api']
TypeError: string indices must be integers, not 'str'
</pre>
</body></html>`)
		got := sanitizeLLBody(body)
		assert.Contains(t, got, "TypeError: string indices must be integers, not 'str'")
		assert.NotContains(t, got, "<html>")
		assert.NotContains(t, got, "Traceback (most recent call last)")
		assert.Less(t, len(got), 300)
	})

	t.Run("strips tags from plain html error", func(t *testing.T) {
		got := sanitizeLLBody([]byte("<html><body><h2>500 Internal Server Error</h2><p>boom</p></body></html>"))
		assert.Contains(t, got, "boom")
		assert.NotContains(t, got, "<h2>")
	})

	t.Run("truncates long output", func(t *testing.T) {
		long := make([]byte, 500)
		for i := range long {
			long[i] = 'a'
		}
		got := sanitizeLLBody(long)
		assert.LessOrEqual(t, len(got), 300+len("..."))
		assert.Contains(t, got, "...")
	})

	t.Run("empty body returns empty", func(t *testing.T) {
		assert.Empty(t, sanitizeLLBody(nil))
	})
}
