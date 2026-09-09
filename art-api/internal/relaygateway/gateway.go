package relaygateway

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

var ErrOffline = errors.New("relay control unavailable")

type Message struct {
	Revision       string          `json:"revision,omitempty"`
	Offset         int             `json:"offset,omitempty"`
	QuarantinePage *QuarantinePage `json:"quarantine_page,omitempty"`
	Delivery       *DeliveryStatus `json:"delivery,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Durable        bool            `json:"durable,omitempty"`
	Type           string          `json:"type"`
	Session        string          `json:"session"`
	RequestID      string          `json:"request_id,omitempty"`
	UUID           string          `json:"uuid,omitempty"`
	Action         string          `json:"action,omitempty"`
	Status         string          `json:"status,omitempty"`
	ExpiresAt      int64           `json:"expires_at,omitempty"`
	Connections    int             `json:"connections,omitempty"`
	Bandwidth      uint64          `json:"bandwidth,omitempty"`
}
type waiter struct {
	uuid   string
	result chan Message
}
type peer struct {
	conn       *websocket.Conn
	session    string
	token      string
	ready      bool
	pending    map[string]waiter
	issued     map[string]time.Time
	cancel     context.CancelFunc
	closed     <-chan struct{}
	reports    map[string]Message
	delivery   *DeliveryStatus
	observedAt time.Time
}
type Gateway struct {
	mu            sync.Mutex
	peers         map[string]*peer
	Check         func(context.Context, string, string) error
	Report        func(context.Context, string, Message) error
	Authorize     func(context.Context, string, string, string) error
	DurableReport func(context.Context, string, string, Message) error
}

func New() *Gateway { return &Gateway{peers: make(map[string]*peer)} }
func (g *Gateway) Online(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.peers[id]
	return p != nil && p.ready
}
func (g *Gateway) Disconnect(id string) {
	g.mu.Lock()
	p := g.peers[id]
	delete(g.peers, id)
	g.mu.Unlock()
	if p != nil {
		p.cancel()
		_ = p.conn.CloseNow()
	}
}

// Caller authenticates before upgrade; Check is repeated after upgrade and during use.
func (g *Gateway) Serve(w http.ResponseWriter, r *http.Request, id, token string) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8192)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if g.Check(ctx, id, token) != nil {
		return
	}
	p := &peer{conn: conn, session: uuid.NewString(), token: token, pending: map[string]waiter{}, issued: map[string]time.Time{}, cancel: cancel, closed: ctx.Done(), reports: map[string]Message{}}
	g.mu.Lock()
	if g.peers[id] != nil {
		g.mu.Unlock()
		return
	} // A second connection cannot steal a live control session.
	g.peers[id] = p
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		if g.peers[id] == p {
			delete(g.peers, id)
		}
		g.mu.Unlock()
	}()
	helloCtx, helloCancel := context.WithTimeout(ctx, 3*time.Second)
	err = wsjson.Write(helloCtx, conn, Message{Type: "hello", Session: p.session, Durable: g.DurableReport != nil})
	helloCancel()
	if err != nil {
		return
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.mu.Lock()
				for key, expiry := range p.issued {
					if !expiry.IsZero() && time.Now().After(expiry) {
						delete(p.issued, key)
					}
				}
				g.mu.Unlock()
				checkCtx, done := context.WithTimeout(ctx, 3*time.Second)
				err := g.Check(checkCtx, id, token)
				if err == nil {
					err = conn.Ping(checkCtx)
				}
				done()
				if err != nil {
					cancel()
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()
	window := time.Now()
	count := 0
	readyDeadline := time.Now().Add(5 * time.Second)
	for {
		var m Message
		g.mu.Lock()
		ready := p.ready
		g.mu.Unlock()
		deadline := time.Now().Add(30 * time.Second)
		if !ready {
			deadline = readyDeadline
		}
		readCtx, done := context.WithDeadline(ctx, deadline)
		err := wsjson.Read(readCtx, conn, &m)
		done()
		if err != nil || m.Session != p.session {
			return
		}
		if time.Since(window) > time.Second {
			window = time.Now()
			count = 0
		}
		count++
		if count > 100 {
			return
		}
		if !ready && m.Type != "ready" {
			return
		}
		switch m.Type {
		case "ready":
			if ready {
				return
			}
			g.mu.Lock()
			p.ready = true
			g.mu.Unlock()
		case "ack", "quarantine_reply":
			g.mu.Lock()
			wait, ok := p.pending[m.RequestID]
			g.mu.Unlock()
			if ok && wait.uuid == m.UUID {
				select {
				case wait.result <- m:
				default:
				}
			}
		case "telemetry":
			if !m.Delivery.valid() {
				return
			}
			if m.Connections < 0 || m.Connections > 1000000 || m.Bandwidth > 1<<50 {
				return
			}
			if g.Report != nil {
				if err := g.report(ctx, id, m); err != nil {
					return
				}
			}
			g.mu.Lock()
			p.delivery = m.Delivery
			p.observedAt = time.Now().UTC()
			g.mu.Unlock()
		case "lifecycle":
			if g.DurableReport != nil && m.RequestID != "" {
				reportCtx, finish := context.WithTimeout(ctx, 3*time.Second)
				err := g.DurableReport(reportCtx, id, token, m)
				finish()
				if errors.Is(err, ErrStaleGeneration) {
					rejectCtx, finish := context.WithTimeout(ctx, 3*time.Second)
					err = wsjson.Write(rejectCtx, conn, Message{Type: "lifecycle_rejected", Session: m.Session, RequestID: m.RequestID, UUID: m.UUID, Status: m.Status, Reason: "credential_rotated"})
					finish()
					if err != nil {
						return
					}
					continue
				}
				if err != nil {
					return
				}
				g.mu.Lock()
				if m.Status == "closed" {
					delete(p.issued, m.UUID)
				} else if _, ok := p.issued[m.UUID]; ok {
					p.issued[m.UUID] = time.Time{}
				}
				g.mu.Unlock()
				if err := acknowledgeLifecycle(ctx, conn, m); err != nil {
					return
				}
				continue
			}
			if len(m.RequestID) > 64 {
				return
			}
			if previous, ok := p.reports[m.RequestID]; m.RequestID != "" && ok {
				if previous.UUID != m.UUID || previous.Status != m.Status {
					return
				}
				if err := acknowledgeLifecycle(ctx, conn, m); err != nil {
					return
				}
				continue
			}
			if m.RequestID != "" && len(p.reports) >= 8192 {
				return
			}
			g.mu.Lock()
			_, allowed := p.issued[m.UUID]
			g.mu.Unlock()
			if !allowed || (m.Status != "active" && m.Status != "closed") {
				return
			}
			if g.Report != nil {
				if err := g.report(ctx, id, m); err != nil {
					return
				}
			}
			g.mu.Lock()
			if m.Status == "active" {
				p.issued[m.UUID] = time.Time{}
			} else {
				delete(p.issued, m.UUID)
			}
			g.mu.Unlock()
			if m.RequestID != "" {
				p.reports[m.RequestID] = m
				if err := acknowledgeLifecycle(ctx, conn, m); err != nil {
					return
				}
			}
		default:
			return
		}
	}
}

func (g *Gateway) report(ctx context.Context, id string, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return g.Report(ctx, id, m)
}

func acknowledgeLifecycle(ctx context.Context, conn *websocket.Conn, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return wsjson.Write(ctx, conn, Message{Type: "lifecycle_ack", Session: m.Session, RequestID: m.RequestID, UUID: m.UUID, Status: m.Status})
}

func (g *Gateway) Command(ctx context.Context, id, action, relayUUID string) (string, error) {
	if action != "permit" && action != "terminate" {
		return "", errors.New("unsupported command")
	}
	g.mu.Lock()
	p := g.peers[id]
	g.mu.Unlock()
	if p == nil {
		return "", ErrOffline
	}
	if err := g.Check(ctx, id, p.token); err != nil {
		g.Disconnect(id)
		return "", err
	}
	requestID := uuid.NewString()
	ch := make(chan Message, 1)
	g.mu.Lock()
	if g.peers[id] != p || !p.ready || len(p.pending) >= 64 || (action == "permit" && len(p.issued) >= 4096) {
		g.mu.Unlock()
		return "", ErrOffline
	}
	p.pending[requestID] = waiter{uuid: relayUUID, result: ch}
	if action == "permit" {
		p.issued[relayUUID] = time.Now().Add(2 * time.Minute)
	}
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(p.pending, requestID); g.mu.Unlock() }()
	commandCtx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	if action == "permit" && g.Authorize != nil {
		if err := g.Authorize(commandCtx, id, p.token, relayUUID); err != nil {
			g.mu.Lock()
			delete(p.issued, relayUUID)
			g.mu.Unlock()
			return "", err
		}
	}
	m := Message{Type: "command", Session: p.session, RequestID: requestID, UUID: relayUUID, Action: action, ExpiresAt: time.Now().Add(5 * time.Second).UnixMilli()}
	if err := wsjson.Write(commandCtx, p.conn, m); err != nil {
		return "", err
	}
	retry := time.NewTicker(750 * time.Millisecond)
	defer retry.Stop()
	for {
		select {
		case reply := <-ch:
			if (action == "permit" && reply.Status == "permitted") || (action == "terminate" && (reply.Status == "terminated" || reply.Status == "not_found")) {
				return reply.Status, nil
			}
			return "", errors.New("relay rejected command")
		case <-commandCtx.Done():
			return "", commandCtx.Err()
		case <-p.closed:
			return "", ErrOffline
		case <-retry.C:
			// Same identity and deadline: the relay returns its cached result, never a new permit.
			if err := wsjson.Write(commandCtx, p.conn, m); err != nil {
				return "", err
			}
		}
	}
}
