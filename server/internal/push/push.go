// SPDX-License-Identifier: AGPL-3.0-or-later

// Package push delivers notifications through ntfy / UnifiedPush (brief §6.14).
//
// Payloads carry no content, only {"type": "...", "id": "..."}; apps fetch
// details from the API. Sending is a background job, so a slow push server
// never delays an API response.
package push

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quip1/simple-apb/server/internal/jobs"
	"github.com/quip1/simple-apb/server/internal/settings"
	"github.com/quip1/simple-apb/server/internal/store"
)

// Event types (v1).
const (
	UploadDone      = "upload_done"
	UploadFailed    = "upload_failed"
	LibraryAdded    = "library_added"
	SignupRequest   = "signup_request"
	BackupFailed    = "backup_failed"
	IntegrityFailed = "integrity_failed"
	UncleanShutdown = "unclean_shutdown"
)

// JobPush is the job type that delivers one event.
const JobPush = "push_send"

// Event is a notification.
type Event struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

// Recipients says who gets an event.
type Recipients struct {
	Users  []string `json:"users,omitempty"`
	Admins bool     `json:"admins,omitempty"`
	Group  string   `json:"group,omitempty"`
}

type payload struct {
	To    Recipients `json:"to"`
	Event Event      `json:"event"`
}

// Enqueue schedules an event for delivery.
func Enqueue(ctx context.Context, q store.Querier, to Recipients, ev Event) error {
	_, err := jobs.Enqueue(ctx, q, JobPush, payload{To: to, Event: ev}, time.Time{}, "")
	return err
}

// Notifier delivers queued events.
type Notifier struct {
	DB   *store.DB
	Enc  store.Encrypter
	Log  *slog.Logger
	HTTP *http.Client // nil = a safe default client
}

// NewTopic returns a random 128-bit topic name.
func NewTopic() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "sapb" + hex.EncodeToString(b)
}

// Handle is the job handler.
func (n *Notifier) Handle(ctx context.Context, j *jobs.Job) (any, error) {
	var p payload
	if err := j.Decode(&p); err != nil {
		return nil, jobs.Permanent(err)
	}
	return n.Send(ctx, p.To, p.Event)
}

// SendResult counts deliveries.
type SendResult struct {
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
	Removed int `json:"removed"`
}

// Send delivers an event to every device of the recipients. Delivery is best
// effort: apps also sync on start and over /ws, so a lost push loses nothing.
func (n *Notifier) Send(ctx context.Context, to Recipients, ev Event) (*SendResult, error) {
	users, err := n.resolve(ctx, to)
	if err != nil {
		return nil, err
	}
	ntfy, err := settings.Store{Q: n.DB, Enc: n.Enc}.Ntfy(ctx)
	if err != nil {
		return nil, err
	}
	client := n.client(ntfy)
	body, _ := json.Marshal(ev)
	res := &SendResult{}
	for _, u := range users {
		subs, err := listSubs(ctx, n.DB, u)
		if err != nil {
			return res, err
		}
		for _, s := range subs {
			target, auth := s.endpoint, ""
			if target == "" {
				if ntfy.InternalURL == "" {
					continue
				}
				target = strings.TrimRight(ntfy.InternalURL, "/") + "/" + s.topic
				auth = ntfy.Token
			}
			status, err := post(ctx, client, target, auth, body)
			switch {
			case err == nil && status < 300:
				res.Sent++
			case status == http.StatusNotFound || status == http.StatusGone:
				// The distributor says this endpoint is gone (app uninstalled).
				_, _ = n.DB.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, s.id)
				res.Removed++
			default:
				res.Failed++
				n.Log.Warn("push delivery failed", "type", ev.Type, "status", status, "err", err)
			}
		}
	}
	return res, nil
}

