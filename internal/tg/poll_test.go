package tg

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetUpdates(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			io.WriteString(w, `{"ok":true,"result":true}`)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"ok":true,"result":[{"update_id":7,"message":{"message_id":1,"text":"/start","chat":{"id":5,"type":"private"},"from":{"id":5}}}]}`)
	}))
	defer srv.Close()
	c := New("1:x", srv.URL, "")
	ups, r := c.GetUpdates(context.Background(), 3, 50)
	if !r.OK || len(ups) != 1 || ups[0].UpdateID != 7 || ups[0].Message == nil || ups[0].Message.Text != "/start" {
		t.Fatalf("bad result %+v %+v", r, ups)
	}
	if got["offset"].(float64) != 3 || got["timeout"].(float64) != 50 {
		t.Fatalf("bad request %v", got)
	}
}

func TestGetUpdatesCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // a long poll that does not answer in time
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	c := New("1:x", srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	_, r := c.GetUpdates(ctx, 0, 50)
	if r.OK || time.Since(start) > 5*time.Second {
		t.Fatalf("cancel did not end the poll: %+v after %v", r, time.Since(start))
	}
}
