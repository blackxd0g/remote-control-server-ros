package config

import "testing"

func TestRDSNamespaceOverridesLegacyARTValue(t *testing.T) {
	t.Setenv("ART_DATA_DIR", "/legacy")
	t.Setenv("RDS_DATA_DIR", "/current")
	if value := env("ART_DATA_DIR", "/fallback"); value != "/current" {
		t.Fatalf("expected RDS value, got %q", value)
	}
}

func TestLegacyARTNamespaceRemainsCompatible(t *testing.T) {
	t.Setenv("RDS_DATA_DIR", "")
	t.Setenv("ART_DATA_DIR", "/legacy")
	if value := env("ART_DATA_DIR", "/fallback"); value != "/legacy" {
		t.Fatalf("expected legacy ART value, got %q", value)
	}
}