func post(ctx context.Context, c *http.Client, target, auth string, body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func (n *Notifier) resolve(ctx context.Context, to Recipients) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, u := range to.Users {
		add(u)
	}
	collect := func(query string, args ...any) error {
		rows, err := n.DB.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			add(id)
		}
		return rows.Err()
	}
	if to.Admins {
		if err := collect(`SELECT id FROM users WHERE role = 'admin' AND status = 'active'`); err != nil {
			return nil, err
		}
	}
	if to.Group != "" {
		if err := collect(`SELECT m.user_id FROM group_members m JOIN users u ON u.id = m.user_id
			WHERE m.group_id = ? AND u.status IN ('active', 'restricted')`, to.Group); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type sub struct{ id, topic, endpoint string }

func listSubs(ctx context.Context, q store.Querier, userID string) ([]sub, error) {
	rows, err := q.Query(ctx, `SELECT id, topic, unifiedpush_endpoint FROM push_subscriptions WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []sub
	for rows.Next() {
		var (
			s  sub
			ep sql.NullString
		)
		if err := rows.Scan(&s.id, &s.topic, &ep); err != nil {
			return nil, err
		}
		s.endpoint = ep.String
		out = append(out, s)
	}
	return out, rows.Err()
}

// Register stores (or updates) a device's subscription and returns its topic.
func Register(ctx context.Context, q store.Querier, userID, deviceID, endpoint string) (string, error) {
	var topic string
	err := q.QueryRow(ctx, `SELECT topic FROM push_subscriptions WHERE user_id = ? AND device_id = ?`, userID, deviceID).Scan(&topic)
	now := store.NowMS()
	var ep any
	if endpoint != "" {
		ep = endpoint
	}
	if errors.Is(err, sql.ErrNoRows) {
		topic = NewTopic()
		_, err = q.Exec(ctx, `INSERT INTO push_subscriptions (id, device_id, user_id, topic, unifiedpush_endpoint, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, store.NewID(), deviceID, userID, topic, ep, now, now)
		return topic, err
	}
	if err != nil {
		return "", err
	}
	_, err = q.Exec(ctx, `UPDATE push_subscriptions SET unifiedpush_endpoint = ?, updated_at = ? WHERE user_id = ? AND device_id = ?`,
		ep, now, userID, deviceID)
	return topic, err
}

// Unregister removes a device's subscription.
func Unregister(ctx context.Context, q store.Querier, userID, deviceID string) error {
	res, err := q.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = ? AND device_id = ?`, userID, deviceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ── SSRF protection ──
//
// The server POSTs to URLs that clients give it, so a malicious client could
// try to make it call internal services. Endpoints must be http(s), and the
// connection is refused if the host resolves to a private, loopback or
// link-local address, unless it is this instance's own ntfy.

// ErrUnsafeEndpoint is returned for endpoints the server refuses to call.
var ErrUnsafeEndpoint = errors.New("push endpoint must be a public https URL (or this instance's ntfy)")

// AllowedHosts returns the hosts of the configured ntfy URLs.
func AllowedHosts(n settings.Ntfy) []string {
	var out []string
	for _, raw := range []string{n.InternalURL, n.PublicURL} {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			out = append(out, strings.ToLower(u.Hostname()))
		}
	}
	return out
}

// ValidateEndpoint checks an endpoint's shape at registration.
func ValidateEndpoint(raw string, allowed []string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return ErrUnsafeEndpoint
	}
	host := strings.ToLower(u.Hostname())
	if contains(allowed, host) {
		return nil
	}
	if u.Scheme != "https" {
		return ErrUnsafeEndpoint
	}
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return ErrUnsafeEndpoint
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func publicIP(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsInterfaceLocalMulticast()
}

// client returns an HTTP client whose dialer re-checks resolved addresses at
// connection time (defeats DNS rebinding), except for the instance's ntfy.
func (n *Notifier) client(ntfy settings.Ntfy) *http.Client {
	if n.HTTP != nil {
		return n.HTTP
	}
	allowed := AllowedHosts(ntfy)
	d := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if contains(allowed, strings.ToLower(host)) {
				return d.DialContext(ctx, network, addr)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !publicIP(ip.IP) {
					return nil, fmt.Errorf("%w: %s resolves to %s", ErrUnsafeEndpoint, host, ip.IP)
				}
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no address for %s", host)
			}
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          10,
	}
	return &http.Client{Transport: tr, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
