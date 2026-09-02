package webhook

import "testing"

func TestWebhookSecretIsStableAndScoped(t *testing.T) {
	service := New(nil, nil, []byte("0123456789abcdef0123456789abcdef"), false)
	first := service.Secret("one")
	if first == "" || first != service.Secret("one") || first == service.Secret("two") {
		t.Fatal("derived webhook secret must be stable and unique per webhook")
	}
}

func TestWebhookURLValidationRejectsSSRFAndInsecureTransport(t *testing.T) {
	service := New(nil, nil, []byte("0123456789abcdef0123456789abcdef"), false)
	for _, candidate := range []string{"http://example.com/hook", "https://127.0.0.1/hook", "https://[::1]/hook", "https://user:pass@example.com/hook"} {
		if err := service.ValidateURL(candidate); err == nil {
			t.Fatalf("unsafe URL accepted: %s", candidate)
		}
	}
}

func TestPrivateWebhookDestinationRequiresExplicitOptIn(t *testing.T) {
	service := New(nil, nil, []byte("0123456789abcdef0123456789abcdef"), true)
	if err := service.ValidateURL("https://127.0.0.1/hook"); err != nil {
		t.Fatalf("explicitly allowed private URL rejected: %v", err)
	}
}
