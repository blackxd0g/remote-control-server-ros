package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestParseSessionQuery(t *testing.T) {
	request := httptest.NewRequest("GET", "/?limit=50&offset=100&status=active&search=desktop&user_id=user-1", nil)
	query, reason := parseSessionQuery(request)
	if reason != "" || query.Limit != 50 || query.Offset != 100 || query.Status != "active" || query.Search != "desktop" || query.UserID != "user-1" {
		t.Fatalf("unexpected query: %+v reason=%s", query, reason)
	}
}

func TestParseSessionQueryRejectsStatus(t *testing.T) {
	request := httptest.NewRequest("GET", "/?status=unknown", nil)
	if _, reason := parseSessionQuery(request); reason == "" {
		t.Fatal("invalid status was accepted")
	}
}
