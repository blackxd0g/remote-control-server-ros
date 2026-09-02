package mfa

import (
	"testing"
	"time"
)

func TestValidateRFC6238VectorAndWindow(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if !Validate(secret, "287082", time.Unix(59, 0)) {
		t.Fatal("expected RFC 6238 six-digit vector to validate")
	}
	if !Validate(secret, "287082", time.Unix(89, 0)) {
		t.Fatal("expected previous time-step to validate within allowed clock skew")
	}
	if Validate(secret, "287082", time.Unix(119, 0)) {
		t.Fatal("code outside the allowed clock-skew window was accepted")
	}
	if Validate(secret, "not-a-code", time.Unix(59, 0)) {
		t.Fatal("non-numeric code was accepted")
	}
}
