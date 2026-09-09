package httpapi_test

import (
	"context"
	"encoding/json"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testQuarantineMaintenance(t *testing.T, ctx context.Context, server *httptest.Server, id, admin, dir string) {
	t.Helper()
	call := func(method, path, token, body string, output any) int {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, server.URL+"/api/admin/relay-servers/"+id+"/quarantine"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if output != nil {
			if err = json.NewDecoder(response.Body).Decode(output); err != nil {
				t.Fatal(err)
			}
		}
		return response.StatusCode
	}
	for _, p := range []string{"", "/export", "/archive"} {
		method := "GET"
		if p == "/archive" {
			method = "POST"
		}
		if code := call(method, p, "", "", nil); code < 400 {
			t.Fatal("anonymous quarantine access")
		}
	}
	var page relaygateway.QuarantinePage
	if call("GET", "", admin, "", &page) != 200 || page.Total == 0 {
		t.Fatal("quarantine list unavailable")
	}
	if call("GET", "?offset=-1", admin, "", nil) != 400 {
		t.Fatal("negative pagination accepted")
	}
	var export struct {
		Revision string
		Events   []relaygateway.QuarantineEvent
	}
	if call("GET", "/export?revision="+page.Revision, admin, "", &export) != 200 || len(export.Events) != page.Total || export.Revision != page.Revision {
		t.Fatal("incomplete export")
	}
	if call("POST", "/archive", admin, `{"revision":"`+page.Revision+`"}`, nil) != 400 {
		t.Fatal("archive without confirmation")
	}
	if call("POST", "/archive", admin, `{"revision":"00000000-0000-4000-8000-000000000001","confirm":true}`, nil) != 409 {
		t.Fatal("stale revision cleared quarantine")
	}
	body := `{"revision":"` + page.Revision + `","confirm":true}`
	if call("POST", "/archive", admin, body, nil) != 200 {
		t.Fatal("archive failed")
	}
	if call("POST", "/archive", admin, body, nil) != 200 {
		t.Fatal("archive retry failed")
	}
	var after relaygateway.QuarantinePage
	if call("GET", "", admin, "", &after) != 200 || after.Total != 0 {
		t.Fatal("archive did not free quarantine")
	}
	data, err := os.ReadFile(filepath.Join(dir, "outbox", "quarantine-"+page.Revision+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var backup relaygateway.QuarantinePage
	if json.Unmarshal(data, &backup) != nil || len(backup.Events) != page.Total || backup.Events[0].ID != export.Events[0].ID {
		t.Fatal("backup lost event identity")
	}
}
