package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuilderAuthenticationUsesDedicatedBearerToken(t *testing.T) {
	token := []byte("0123456789abcdef0123456789abcdef")
	server := &Server{builderToken: token}
	called := false
	handler := server.requireBuilder(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		called = true
		response.WriteHeader(http.StatusNoContent)
	}))

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/internal/v1/client-builds/claim", nil))
	if unauthorized.Code != http.StatusUnauthorized || called {
		t.Fatalf("unauthorized request passed: status=%d called=%v", unauthorized.Code, called)
	}

	authorizedRequest := httptest.NewRequest(http.MethodPost, "/internal/v1/client-builds/claim", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer "+string(token))
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusNoContent || !called {
		t.Fatalf("authorized request rejected: status=%d called=%v", authorized.Code, called)
	}
}
