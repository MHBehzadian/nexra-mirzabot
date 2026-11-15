package panels

import (
	"os"
	"testing"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
)

// Runs only with PG_LIVE_URL (a real PasarGuard) and PG_LIVE_DSN (MySQL).
func TestPasarGuardLive(t *testing.T) {
	url, dsn := os.Getenv("PG_LIVE_URL"), os.Getenv("PG_LIVE_DSN")
	if url == "" || dsn == "" {
		t.Skip("no live PasarGuard")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureSchema("1", func(string) {}); err != nil {
		t.Fatal(err)
	}
	d.Exec("DELETE FROM marzban_panel WHERE name_panel='pg'")
	d.Exec("INSERT INTO marzban_panel (name_panel,url_panel,username_panel,password_panel,type,inboundid,sublink,configManual,MethodUsername,statusTest,status,onholdstatus) VALUES ('pg',?,?,?,'pasarguard','0','onsublink','offconfig','x','ontestshowpanel','activepanel','offonhold')",
		url, os.Getenv("PG_LIVE_USER"), os.Getenv("PG_LIVE_PASS"))
	m := New(d)
	name := "nxtest" + time.Now().Format("150405")
	exp := time.Now().Unix() + 30*86400

	c := m.CreateUser("pg", name, exp, 5*1024*1024*1024, false)
	t.Logf("create: %v", c)
	if c.S("status") != "successful" || c.S("subscription_url") == "" {
		t.Fatalf("create failed: %v", c)
	}
	u := m.DataUser("pg", name)
	t.Logf("data: %v", u)
	if u.S("status") != "active" || u.F("data_limit") != 5*1024*1024*1024 {
		t.Fatalf("data: %v", u)
	}
	if e := u.F("expire"); e < float64(exp-5) || e > float64(exp+5) {
		t.Fatalf("expire %v, want ~%d", e, exp)
	}
	// renew: +30 days, 10 GB (what the bot sends)
	exp2 := exp + 30*86400
	r := m.Modifyuser(name, "pg", map[string]any{"expire": exp2, "data_limit": int64(10 * 1024 * 1024 * 1024), "proxies": map[string]any{}})
	t.Logf("modify: %v", r)
	u = m.DataUser("pg", name)
	if u.F("data_limit") != 10*1024*1024*1024 || u.F("expire") < float64(exp2-5) {
		t.Fatalf("after modify: %v", u)
	}
	if len(u.List("links")) == 0 {
		t.Fatalf("no configs: %v", u)
	}
	m.ResetUserDataUsage("pg", name)
	time.Sleep(1100 * time.Millisecond) // the link carries the revoke time in seconds
	rv := m.RevokeSub("pg", name)
	t.Logf("revoke: %v", rv)
	if rv.S("status") != "successful" || rv.S("subscription_url") == c.S("subscription_url") {
		t.Fatalf("revoke: %v (old sub %s)", rv, c.S("subscription_url"))
	}
	// on hold
	d.Exec("UPDATE marzban_panel SET onholdstatus='ononhold' WHERE name_panel='pg'")
	h := m.CreateUser("pg", name+"h", time.Now().Unix()+7*86400, 1024*1024*1024, true)
	t.Logf("on hold test user: %v", h)
	hd := m.DataUser("pg", name+"h")
	if h.S("status") != "successful" || hd.S("status") != "on_hold" || hd.F("expire") != 0 {
		t.Fatalf("on hold: %v / %v", h, hd)
	}
	for _, n := range []string{name, name + "h"} {
		if rm := m.RemoveUser("pg", n); rm.S("status") != "successful" {
			t.Fatalf("remove %s: %v", n, rm)
		}
	}
	if g := m.DataUser("pg", name); g.S("status") != "Unsuccessful" {
		t.Fatalf("user still there: %v", g)
	}
}
