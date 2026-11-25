package bot

import (
	"context"
	"strings"
	"time"
)

// Poll fetches updates with getUpdates until ctx ends, feeding them to the
// same workers the webhook does. A bot on a bare IP runs this way: Telegram
// never has to reach the server, so it needs no domain and no certificate.
// It returns once it has stopped handing out updates, so the caller can then
// close the queue safely.
func (b *Bot) Poll(ctx context.Context) {
	if r := b.TG.DeleteWebhook(); !r.OK {
		b.Log.Printf("polling: deleteWebhook: %s", r.Description)
	}
	b.Log.Printf("polling Telegram for updates (no webhook)")
	var offset int64
	wait := time.Second
	for ctx.Err() == nil {
		updates, r := b.TG.GetUpdates(ctx, offset, 50)
		if ctx.Err() != nil {
			return
		}
		if !r.OK {
			// 409: a webhook was set again from somewhere else; take it back
			if r.ErrorCode == 409 || strings.Contains(r.Description, "webhook") {
				b.TG.DeleteWebhook()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if wait < 30*time.Second {
				wait *= 2
			}
			continue
		}
		wait = time.Second
		for i := range updates {
			u := updates[i]
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			b.Enqueue(&u)
		}
	}
}
