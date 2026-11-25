package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bot.env")
	in := &Config{BotToken: "1:x", AdminID: "5", Domain: "b.example.com", NexraSecret: `s'e cr$t #1`, DBHost: "localhost", DBPort: "3306",
		DBName: "db", DBUser: "u", DBPass: `p"a$s\w '`, Listen: "127.0.0.1:18007", APIOwnerKey: "o", APIManagerKey: "m", DataDir: "/var/lib/nexrabot"}
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0640)
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.DBPass != in.DBPass || out.NexraSecret != in.NexraSecret || out.Listen != in.Listen || out.APIOwnerKey != "o" {
		t.Fatalf("round trip lost data: %q %q", out.DBPass, out.NexraSecret)
	}
	// rewriting keeps the mode a service user depends on
	if err := out.Write(path); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0640 {
		t.Fatalf("mode changed to %v", st.Mode().Perm())
	}
}

func TestPollingOnIPRoundTrip(t *testing.T) {
	path := t.TempDir() + "/bot9.env"
	in := &Config{BotToken: "1:x", AdminID: "5", Domain: "1.2.3.4:18009", DBName: "d", DBUser: "u",
		Updates: "polling", PublicURL: "http://1.2.3.4:18009", Listen: "0.0.0.0:18009"}
	if err := in.Write(path); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Polling() || out.URL("/autopay.php") != "http://1.2.3.4:18009/autopay.php" {
		t.Fatalf("got updates=%q url=%q", out.Updates, out.URL("/autopay.php"))
	}
	// a domain bot keeps the webhook and https://domain, and writes neither key
	d := &Config{BotToken: "1:x", AdminID: "5", Domain: "bot.example.com", DBName: "d", DBUser: "u", PublicURL: "https://bot.example.com"}
	if err := d.Write(path + "2"); err != nil {
		t.Fatal(err)
	}
	out, _ = Load(path + "2")
	if out.Polling() || out.URL("/x") != "https://bot.example.com/x" {
		t.Fatalf("domain bot: updates=%q url=%q", out.Updates, out.URL("/x"))
	}
}
