// Package tg is a small Telegram Bot API client covering what the bot uses.
package tg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	Token string
	Base  string // https://api.telegram.org
	HTTP  *http.Client
	Log   *log.Logger
}

func New(token, base, proxy string) *Client {
	tr := &http.Transport{MaxIdleConnsPerHost: 32, IdleConnTimeout: 90 * time.Second}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &Client{Token: token, Base: base, HTTP: &http.Client{Timeout: 60 * time.Second, Transport: tr}}
}

// Response is the Bot API envelope.
type Response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

func (c *Client) logf(format string, a ...any) {
	if c.Log != nil {
		c.Log.Printf(format, a...)
	}
}

// Call sends a method with JSON parameters. Nil values are dropped, the way
// an unset PHP array entry would be.
func (c *Client) Call(method string, params map[string]any) Response {
	clean := map[string]json.RawMessage{}
	for k, v := range params {
		if v == nil {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil || string(b) == "null" {
			continue
		}
		clean[k] = b
	}
	body, err := json.Marshal(clean)
	if err != nil {
		c.logf("tg %s marshal: %v", method, err)
		return Response{}
	}
	return c.do(method, "application/json", bytes.NewReader(body))
}

func (c *Client) do(method, contentType string, body io.Reader) Response {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.Base+"/bot"+c.Token+"/"+method, body)
	if err != nil {
		return Response{}
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.logf("tg %s: %v", method, err)
		return Response{}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var r Response
	_ = json.Unmarshal(raw, &r)
	if !r.OK {
		c.logf("tg %s failed: %s", method, string(raw))
	}
	return r
}

// Upload sends a method with one file field plus string fields.
func (c *Client) Upload(method string, fields map[string]any, fileField, fileName string, data []byte) Response {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if v == nil {
			continue
		}
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case int64:
			s = strconv.FormatInt(x, 10)
		case int:
			s = strconv.Itoa(x)
		case bool:
			s = strconv.FormatBool(x)
		default:
			b, _ := json.Marshal(x)
			if string(b) == "null" {
				continue
			}
			s = string(b)
		}
		_ = w.WriteField(k, s)
	}
	fw, _ := w.CreateFormFile(fileField, fileName)
	_, _ = fw.Write(data)
	_ = w.Close()
	return c.do(method, w.FormDataContentType(), &buf)
}

// ---------------------------------------------------------------------------
// keyboards

// CopyText is the copy_text button payload.
type CopyText struct {
	Text string `json:"text"`
}

// Button covers both reply and inline keyboard buttons.
type Button struct {
	Text              string    `json:"text"`
	CallbackData      *string   `json:"callback_data,omitempty"`
	URL               string    `json:"url,omitempty"`
	CopyText          *CopyText `json:"copy_text,omitempty"`
	RequestContact    bool      `json:"request_contact,omitempty"`
	Style             string    `json:"style,omitempty"`
	IconCustomEmojiID string    `json:"icon_custom_emoji_id,omitempty"`
}

// Markup is any reply_markup object; nil means none.
type Markup any

// CB builds an inline button with callback data.
func CB(text, data string) Button { d := data; return Button{Text: text, CallbackData: &d} }

// URLBtn builds an inline URL button.
func URLBtn(text, u string) Button { return Button{Text: text, URL: u} }

// Txt builds a reply keyboard button.
func Txt(text string) Button { return Button{Text: text} }

type InlineKeyboard struct {
	InlineKeyboard [][]Button `json:"inline_keyboard"`
	// a few PHP keyboards carried this on inline markup; Telegram ignores it
	ResizeKeyboard bool `json:"resize_keyboard,omitempty"`
}

type ReplyKeyboard struct {
	Keyboard       [][]Button `json:"keyboard"`
	ResizeKeyboard bool       `json:"resize_keyboard"`
}

// Inline makes an inline keyboard from rows.
func Inline(rows ...[]Button) *InlineKeyboard {
	if rows == nil {
		rows = [][]Button{}
	}
	return &InlineKeyboard{InlineKeyboard: rows}
}

// Reply makes a resized reply keyboard from rows.
func Reply(rows ...[]Button) *ReplyKeyboard {
	if rows == nil {
		rows = [][]Button{}
	}
	return &ReplyKeyboard{Keyboard: rows, ResizeKeyboard: true}
}

