package panels

import (
	"crypto/rand"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/MHBehzadian/nexra-mirzabot/internal/db"
)

// A Nexra-type panel row talks to two servers: Nexra Panel itself
// (url_panel/username_panel/password_panel) for every write, so the admin's
// traffic quota is enforced, and the real Marzban behind it
// (marzban_*_direct) for read-only lookups.

func nexraBase(p db.Row) string { return trimSlash(p.S("url_panel")) }

// NexraLogin is nexra_login(): {"access_token":..} or {"error":..}.
func (m *Manager) NexraLogin(panel db.Row) map[string]any {
	panel = m.panelByID(panel.S("id"))
	cache := loginCache(panel)
	if e, ok := cache["nexra"].(map[string]any); ok && isset(e, "access_token") {
		if tok, ok := fresh(e, 600); ok {
			return map[string]any{"access_token": tok, "time": e["time"]}
		}
	}
	r := req{method: "POST", url: nexraBase(panel) + "/login", timeout: 8 * time.Second,
		body:        formBody("username", panel.S("username_panel"), "password", panel.S("password_panel")),
		contentType: "application/x-www-form-urlencoded", headers: map[string]string{"accept": "application/json"}}.do()
	if r.err != nil {
		return map[string]any{"error": r.err.Error()}
	}
	body := jsonObj(r.body)
	if d, ok := body["data"].(map[string]any); ok {
		if tok, ok := d["access_token"].(string); ok {
			e := map[string]any{"time": nowStamp(), "access_token": tok}
			cache["nexra"] = e
			m.DB.Update("marzban_panel", "datelogin", string(marshal(cache)), "name_panel", panel.S("name_panel"))
			return e
		}
	}
	return map[string]any{"error": NexraErrorMessage(body, "Nexra login failed")}
}

