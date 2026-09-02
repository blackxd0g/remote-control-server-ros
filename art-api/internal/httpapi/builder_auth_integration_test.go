package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/audit"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/httpapi"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
)

func TestBuilderBootstrapAndWorkerCredentialsAreSeparated(t *testing.T) {
	repository, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "builder.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	bootstrap := "builder-bootstrap-credential-0123456789"
	handler := httpapi.New(nil, nil, audit.New(repository), repository, events.NewHub(), []byte("internal-secret"), httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).EnableBuilderAPI([]byte(bootstrap)).Handler()
	body := `{"worker_id":"worker-a","name":"Worker A","hostname":"builder.test","version":"2.0","formats":["installer"],"platforms":["windows"],"architectures":["amd64"],"concurrency":1}`
	response := builderRequest(handler, http.MethodPost, "/internal/v1/builders/register", body, bootstrap)
	var registration struct {
		WorkerToken string `json:"worker_token"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &registration) != nil || len(registration.WorkerToken) < 40 {
		t.Fatalf("registration failed: %d %s", response.Code, response.Body.String())
	}

	response = builderRequest(handler, http.MethodPost, "/internal/v1/builders/heartbeat", body, bootstrap)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap credential retained worker access: %d", response.Code)
	}
	response = builderRequest(handler, http.MethodPost, "/internal/v1/builders/heartbeat", body, registration.WorkerToken)
	if response.Code != http.StatusOK {
		t.Fatalf("worker heartbeat failed: %d %s", response.Code, response.Body.String())
	}
	spoofed := strings.Replace(body, "worker-a", "worker-b", 1)
	response = builderRequest(handler, http.MethodPost, "/internal/v1/builders/heartbeat", spoofed, registration.WorkerToken)
	if response.Code != http.StatusForbidden {
		t.Fatalf("worker identity spoof accepted: %d", response.Code)
	}
}

func builderRequest(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
