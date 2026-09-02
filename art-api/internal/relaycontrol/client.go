package relaycontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("relay stream not found")

type Client struct {
	secret  string
	timeout time.Duration
}

type command struct {
	Action    string `json:"action"`
	Token     string `json:"token"`
	UUID      string `json:"uuid"`
	RequestID string `json:"request_id"`
}

type response struct {
	RequestID string `json:"request_id"`
	UUID      string `json:"uuid"`
	Status    string `json:"status"`
}

func New(secret []byte) *Client {
	return &Client{secret: strings.TrimSpace(string(secret)), timeout: 1500 * time.Millisecond}
}

func (c *Client) Terminate(ctx context.Context, relayServer, relayUUID string) error {
	address, err := controlAddress(relayServer)
	if err != nil {
		return err
	}
	requestID := uuid.NewString()
	payload, err := json.Marshal(command{Action: "terminate", Token: c.secret, UUID: relayUUID, RequestID: requestID})
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: c.timeout}
	connection, err := dialer.DialContext(ctx, "udp", address)
	if err != nil {
		return fmt.Errorf("connect relay control: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(c.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	if _, err = connection.Write(payload); err != nil {
		return fmt.Errorf("send relay termination: %w", err)
	}
	buffer := make([]byte, 2048)
	length, err := connection.Read(buffer)
	if err != nil {
		return fmt.Errorf("relay termination acknowledgement: %w", err)
	}
	var acknowledgement response
	if err = json.Unmarshal(buffer[:length], &acknowledgement); err != nil {
		return fmt.Errorf("decode relay acknowledgement: %w", err)
	}
	if acknowledgement.RequestID != requestID || acknowledgement.UUID != relayUUID {
		return errors.New("relay acknowledgement correlation mismatch")
	}
	switch acknowledgement.Status {
	case "terminated":
		return nil
	case "not_found":
		return ErrNotFound
	default:
		return fmt.Errorf("unexpected relay acknowledgement: %s", acknowledgement.Status)
	}
}

func controlAddress(relayServer string) (string, error) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(relayServer))
	if err != nil {
		return "", fmt.Errorf("invalid relay server address: %w", err)
	}
	return net.JoinHostPort(host, "21119"), nil
}
