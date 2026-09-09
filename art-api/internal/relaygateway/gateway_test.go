package relaygateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func testPeer(t *testing.T, g *Gateway, ready bool) (context.Context, *websocket.Conn, string) {
	t.Helper()
	g.Check = func(context.Context, string, string) error { return nil }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { g.Serve(w, r, "relay", "test") }))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	var hello Message
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		t.Fatal(err)
	}
	if ready {
		if err := wsjson.Write(ctx, conn, Message{Type: "ready", Session: hello.Session}); err != nil {
			t.Fatal(err)
		}
		for !g.Online("relay") {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Millisecond):
			}
		}
	}
	return ctx, conn, hello.Session
}

func permit(t *testing.T, ctx context.Context, g *Gateway, conn *websocket.Conn) Message {
	t.Helper()
	result := make(chan error, 1)
	go func() { _, err := g.Command(ctx, "relay", "permit", "test-relay-uuid"); result <- err }()
	var command Message
	if err := wsjson.Read(ctx, conn, &command); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, conn, Message{Type: "ack", Session: command.Session, RequestID: command.RequestID, UUID: command.UUID, Status: "permitted"}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	return command
}

func TestCommandRetriesSameIdentityAndDisconnectUnblocks(t *testing.T) {
	g := New()
	ctx, conn, _ := testPeer(t, g, true)
	result := make(chan error, 1)
	go func() { _, err := g.Command(ctx, "relay", "permit", "test-relay-uuid"); result <- err }()
	var first, second Message
	if err := wsjson.Read(ctx, conn, &first); err != nil {
		t.Fatal(err)
	}
	// Drop the first acknowledgement, then verify the retry cannot grant a fresh permit.
	if err := wsjson.Read(ctx, conn, &second); err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("retry changed identity/deadline: %+v %+v", first, second)
	}
	if err := wsjson.Write(ctx, conn, Message{Type: "ack", Session: second.Session, RequestID: second.RequestID, UUID: second.UUID, Status: "permitted"}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	go func() { _, err := g.Command(ctx, "relay", "terminate", first.UUID); result <- err }()
	if err := wsjson.Read(ctx, conn, &second); err != nil {
		t.Fatal(err)
	}
	g.Disconnect("relay")
	select {
	case err := <-result:
		if !errors.Is(err, ErrOffline) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect did not unblock command")
	}
}

func TestLifecycleAckDedupAndConflictingReplay(t *testing.T) {
	g := New()
	var calls atomic.Int32
	g.Report = func(context.Context, string, Message) error { calls.Add(1); return nil }
	ctx, conn, session := testPeer(t, g, true)
	command := permit(t, ctx, g, conn)
	for _, status := range []string{"active", "closed"} {
		report := Message{Type: "lifecycle", Session: session, RequestID: status, UUID: command.UUID, Status: status}
		for range 2 {
			if err := wsjson.Write(ctx, conn, report); err != nil {
				t.Fatal(err)
			}
			var ack Message
			if err := wsjson.Read(ctx, conn, &ack); err != nil {
				t.Fatal(err)
			}
			if ack.Type != "lifecycle_ack" || ack.RequestID != report.RequestID || ack.UUID != report.UUID || ack.Status != status {
				t.Fatalf("bad ack: %+v", ack)
			}
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("duplicate side effects: %d", calls.Load())
	}
	if err := wsjson.Write(ctx, conn, Message{Type: "lifecycle", Session: session, RequestID: "closed", UUID: "foreign-uuid", Status: "closed"}); err != nil {
		t.Fatal(err)
	}
	var response Message
	if err := wsjson.Read(ctx, conn, &response); err == nil {
		t.Fatal("conflicting event ID accepted")
	}
	if calls.Load() != 2 {
		t.Fatal("conflicting event reported")
	}
}

func TestLifecycleFailureNeverAcknowledged(t *testing.T) {
	g := New()
	g.Report = func(context.Context, string, Message) error { return errors.New("storage unavailable") }
	ctx, conn, session := testPeer(t, g, true)
	command := permit(t, ctx, g, conn)
	if err := wsjson.Write(ctx, conn, Message{Type: "lifecycle", Session: session, RequestID: "event-1", UUID: command.UUID, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	var response Message
	if err := wsjson.Read(ctx, conn, &response); err == nil {
		t.Fatalf("failed report acknowledged: %+v", response)
	}
}

func TestReportsBeforeReadyRejected(t *testing.T) {
	g := New()
	var calls atomic.Int32
	g.Report = func(context.Context, string, Message) error { calls.Add(1); return nil }
	ctx, conn, session := testPeer(t, g, false)
	if err := wsjson.Write(ctx, conn, Message{Type: "telemetry", Session: session}); err != nil {
		t.Fatal(err)
	}
	var response Message
	if err := wsjson.Read(ctx, conn, &response); err == nil {
		t.Fatal("unready report accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("unready peer mutated telemetry")
	}
}

func TestMissingReadyExpiresAndAllowsReconnect(t *testing.T) {
	g := New()
	ctx, conn, _ := testPeer(t, g, false)
	var response Message
	if err := wsjson.Read(ctx, conn, &response); err == nil {
		t.Fatal("silent unready connection remained open")
	}
	if g.Online("relay") {
		t.Fatal("unready peer was selectable")
	}
	// The expired socket must not reserve the relay identity indefinitely.
	_, _, _ = testPeer(t, g, true)
}

func TestCommandWithoutAckTimesOutAfterBoundedRetries(t *testing.T) {
	g := New()
	ctx, conn, _ := testPeer(t, g, true)
	result := make(chan error, 1)
	go func() { _, err := g.Command(ctx, "relay", "permit", "timeout-relay-uuid"); result <- err }()
	first := Message{}
	for i := 0; i < 4; i++ {
		var command Message
		if err := wsjson.Read(ctx, conn, &command); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = command
		} else if command != first {
			t.Fatal("retry renewed command")
		}
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("missing ack reported as %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing ack did not time out")
	}
}
