package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestParseAuditQuery(t *testing.T) {
	request := httptest.NewRequest("GET", "/?limit=50&offset=100&type=connection_denied&from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z&search=device", nil)
	query, err := parseAuditQuery(request)
	if err != nil || query.Limit != 50 || query.Offset != 100 || query.Type != "connection_denied" || query.From == nil || query.To == nil || query.Search != "device" {
		t.Fatalf("unexpected query: %+v err=%v", query, err)
	}
}

func TestParseAuditQueryRejectsInvalidRange(t *testing.T) {
	request := httptest.NewRequest("GET", "/?from=2026-02-01T00:00:00Z&to=2026-01-01T00:00:00Z", nil)
	if _, err := parseAuditQuery(request); err == nil {
		t.Fatal("invalid date range was accepted")
	}
}
