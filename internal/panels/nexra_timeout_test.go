package panels

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
)

// A Nexra Panel that makes the user but answers too late: the bot finds the
// user on the Marzban behind it and takes it as made. One that never makes
// it gives a clear "did not answer" error. Needs PANELS_TEST_DSN (MySQL).
func TestNexraCreateNoAnswer(t *testing.T) {
	dsn := os.Getenv("PANELS_TEST_DSN")
	if dsn == "" {
		t.Skip("no PANELS_TEST_DSN")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureSchema("1", func(string) {}); err != nil {
		t.Fatal(err)
	}
	var made atomic.Bool
	makeIt := true
	nexra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login":
			w.Write([]byte(`{"data":{"access_token":"n"}}`))
		case r.Method == "POST" && r.URL.Path == "/admin/user":
			if makeIt {
				made.Store(true)
			}
			time.Sleep(700 * time.Millisecond)
			w.Write([]byte(`{"success":true}`))
		}
	}))
	defer nexra.Close()
	marz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/admin/token":
			w.Write([]byte(`{"access_token":"m"}`))
		case strings.HasPrefix(r.URL.Path, "/api/user/") && made.Load():
			w.Write([]byte(`{"username":"u1","subscription_url":"/sub/abc","links":["vless://x"]}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"detail":"User not found"}`))
		}
	}))
	defer marz.Close()

	d.Exec("DELETE FROM marzban_panel WHERE name_panel='nx'")
	d.Exec("INSERT INTO marzban_panel (name_panel,url_panel,username_panel,password_panel,type,inboundid,sublink,configManual,MethodUsername,statusTest,status,onholdstatus,marzban_url_direct,marzban_username_direct,marzban_password_direct,datelogin) VALUES ('nx',?,'a','b','nexra','0','onsublink','offconfig','x','ontestshowpanel','activepanel','offonhold',?,'c','d','')",
		nexra.URL, marz.URL)
	old := nexraWriteTimeout
	nexraWriteTimeout = 200 * time.Millisecond
	defer func() { nexraWriteTimeout = old }()
	m := New(d)

	c := m.CreateUser("nx", "u1", time.Now().Unix()+86400, 1<<30, false)
	if c.S("status") != "successful" || !strings.HasSuffix(c.S("subscription_url"), "/sub/abc") {
		t.Fatalf("made but late: %v", c)
	}

	made.Store(false)
	makeIt = false
	c = m.CreateUser("nx", "u2", time.Now().Unix()+86400, 1<<30, false)
	if c.S("status") == "successful" || !strings.Contains(c.S("msg"), "جواب نداد") {
		t.Fatalf("never made: %v", c)
	}
	t.Logf("error shown: %v", c)
	d.Exec("DELETE FROM marzban_panel WHERE name_panel='nx'")
}