// Row is a convenience for one keyboard row.
func Row(b ...Button) []Button { return b }

// ---------------------------------------------------------------------------
// methods

func (c *Client) SendMessage(chatID any, text string, markup Markup, parseMode string) Response {
	return c.Call("sendMessage", map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
		"reply_markup":             markup,
		"parse_mode":               emptyNil(parseMode),
	})
}

func (c *Client) EditMessageText(chatID any, messageID int64, text string, markup Markup) Response {
	return c.Call("editMessageText", map[string]any{
		"chat_id":      chatID,
		"message_id":   messageID,
		"text":         text,
		"reply_markup": markup,
		"parse_mode":   "html",
	})
}

func (c *Client) EditMessageCaption(chatID any, messageID int64, caption string, markup Markup) Response {
	return c.Call("editMessageCaption", map[string]any{
		"chat_id":      chatID,
		"message_id":   messageID,
		"caption":      caption,
		"reply_markup": markup,
	})
}

func (c *Client) DeleteMessage(chatID any, messageID int64) Response {
	return c.Call("deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID})
}

func (c *Client) ForwardMessage(fromChat any, messageID int64, toChat any) Response {
	return c.Call("forwardMessage", map[string]any{"from_chat_id": fromChat, "message_id": messageID, "chat_id": toChat})
}

// SendPhotoID sends an existing photo by file_id/URL.
func (c *Client) SendPhotoID(chatID any, photo, caption string, markup Markup, parseMode string) Response {
	return c.Call("sendPhoto", map[string]any{
		"chat_id":      chatID,
		"photo":        photo,
		"caption":      caption,
		"reply_markup": markup,
		"parse_mode":   emptyNil(parseMode),
	})
}

// SendPhotoFile uploads PNG bytes.
func (c *Client) SendPhotoFile(chatID any, png []byte, caption string, markup Markup) Response {
	return c.Upload("sendPhoto", map[string]any{
		"chat_id":      chatID,
		"caption":      caption,
		"reply_markup": markup,
		"parse_mode":   "HTML",
	}, "photo", "qr.png", png)
}

func (c *Client) SendVideo(chatID any, video, caption string) Response {
	return c.Call("sendVideo", map[string]any{"chat_id": chatID, "video": video, "caption": caption})
}

func (c *Client) SendDocument(chatID any, name string, data []byte, caption string) Response {
	return c.Upload("sendDocument", map[string]any{"chat_id": chatID, "caption": caption}, "document", name, data)
}

func (c *Client) AnswerCallback(id, text string, alert bool) Response {
	return c.Call("answerCallbackQuery", map[string]any{
		"callback_query_id": id,
		"text":              text,
		"show_alert":        alert,
		"cache_time":        5,
	})
}

// ChatMemberStatus returns the member status, ok=false when the call failed.
func (c *Client) ChatMemberStatus(chat string, userID any) (string, bool) {
	r := c.Call("getChatMember", map[string]any{"chat_id": chat, "user_id": userID})
	if !r.OK {
		return "", false
	}
	var m struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(r.Result, &m)
	return m.Status, true
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

func (c *Client) GetMe() (User, error) {
	r := c.Call("getMe", nil)
	if !r.OK {
		return User{}, fmt.Errorf("getMe: %s", r.Description)
	}
	var u User
	err := json.Unmarshal(r.Result, &u)
	return u, err
}

// DownloadFile fetches a file the bot has seen (getFile + file download).
func (c *Client) DownloadFile(fileID string) ([]byte, string, error) {
	r := c.Call("getFile", map[string]any{"file_id": fileID})
	if !r.OK {
		return nil, "", fmt.Errorf("getFile: %s", r.Description)
	}
	var f struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(r.Result, &f)
	res, err := c.HTTP.Get(c.Base + "/file/bot" + c.Token + "/" + f.FilePath)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 20<<20))
	return b, res.Header.Get("Content-Type"), err
}

func (c *Client) SetWebhook(u, secret string) Response {
	p := map[string]any{"url": u, "max_connections": 40, "allowed_updates": []string{"message", "callback_query"}}
	if secret != "" {
		p["secret_token"] = secret
	}
	return c.Call("setWebhook", p)
}

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
