package relaycontrol

import "testing"

func TestControlAddress(t *testing.T) {
	for input, expected := range map[string]string{
		"relay.example:21117": "relay.example:21119",
		"[2001:db8::1]:21117": "[2001:db8::1]:21119",
	} {
		actual, err := controlAddress(input)
		if err != nil || actual != expected {
			t.Fatalf("controlAddress(%q) = %q, %v; want %q", input, actual, err, expected)
		}
	}
}