// NexraErrorMessage extracts a readable message from a Nexra/FastAPI reply.
func NexraErrorMessage(body map[string]any, fallback string) string {
	msg := fallback
	if s, ok := body["message"].(string); ok && s != "" {
		msg = s
	} else if s, ok := body["detail"].(string); ok && s != "" {
		msg = s
	} else if arr, ok := body["detail"].([]any); ok {
		var parts []string
		for _, it := range arr {
			if im, ok := it.(map[string]any); ok {
				if s, ok := im["msg"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		if len(parts) > 0 {
			msg = strings.Join(parts, "; ")
		}
	}
	if strings.Contains(strings.ToLower(msg), "insufficient traffic") {
		return "❌ حجم پنل شما در Nexra کافی نیست. لطفاً حجم پنل را شارژ کنید.\n(" + msg + ")"
	}
	return msg
}

// nexraWriteTimeout is how long a create/change waits for Nexra Panel.
var nexraWriteTimeout = 45 * time.Second

// nexraNoAnswer marks a result where Nexra Panel never answered (timeout,
// connection lost): the change may still have been made.
const nexraNoAnswer = "_no_answer"

func (m *Manager) nexraRequest(panel db.Row, method, path string, payload any) map[string]any {
	auth := m.NexraLogin(panel)
	tok, ok := auth["access_token"].(string)
	if !ok {
		e := str(auth, "error")
		if e == "" {
			e = "auth failed"
		}
		return map[string]any{"detail": e}
	}
	h := map[string]string{"Accept": "application/json", "Authorization": "Bearer " + tok}
	var body []byte
	ct := ""
	if payload != nil {
		body = marshal(payload)
		ct = "application/json"
	}
	// Nexra Panel makes the change on the panel behind it (Marzban, 3x-ui,
	// Guard) before it answers, waiting up to 30s for that panel, so a change
	// gets longer than a read.
	timeout := 10 * time.Second
	if method != "GET" {
		timeout = nexraWriteTimeout
	}
	r := req{method: method, url: nexraBase(panel) + path, body: body, contentType: ct, headers: h, timeout: timeout}.do()
	if r.err != nil {
		return map[string]any{"detail": r.err.Error(), nexraNoAnswer: true}
	}
	return jsonObj(r.body)
}

// NexraDashboard is nexra_dashboard().
func (m *Manager) NexraDashboard(panel db.Row) map[string]any {
	auth := m.NexraLogin(panel)
	tok, ok := auth["access_token"].(string)
	if !ok {
		e := str(auth, "error")
		if e == "" {
			e = "auth failed"
		}
		return map[string]any{"detail": e}
	}
	r := req{url: nexraBase(panel) + "/dashboard", timeout: 10 * time.Second,
		headers: map[string]string{"Accept": "application/json", "Authorization": "Bearer " + tok}}.do()
	if r.err != nil {
		return map[string]any{"detail": r.err.Error()}
	}
	body := jsonObj(r.body)
	if v, _ := body["success"].(bool); !v {
		return map[string]any{"detail": NexraErrorMessage(body, "Nexra dashboard request failed")}
	}
	d, _ := body["data"].(map[string]any)
	if d == nil {
		d = map[string]any{}
	}
	return d
}

func (m *Manager) nexraDirectToken(panel db.Row) map[string]any {
	panel = m.panelByID(panel.S("id"))
	cache := loginCache(panel)
	if e, ok := cache["marzban"].(map[string]any); ok && isset(e, "access_token") {
		if tok, ok := fresh(e, 3600); ok {
			return map[string]any{"access_token": tok}
		}
	}
	r := req{method: "POST", url: trimSlash(panel.S("marzban_url_direct")) + "/api/admin/token", timeout: 8 * time.Second,
		body:        formBody("username", panel.S("marzban_username_direct"), "password", panel.S("marzban_password_direct")),
		contentType: "application/x-www-form-urlencoded", headers: map[string]string{"accept": "application/json"}}.do()
	if r.err != nil {
		return map[string]any{"error": r.err.Error()}
	}
	body := jsonObj(r.body)
	if tok, ok := body["access_token"].(string); ok {
		e := map[string]any{"time": nowStamp(), "access_token": tok}
		cache["marzban"] = e
		m.DB.Update("marzban_panel", "datelogin", string(marshal(cache)), "name_panel", panel.S("name_panel"))
		return e
	}
	d := str(body, "detail")
	if d == "" {
		d = "Marzban direct login failed"
	}
	return map[string]any{"error": d}
}

func (m *Manager) nexraDirectGetUser(panel db.Row, username string) map[string]any {
	tok := m.nexraDirectToken(panel)
	t, ok := tok["access_token"].(string)
	if !ok {
		e := str(tok, "error")
		if e == "" {
			e = "auth failed"
		}
		return map[string]any{"detail": e}
	}
	r := req{url: trimSlash(panel.S("marzban_url_direct")) + "/api/user/" + username, timeout: 8 * time.Second,
		headers: map[string]string{"Accept": "application/json", "Authorization": "Bearer " + t}}.do()
	return jsonObj(r.body)
}

func phpUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (m *Manager) nexraCreate(panel db.Row, username string, expire int64, dataLimit float64) Out {
	exp := int64(0)
	if expire != 0 {
		exp = expire * 1000
	}
	payload := map[string]any{
		"email": username, "id": phpUUID(), "enable": true,
		"expiry_time": exp, "total": dataLimit, "sub_id": username, "flow": "",
	}
	res := m.nexraRequest(panel, "POST", "/admin/user", payload)
	ok, _ := res["success"].(bool)
	var created map[string]any
	if !ok && res[nexraNoAnswer] == true {
		// no answer in time: the panel may have made the user anyway, so
		// look for it before calling it a failure (a retry would then hit a
		// taken username, and the user would sit on the panel unsold)
		created = m.nexraDirectGetUser(panel, username)
		ok = isset(created, "username") && !isset(created, "detail")
		if ok {
			log.Printf("nexra %s: create %s got no answer (%v) but the user exists; taking it as made", panel.S("name_panel"), username, res["detail"])
		}
	}
	if !ok {
		msg := NexraErrorMessage(res, "خطا در ساخت یوزر روی Nexra")
		if res[nexraNoAnswer] == true {
			msg = fmt.Sprintf("Nexra Panel در %d ثانیه جواب نداد (پنل یا سرور پشت آن کند یا در دسترس نیست): %s", int(nexraWriteTimeout.Seconds()), msg)
		}
		return unsuccessful(msg)
	}
	if created == nil {
		created = m.nexraDirectGetUser(panel, username)
	}
	if isset(created, "detail") || !isset(created, "username") {
		return Out{"status": "successful", "username": username, "subscription_url": "", "configs": []string{}}
	}
	sub := str(created, "subscription_url")
	if !subURLRe.MatchString(sub) {
		sub = trimSlash(panel.S("marzban_url_direct")) + "/" + strings.TrimLeft(sub, "/")
	}
	links := strList(created["links"])
	if links == nil {
		links = []string{}
	}
	return Out{"status": "successful", "username": username, "subscription_url": sub, "configs": links}
}

func (m *Manager) nexraData(panel db.Row, username string) Out {
	u := m.nexraDirectGetUser(panel, username)
	if isset(u, "detail") || !isset(u, "username") {
		d := u["detail"]
		if d == nil {
			d = "User not found"
		}
		return unsuccessful(d)
	}
	sub := str(u, "subscription_url")
	if !subURLRe.MatchString(sub) {
		sub = trimSlash(panel.S("marzban_url_direct")) + "/" + strings.TrimLeft(sub, "/")
	}
	expire := u["expire"]
	if str(u, "status") == "on_hold" {
		expire = 0
	}
	links := strList(u["links"])
	if links == nil {
		links = []string{}
	}
	return Out{
		"status": u["status"], "username": u["username"], "data_limit": u["data_limit"],
		"expire": expire, "online_at": u["online_at"], "used_traffic": u["used_traffic"],
		"links": links, "subscription_url": sub,
	}
}

func (m *Manager) nexraRemove(panel db.Row, username string) Out {
	res := m.nexraRequest(panel, "DELETE", "/admin/user/"+url.PathEscape(username), nil)
	if v, _ := res["success"].(bool); !v {
		return unsuccessful(NexraErrorMessage(res, "خطا در حذف یوزر روی Nexra"))
	}
	return Out{"status": "successful", "username": username}
}

func (m *Manager) nexraReset(panel db.Row, username string) {
	m.nexraRequest(panel, "PUT", "/admin/user/"+url.PathEscape(username)+"/reset", nil)
}

// nexraModify mirrors Modifyuser_nexra: unspecified fields are filled from
// the live Marzban record. Errors come back as {"detail": ...}.
func (m *Manager) nexraModify(panel db.Row, username string, data map[string]any) Out {
	cur := m.nexraDirectGetUser(panel, username)
	if isset(cur, "detail") || !isset(cur, "username") {
		d := cur["detail"]
		if d == nil {
			d = "User not found"
		}
		return Out{"detail": d}
	}
	var expire int64
	if v, ok := data["expire"]; ok {
		if toF(v) != 0 {
			expire = int64(toF(v)) * 1000
		}
	} else {
		expire = int64(toF(cur["expire"])) * 1000
	}
	total := toF(cur["data_limit"])
	if v, ok := data["data_limit"]; ok {
		total = toF(v)
	}
	enable := str(cur, "status") != "disabled"
	if v, ok := data["status"]; ok {
		enable = fmt.Sprint(v) != "disabled"
	}
	payload := map[string]any{
		"email": username, "enable": enable, "expiry_time": expire,
		"total": total, "sub_id": username, "flow": "",
	}
	res := m.nexraRequest(panel, "PUT", "/admin/user/"+url.PathEscape(username), payload)
	if v, _ := res["success"].(bool); !v {
		return Out{"detail": NexraErrorMessage(res, "خطا در ویرایش یوزر روی Nexra")}
	}
	return Out{"username": username, "success": true}
}
