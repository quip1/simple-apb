// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/quip1/simple-apb/server/internal/authz"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Hub pushes "something changed" hints to open clients over /ws, so they call
// GET /me/sync right away. It watches the change log (one cheap query every
// 500 ms), which also catches changes made by background jobs.
type Hub struct {
	db   *store.DB
	mu   sync.Mutex
	subs map[string]map[chan int64]struct{} // userID → subscribers
	seq  int64
}

// NewHub creates a hub positioned at the current end of the change log.
func NewHub(ctx context.Context, db *store.DB) *Hub {
	h := &Hub{db: db, subs: map[string]map[chan int64]struct{}{}}
	h.seq, _ = store.MaxSeq(ctx, db)
	return h
}

// Run polls the change log until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			users, everyone, seq, err := store.UsersChangedSince(ctx, h.db, h.seq)
			if err != nil || seq == h.seq {
				continue
			}
			h.seq = seq
			h.mu.Lock()
			for user, chans := range h.subs {
				if !everyone && !users[user] {
					continue
				}
				for ch := range chans {
					select {
					case ch <- seq:
					default: // a hint is already pending; one is enough
					}
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) subscribe(user string) chan int64 {
	ch := make(chan int64, 1)
	h.mu.Lock()
	if h.subs[user] == nil {
		h.subs[user] = map[chan int64]struct{}{}
	}
	h.subs[user][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(user string, ch chan int64) {
	h.mu.Lock()
	delete(h.subs[user], ch)
	if len(h.subs[user]) == 0 {
		delete(h.subs, user)
	}
	h.mu.Unlock()
}

type wsMessage struct {
	Type string `json:"type"`
	Seq  int64  `json:"seq"`
}

// serveWS upgrades an authenticated request (Bearer header or session
// cookie). The library's default Origin check rejects cross-site pages, which
// keeps cookie auth safe from cross-site WebSocket hijacking.
func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	app := s.App()
	p, err := requireCan(r.Context(), authz.Read)
	if err != nil {
		writeAuthErr(w, err)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the error
	}
	defer func() { _ = c.CloseNow() }()
	ctx := c.CloseRead(r.Context()) // we never read; this handles pings/close frames
	ch := app.Hub.subscribe(p.User.ID)
	defer app.Hub.unsubscribe(p.User.ID, ch)

	send := func(m wsMessage) error {
		b, _ := json.Marshal(m)
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return c.Write(wctx, websocket.MessageText, b)
	}
	seq, _ := store.MaxSeq(ctx, app.DB)
	if send(wsMessage{Type: "hello", Seq: seq}) != nil {
		return
	}
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case seq := <-ch:
			if send(wsMessage{Type: "changed", Seq: seq}) != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
