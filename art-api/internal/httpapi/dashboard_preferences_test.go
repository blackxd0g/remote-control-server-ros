package httpapi

import "testing"

func TestNormalizeDashboardLayout(t *testing.T) {
	value, valid := normalizeDashboardLayout(dashboardLayout{
		Order:  []string{"relay-load", "unknown", "relay-load", "users"},
		Hidden: []string{"users", "unknown", "users"},
	})
	if !valid {
		t.Fatal("expected a partially visible layout to be valid")
	}
	if value.Version != 1 || len(value.Order) != len(dashboardWidgetIDs) || value.Order[0] != "relay-load" || value.Order[1] != "users" {
		t.Fatalf("unexpected normalized order: %#v", value)
	}
	if len(value.Hidden) != 1 || value.Hidden[0] != "users" {
		t.Fatalf("unexpected normalized hidden widgets: %#v", value.Hidden)
	}
}

func TestNormalizeDashboardLayoutRejectsAllHidden(t *testing.T) {
	_, valid := normalizeDashboardLayout(dashboardLayout{Hidden: append([]string(nil), dashboardWidgetIDs...)})
	if valid {
		t.Fatal("expected an all-hidden layout to be rejected")
	}
}
