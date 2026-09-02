package events

import "testing"

func TestSubscribeAfterReplaysGapAndContinuesLive(t *testing.T) {
	hub := NewHub()
	first := hub.Publish(SessionCreated, map[string]string{"id": "one"})
	second := hub.Publish(SessionCreated, map[string]string{"id": "two"})

	stream, backlog, unsubscribe := hub.SubscribeAfter(hub.SourceID(), first.Revision)
	defer unsubscribe()
	if len(backlog) != 1 || backlog[0].Revision != second.Revision {
		t.Fatalf("unexpected backlog: %#v", backlog)
	}

	third := hub.Publish(SessionCreated, map[string]string{"id": "three"})
	if live := <-stream; live.Revision != third.Revision {
		t.Fatalf("unexpected live event: %#v", live)
	}
}

func TestSubscribeAfterReplaysNewSourceHistory(t *testing.T) {
	hub := NewHub()
	event := hub.Publish(SessionCreated, map[string]string{"id": "new-source"})
	_, backlog, unsubscribe := hub.SubscribeAfter("previous-api-source", 999)
	defer unsubscribe()
	if len(backlog) != 1 || backlog[0].Revision != event.Revision {
		t.Fatalf("new source backlog missing: %#v", backlog)
	}
}
