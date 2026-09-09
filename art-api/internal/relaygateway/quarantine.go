package relaygateway

import (
	"context"
	"errors"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"time"
)

type QuarantinePage struct {
	Revision string            `json:"revision"`
	Total    int               `json:"total"`
	Offset   int               `json:"offset"`
	Events   []QuarantineEvent `json:"events"`
}

// One bounded page per frame; all requests are scoped to a currently authenticated control peer.
func (g *Gateway) Quarantine(ctx context.Context, id, action, revision string, offset int) (*QuarantinePage, error) {
	if (action != "list" && action != "archive") || offset < 0 || offset > 4096 {
		return nil, errors.New("invalid quarantine request")
	}
	if revision != "" {
		if _, err := uuid.Parse(revision); err != nil {
			return nil, err
		}
	}
	if action == "archive" && revision == "" {
		return nil, errors.New("revision required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	g.mu.Lock()
	p := g.peers[id]
	g.mu.Unlock()
	if p == nil {
		return nil, ErrOffline
	}
	if err := g.Check(ctx, id, p.token); err != nil {
		return nil, err
	}
	requestID := uuid.NewString()
	ch := make(chan Message, 1)
	g.mu.Lock()
	if g.peers[id] != p || !p.ready || len(p.pending) >= 64 {
		g.mu.Unlock()
		return nil, ErrOffline
	}
	p.pending[requestID] = waiter{result: ch}
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(p.pending, requestID); g.mu.Unlock() }()
	m := Message{Type: "quarantine", Session: p.session, RequestID: requestID, Action: action, Revision: revision, Offset: offset, ExpiresAt: time.Now().Add(10 * time.Second).UnixMilli()}
	if err := wsjson.Write(ctx, p.conn, m); err != nil {
		return nil, err
	}
	select {
	case reply := <-ch:
		if reply.Type != "quarantine_reply" || reply.Status != "ok" || reply.QuarantinePage == nil {
			return nil, errors.New("quarantine changed or unavailable")
		}
		page := reply.QuarantinePage
		if _, err := uuid.Parse(page.Revision); err != nil {
			return nil, errors.New("invalid revision")
		}
		if revision != "" && page.Revision != revision {
			return nil, errors.New("revision mismatch")
		}
		if page.Total < 0 || page.Total > 4096 || page.Offset != offset || len(page.Events) > 10 || (action == "list" && len(page.Events) != min(10, page.Total-offset)) {
			return nil, errors.New("invalid quarantine page")
		}
		for _, e := range page.Events {
			if len(e.ID) > 64 || len(e.ID) == 0 || len(e.UUID) < 8 || len(e.UUID) > 128 || (e.Status != "active" && e.Status != "closed") || e.Reason != "credential_rotated" {
				return nil, errors.New("invalid quarantine event")
			}
		}
		return page, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.closed:
		return nil, ErrOffline
	}
}
