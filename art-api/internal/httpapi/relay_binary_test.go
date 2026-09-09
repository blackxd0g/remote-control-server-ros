package httpapi_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/audit"
	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/httpapi"
	"github.com/art-rustdesk/platform/art-api/internal/mfa"
	"github.com/art-rustdesk/platform/art-api/internal/relaygateway"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
	"github.com/coder/websocket"
)

// Opt-in integration test of the actual locally built Rust executable, not a WS mock.
func TestRelayBinaryWSS(t *testing.T) {
	testRelayBinaryWSS(t, false)
}

func TestRelayBinaryDurableRestart(t *testing.T) {
	testRelayBinaryWSS(t, true)
}

func TestRelayBinaryRotationQuarantine(t *testing.T) { testRelayBinaryWSS(t, true, true) }

func TestRelayBinaryLoadOutage(t *testing.T) {
	if os.Getenv("RDS_TEST_RELAY_LOAD") != "1" {
		t.Skip("RDS_TEST_RELAY_LOAD=1 required for 60-second outage/load scenario")
	}
	testRelayBinaryWSS(t, true, false, true)
}

func testRelayBinaryWSS(t *testing.T, restart bool, rotation ...bool) {
	binary := os.Getenv("RDS_TEST_HBBR_BINARY")
	if binary == "" {
		t.Skip("set RDS_TEST_HBBR_BINARY to the locally built HBBR")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	dir := t.TempDir()
	repository, err := sqlstore.Open("sqlite", filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err = repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	port := func() int {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		defer l.Close()
		return l.Addr().(*net.TCPAddr).Port
	}
	dataPort, wsPort := port(), port()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(dataPort))
	now := time.Now().UTC()
	relay := domain.RelayServer{ID: "binary-relay", Name: "Binary relay", Hostname: "127.0.0.1", Port: dataPort, Enabled: true, Health: "healthy", CreatedAt: now, UpdatedAt: now}
	if err = repository.CreateRelayServer(ctx, relay); err != nil {
		t.Fatal(err)
	}
	credential, _ := relaygateway.NewToken()
	enrollment, _ := relaygateway.NewToken()
	if err = repository.PutRelayEnrollment(ctx, relaygateway.Credential{RelayID: relay.ID, EnrollmentHash: relaygateway.Hash(enrollment), EnrollmentExpires: now.Add(time.Minute), Generation: "1", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = repository.ConsumeRelayEnrollment(ctx, relay.ID, relaygateway.Hash(enrollment), relaygateway.Hash(credential), now); err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub()
	manager := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), "art-rustdesk", "art-hbbs", time.Hour)
	service, err := auth.NewService(repository, manager, hub, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("correct horse battery staple")
	if err = repository.CreateUser(ctx, domain.User{ID: "admin", Username: "admin", PasswordHash: hash, Role: domain.RoleAdmin, Enabled: true, TokenVersion: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	mfaService, _ := mfa.New(repository, []byte("test-mfa-secret-0123456789012345"), mfa.ModeOptional, "Test")
	handler := httpapi.New(service, mfaService, audit.New(repository), repository, hub, []byte("binary-test-internal"), httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).Handler()
	admin := login(t, handler)
	_, adminSession, _, err := service.Authenticate(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	type handlerValue struct{ http.Handler }
	var current atomic.Value
	current.Store(handlerValue{handler})
	var offline atomic.Bool
	var socketsMu sync.Mutex
	var sockets []net.Conn
	server, caPEM := relayTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "test API offline", 503)
			return
		}
		current.Load().(handlerValue).ServeHTTP(w, r)
	}), func(conn net.Conn, state http.ConnState) {
		if state == http.StateHijacked {
			socketsMu.Lock()
			sockets = append(sockets, conn)
			socketsMu.Unlock()
		}
	})
	defer server.Close()
	selectRelay := func() int {
		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/internal/v1/relay/select", nil)
		req.Header.Set("X-RDS-Internal-Token", "binary-test-internal")
		response, err := server.Client().Do(req)
		if err != nil {
			return 0
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response.StatusCode
	}
	if selectRelay() != 503 {
		t.Fatal("relay selected before control was ready")
	}
	caPath := filepath.Join(dir, "ca.pem")
	other, wrongCA := relayTLSServer(t, http.NotFoundHandler())
	other.Close()
	if err = os.WriteFile(caPath, wrongCA, 0600); err != nil {
		t.Fatal(err)
	}
	saved, _ := json.Marshal(map[string]string{"credential": credential})
	credentialPath := filepath.Join(dir, "relay.json")
	if err = os.WriteFile(credentialPath, saved, 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the legacy UDP address occupied: secure mode must not bind it at all.
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	logFile, err := os.Create(filepath.Join(dir, "hbbr.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	command := exec.CommandContext(ctx, binary)
	command.Env = append(os.Environ(), "RDS_RELAY_CONTROL_MODE=wss", "RDS_RELAY_OUTBOX_DIR="+filepath.Join(dir, "outbox"), "RDS_RELAY_CONTROL_URL=wss"+strings.TrimPrefix(server.URL, "https")+"/api/relay/"+relay.ID+"/control", "RDS_RELAY_CREDENTIAL_FILE="+credentialPath, "SSL_CERT_FILE="+caPath, "RDS_HBBR_LISTEN="+address, "RDS_HBBR_WEBSOCKET_LISTEN="+net.JoinHostPort("127.0.0.1", strconv.Itoa(wsPort)), "RDS_HBBR_CONTROL_LISTEN="+udp.LocalAddr().String(), "RDS_INTERNAL_SECRET_FILE=/does-not-exist", "RUST_LOG=info")
	command.Stdout, command.Stderr = logFile, logFile
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		if t.Failed() {
			data, _ := os.ReadFile(logFile.Name())
			t.Log(string(data))
		}
	}()
	rejectedCertificate := false
	for range 40 {
		data, _ := os.ReadFile(logFile.Name())
		if strings.Contains(string(data), "invalid peer certificate") {
			rejectedCertificate = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !rejectedCertificate {
		t.Fatal("Rust client did not reject the untrusted server certificate")
	}
	if err = os.WriteFile(caPath, caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	permit := func(id string) int {
		input, _ := json.Marshal(map[string]string{"uuid": id, "address": address})
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/internal/v1/relay/permit", bytes.NewReader(input))
		req.Header.Set("X-RDS-Internal-Token", "binary-test-internal")
		response, err := server.Client().Do(req)
		if err != nil {
			return 0
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response.StatusCode
	}
	ready := false
	for range 50 {
		if permit("readiness-uuid") == 200 {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("secure relay did not become ready")
	}
	if selectRelay() != 200 {
		t.Fatal("ready relay not selected")
	}
	connect := func(id string) net.Conn {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		nested := append([]byte{0x12, byte(len(id))}, []byte(id)...)
		message := append([]byte{0x92, 0x01, byte(len(nested))}, nested...)
		frame := append([]byte{byte(len(message) << 2)}, message...)
		if _, err = conn.Write(frame); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	denied := connect("no-permit-uuid")
	var b [1]byte
	_, err = denied.Read(b[:])
	denied.Close()
	if err == nil {
		t.Fatal("unpermitted peer accepted")
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("unpermitted peer was not closed")
	}
	id := "binary-pair-uuid"
	if permit(id) != 200 {
		t.Fatal("permit failed")
	}
	a, bconn := connect(id), connect(id)
	defer a.Close()
	defer bconn.Close()
	payload := bytes.Repeat([]byte("WSS relay payload"), 2048)
	go func() { _, _ = a.Write(payload) }()
	received := make([]byte, len(payload))
	if _, err = io.ReadFull(bconn, received); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, received) {
		t.Fatal("relay payload mismatch")
	}
	if _, err = bconn.Write([]byte("back")); err != nil {
		t.Fatal(err)
	}
	received = make([]byte, 4)
	if _, err = io.ReadFull(a, received); err != nil || string(received) != "back" {
		t.Fatal("reverse traffic failed")
	}
	connectWS := func(id string) net.Conn {
		ws, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+strconv.Itoa(wsPort)+"/ws/relay", nil)
		if err != nil {
			t.Fatal(err)
		}
		nested := append([]byte{0x12, byte(len(id))}, []byte(id)...)
		message := append([]byte{0x92, 0x01, byte(len(nested))}, nested...)
		if err = ws.Write(ctx, websocket.MessageBinary, message); err != nil {
			t.Fatal(err)
		}
		conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		return conn
	}
	for _, kind := range []string{"ws-ws", "tcp-ws"} {
		pairID := "binary-" + kind + "-uuid"
		if permit(pairID) != 200 {
			t.Fatal("WS permit failed")
		}
		var left net.Conn
		if kind == "ws-ws" {
			left = connectWS(pairID)
		} else {
			left = connect(pairID)
		}
		right := connectWS(pairID)
		go func() { _, _ = left.Write(payload) }()
		copyBuffer := make([]byte, len(payload))
		if _, err = io.ReadFull(right, copyBuffer); err != nil || !bytes.Equal(payload, copyBuffer) {
			t.Fatalf("%s forward failed", kind)
		}
		if _, err = right.Write([]byte("back")); err != nil {
			t.Fatal(err)
		}
		if _, err = io.ReadFull(left, received); err != nil || string(received) != "back" {
			t.Fatalf("%s reverse failed", kind)
		}
		left.Close()
		right.Close()
	}
	killID := "terminate-pair-uuid"
	loadTest := len(rotation) > 1 && rotation[1]
	if loadTest {
		start := time.Now()
		block := bytes.Repeat([]byte("load"), 16384)
		for i := 0; i < 64; i++ {
			pairID := "load-pair-" + strconv.Itoa(i)
			if permit(pairID) != 200 {
				t.Fatal("load permit rejected")
			}
			left, right := connect(pairID), connect(pairID)
			written := make(chan error, 1)
			go func() { _, e := left.Write(block); written <- e }()
			output := make([]byte, len(block))
			_, e := io.ReadFull(right, output)
			writeErr := <-written
			left.Close()
			right.Close()
			if e != nil || writeErr != nil || !bytes.Equal(output, block) {
				t.Fatal("load data mismatch")
			}
		}
		t.Logf("64 TCP pairs, 4 MiB verified payload, elapsed=%s", time.Since(start))
	}
	if restart {
		// Break the control path while the TCP pair is still alive, then restart both consumers.
		offline.Store(true)
		socketsMu.Lock()
		for _, socket := range sockets {
			_ = socket.Close()
		}
		socketsMu.Unlock()
		if loadTest {
			_ = a.SetDeadline(time.Now().Add(80 * time.Second))
			_ = bconn.SetDeadline(time.Now().Add(80 * time.Second))
			for i := 0; i < 60; i++ {
				if _, e := a.Write([]byte("live")); e != nil {
					t.Fatal(e)
				}
				if _, e := io.ReadFull(bconn, received); e != nil || string(received) != "live" {
					t.Fatal("data lost during control outage")
				}
				time.Sleep(time.Second)
			}
			t.Log("60-second control outage: established data pair survived; restarting HBBR/API next")
		}
		_ = command.Process.Kill()
		_ = command.Wait()
		// Rebuild the API/gateway with empty process-local state against the same durable repository.
		rotating := len(rotation) > 0 && rotation[0]
		if rotating {
			enrollment, _ := relaygateway.NewToken()
			credential, _ = relaygateway.NewToken()
			if err = repository.PutRelayEnrollment(ctx, relaygateway.Credential{RelayID: relay.ID, Generation: "2", EnrollmentHash: relaygateway.Hash(enrollment), EnrollmentExpires: time.Now().Add(time.Minute), UpdatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if err = repository.ConsumeRelayEnrollment(ctx, relay.ID, relaygateway.Hash(enrollment), relaygateway.Hash(credential), time.Now()); err != nil {
				t.Fatal(err)
			}
			saved, _ := json.Marshal(map[string]string{"credential": credential})
			if err = os.WriteFile(credentialPath, saved, 0600); err != nil {
				t.Fatal(err)
			}
		}
		fresh := httpapi.New(service, mfaService, audit.New(repository), repository, events.NewHub(), []byte("binary-test-internal"), httpapi.NewLoginLimiter(5, time.Minute, time.Minute)).Handler()
		current.Store(handlerValue{fresh})
		environment := command.Env
		command = exec.CommandContext(ctx, binary)
		command.Env = environment
		command.Stdout, command.Stderr = logFile, logFile
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		// The relay must durably produce closed for the interrupted pair before API recovery.
		outboxPath := filepath.Join(dir, "outbox", "outbox.json")
		var pending struct {
			Events     []struct{ ID, UUID, Status string }
			Quarantine []struct{ ID, UUID, Status, Reason string }
		}
		waitUntil := func(check func() bool) {
			t.Helper()
			for range 200 {
				if check() {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatal("durable recovery condition timed out")
		}
		waitUntil(func() bool {
			data, _ := os.ReadFile(outboxPath)
			_ = json.Unmarshal(data, &pending)
			for _, event := range pending.Events {
				if event.UUID == id && event.Status == "closed" {
					return true
				}
			}
			return false
		})
		offline.Store(false)
		if !rotating {
			waitUntil(func() bool {
				record, e := repository.ConnectionRecord(ctx, "relay:"+id)
				return e == nil && record.ClosedAt != nil
			})
		}
		waitUntil(func() bool {
			data, e := os.ReadFile(outboxPath)
			if e != nil {
				return false
			}
			pending.Events = nil
			return json.Unmarshal(data, &pending) == nil && len(pending.Events) == 0
		})
		if rotating {
			if len(pending.Quarantine) == 0 {
				t.Fatal("rotation lost pending events instead of quarantining")
			}
			for _, event := range pending.Quarantine {
				if event.Reason != "credential_rotated" {
					t.Fatal("bad quarantine reason")
				}
			}
			if record, e := repository.ConnectionRecord(ctx, "relay:"+id); e == nil && record.ClosedAt != nil {
				t.Fatal("old generation changed live projection")
			}
			waitUntil(func() bool {
				req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/admin/relay-servers/"+relay.ID+"/delivery", nil)
				req.Header.Set("Authorization", "Bearer "+admin)
				response, e := server.Client().Do(req)
				if e != nil {
					return false
				}
				defer response.Body.Close()
				var status struct{ Runtime relaygateway.Observation }
				if json.NewDecoder(response.Body).Decode(&status) != nil {
					return false
				}
				return response.StatusCode == 200 && status.Runtime.Connected && status.Runtime.Delivery != nil && status.Runtime.Delivery.Quarantined > 0
			})
			testQuarantineMaintenance(t, ctx, server, relay.ID, admin, dir)
			auditEvents, e := repository.ListAudit(ctx, 100)
			if e != nil {
				t.Fatal(e)
			}
			requested, completed, exported := false, false, false
			for _, event := range auditEvents {
				if event.Type == "relay_quarantine_archive_requested" {
					requested = true
				}
				if event.Type == "relay_quarantine_archived" && event.Result == "success" {
					completed = true
				}
				if event.Type == "relay_quarantine_exported" && event.Result == "success" {
					exported = true
				}
			}
			if !requested || !completed || !exported {
				t.Fatal("quarantine audit incomplete")
			}
		}
		if permit("post-restart-uuid") != 200 {
			t.Fatal("relay did not accept new work after restart")
		}
		left, right := connect("post-restart-uuid"), connect("post-restart-uuid")
		defer left.Close()
		defer right.Close()
		if _, err = left.Write([]byte("back")); err != nil {
			t.Fatal(err)
		}
		if _, err = io.ReadFull(right, received); err != nil || string(received) != "back" {
			t.Fatal("data forwarding failed after restart")
		}
		return
	}
	if permit(killID) != 200 {
		t.Fatal("terminate pair permit failed")
	}
	killA, killB := connect(killID), connect(killID)
	defer killA.Close()
	defer killB.Close()
	if _, err = killA.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(killB, received); err != nil {
		t.Fatal(err)
	}
	record := domain.ConnectionRecord{Key: "relay:" + killID, Transport: "relay", RelayUUID: killID, RelayServer: address, ActorSessionID: adminSession.ID, ActorUserID: "admin", StartedAt: now, LastSeenAt: now}
	if err = repository.UpsertConnection(ctx, record); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/admin/connections/contain", strings.NewReader(`{"key":"relay:`+killID+`"}`))
	request.Header.Set("Authorization", "Bearer "+admin)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var contained map[string]any
	_ = json.NewDecoder(response.Body).Decode(&contained)
	response.Body.Close()
	if response.StatusCode != 200 || contained["transport_interrupted"] != true {
		t.Fatal("relay termination not confirmed")
	}
	for _, conn := range []net.Conn{killA, killB} {
		_, err = conn.Read(b[:])
		if err == nil {
			t.Fatal("terminated peer stayed open")
		}
		if e, ok := err.(net.Error); ok && e.Timeout() {
			t.Fatal("terminated peer timed out rather than closing")
		}
	}
	if err = repository.RevokeRelayCredential(ctx, relay.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if permit("after-revoke-uuid") == 200 {
		t.Fatal("revoked relay received permit")
	}
	if selectRelay() != 503 {
		t.Fatal("revoked relay selected")
	}
	// Losing control must not terminate already established data pairs.
	if _, err = a.Write([]byte("live")); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(bconn, received); err != nil || string(received) != "live" {
		t.Fatal("control revocation interrupted established data")
	}
}

func relayTLSServer(t *testing.T, handler http.Handler, onState ...func(net.Conn, http.ConnState)) (*httptest.Server, []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Relay test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), caPEM...)
	pair, err := tls.X509KeyPair(chain, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	if len(onState) > 0 {
		server.Config.ConnState = onState[0]
	}
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	server.StartTLS()
	return server, caPEM
}
