package panels

import (
	"log"
	"strings"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
)

// PasarGuard (github.com/PasarGuard/panel) is a Marzban fork with the same
// login and user routes, but: a user must be in at least one group (inbounds
// come from groups; there are no per-user inbounds/proxies), dates come back
// as ISO datetimes, and a user has no "links" - the configs are read from its
// subscription in the "links" format.

const pgPaidGroup, pgTestGroup = "mirza_paid", "mirza_test"

func (m *Manager) pgCall(panel db.Row, method, path string, payload []byte) (int, []byte) {
	tok := m.MarzbanToken(panel)
	h := map[string]string{"Accept": "application/json", "Authorization": "Bearer " + str(tok, "access_token")}
	ct := ""
	if payload != nil {
		ct = "application/json"
	}
	r := req{method: method, url: trimSlash(panel.S("url_panel")) + path, body: payload, contentType: ct, headers: h}.do()
	return r.code, r.body
}

// pgGroupID is the id of the bot's group for paid or test users, made on
// first use with every inbound of the panel (narrow it in PasarGuard later).
func (m *Manager) pgGroupID(panel db.Row, isTest bool) any {
	want := pgPaidGroup
	if isTest {
		want = pgTestGroup
	}
	_, body := m.pgCall(panel, "GET", "/api/groups", nil)
	for _, g := range groupsList(jsonAny(body)) {
		if n, _ := g["name"].(string); strings.EqualFold(n, want) && g["id"] != nil {
			return g["id"]
		}
	}
	_, ib := m.pgCall(panel, "GET", "/api/inbounds", nil)
	var tags []string
	for _, t := range strList(jsonAny(ib)) {
		tags = append(tags, t)
	}
	code, created := m.pgCall(panel, "POST", "/api/group", marshal(map[string]any{"name": want, "inbound_tags": tags}))
	g := jsonObj(created)
	if code != 200 && code != 201 || g["id"] == nil {
		log.Printf("pasarguard %s: create group %s failed: HTTP %d %s", panel.S("name_panel"), want, code, string(created))
		return nil
	}
	return g["id"]
}

// pgUnix turns a PasarGuard datetime ("2026-01-02T03:04:05Z", with or without
// a zone; naive means UTC) or a number into unix seconds; 0 for none.
func pgUnix(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		if x == "" {
			return 0
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t.Unix()
			}
		}
	}
	return 0
}

// pgLinks reads the user's configs from its subscription.
func (m *Manager) pgLinks(panel db.Row, subURL string) []string {
	if subURL == "" {
		return nil
	}
	r := req{method: "GET", url: strings.TrimRight(subURL, "/") + "/links", timeout: 10 * time.Second}.do()
	if r.code != 200 {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(r.body)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func pgError(body map[string]any) any {
	if d, ok := body["detail"]; ok && truthy(d) {
		return d
	}
	return nil
}

func (m *Manager) pgCreate(panel db.Row, username string, expire int64, dataLimit float64, isTest bool) Out {
	gid := m.pgGroupID(panel, isTest)
	if gid == nil {
		return unsuccessful("PasarGuard: could not find or create the group for bot users")
	}
	data := map[string]any{
		"username":   username,
		"data_limit": jsonNumber(dataLimit),
		"group_ids":  []any{gid},
	}
	if expire == 0 {
		data["expire"] = 0
	} else if panel.S("onholdstatus") == "ononhold" {
		data["status"] = "on_hold"
		data["on_hold_expire_duration"] = expire - time.Now().Unix()
	} else {
		data["expire"] = expire
	}
	_, body := m.pgCall(panel, "POST", "/api/user", marshal(data))
	out := jsonObj(body)
	if e := pgError(out); e != nil {
		return unsuccessful(e)
	}
	if out == nil || !isset(out, "username") {
		return unsuccessful("PasarGuard did not create the user: " + string(body))
	}
	sub := fixSubURL(str(out, "subscription_url"), panel.S("url_panel"))
	return Out{"status": "successful", "username": out["username"], "subscription_url": sub, "configs": m.pgLinks(panel, sub)}
}

func (m *Manager) pgData(panel db.Row, username string) Out {
	_, body := m.pgCall(panel, "GET", "/api/user/"+username, nil)
	u := jsonObj(body)
	if e := pgError(u); e != nil {
		return unsuccessful(e)
	}
	if !isset(u, "username") {
		return unsuccessful(u["detail"])
	}
	var expire any = pgUnix(u["expire"])
	if str(u, "status") == "on_hold" {
		expire = 0
	}
	sub := fixSubURL(str(u, "subscription_url"), panel.S("url_panel"))
	return Out{
		"status":           u["status"],
		"username":         u["username"],
		"data_limit":       u["data_limit"],
		"expire":           expire,
		"online_at":        u["online_at"],
		"used_traffic":     u["used_traffic"],
		"links":            m.pgLinks(panel, sub),
		"subscription_url": sub,
	}
}

func (m *Manager) pgRevoke(panel db.Row, username string) Out {
	_, body := m.pgCall(panel, "POST", "/api/user/"+username+"/revoke_sub", nil)
	if e := pgError(jsonObj(body)); e != nil {
		return unsuccessful(e)
	}
	d := m.pgData(panel, username)
	return Out{"status": "successful", "configs": d["links"], "subscription_url": d.S("subscription_url")}
}

func (m *Manager) pgRemove(panel db.Row, username string) Out {
	_, body := m.pgCall(panel, "DELETE", "/api/user/"+username, nil)
	if e := pgError(jsonObj(body)); e != nil {
		return unsuccessful(e)
	}
	return Out{"status": "successful", "username": username}
}

func (m *Manager) pgReset(panel db.Row, username string) {
	m.pgCall(panel, "POST", "/api/user/"+username+"/reset", nil)
}

// pgModify changes a user; Marzban-only fields (proxies, inbounds) are left
// out, since a PasarGuard user's inbounds come from its groups.
func (m *Manager) pgModify(panel db.Row, username string, data map[string]any) map[string]any {
	clean := map[string]any{}
	for k, v := range data {
		if k == "proxies" || k == "inbounds" {
			continue
		}
		clean[k] = v
	}
	_, body := m.pgCall(panel, "PUT", "/api/user/"+username, marshal(clean))
	return jsonObj(body)
}

// PasarGuardStats is GET /api/system (version, users, memory, traffic).
func (m *Manager) PasarGuardStats(panel db.Row) map[string]any {
	_, body := m.pgCall(panel, "GET", "/api/system", nil)
	return jsonObj(body)
}
