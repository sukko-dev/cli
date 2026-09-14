package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const defaultWSPath = "/ws"

// ServerMessage represents a message received from the gateway.
type ServerMessage struct {
	Type    string          `json:"type"`
	Channel string          `json:"channel,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	// Mid is the stable message identity: an opaque string (at most 64 chars)
	// that is identical on every copy of the same message — live broadcast,
	// gap replay, and history. Omitted when the server does not send one
	// (e.g. non-message frames or entries stored before mid was introduced).
	Mid string `json:"mid,omitempty"`
}

// WSClient is a WebSocket client for communicating with the Sukko gateway.
type WSClient struct {
	conn      net.Conn
	gateway   string
	closeOnce sync.Once
}

// DialOption configures the WebSocket connection.
type DialOption func(*dialConfig)

type dialConfig struct {
	token  string
	apiKey string
}

// WithToken sets the JWT token for authentication.
func WithToken(token string) DialOption {
	return func(c *dialConfig) { c.token = token }
}

// WithAPIKey sets the API key for authentication.
func WithAPIKey(apiKey string) DialOption {
	return func(c *dialConfig) { c.apiKey = apiKey }
}

// Dial connects to the gateway WebSocket endpoint.
func Dial(ctx context.Context, gatewayURL string, opts ...DialOption) (*WSClient, error) {
	if gatewayURL == "" {
		return nil, errors.New("dial: gateway URL is required")
	}

	cfg := &dialConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Build WebSocket URL
	wsURL := gatewayURL + defaultWSPath

	// Set up headers
	header := http.Header{}
	if cfg.token != "" {
		header.Set("Authorization", "Bearer "+cfg.token)
	}
	if cfg.apiKey != "" {
		header.Set("X-API-Key", cfg.apiKey)
	}

	dialer := ws.Dialer{Header: ws.HandshakeHeaderHTTP(header)}
	conn, _, _, err := dialer.Dial(ctx, wsURL)
	if err != nil {
		return nil, fmt.Errorf("websocket dial %s: %w", wsURL, err)
	}

	return &WSClient{conn: conn, gateway: gatewayURL}, nil
}

// Subscribe sends a subscribe message for the given channels.
func (c *WSClient) Subscribe(channels []string) error {
	msg := map[string]any{
		"type": "subscribe",
		"data": map[string]any{
			"channels": channels,
		},
	}
	return c.writeJSON(msg)
}

// Unsubscribe sends an unsubscribe message for the given channels.
func (c *WSClient) Unsubscribe(channels []string) error {
	msg := map[string]any{
		"type": "unsubscribe",
		"data": map[string]any{
			"channels": channels,
		},
	}
	return c.writeJSON(msg)
}

// Publish sends a publish message on the given channel.
func (c *WSClient) Publish(channel string, data json.RawMessage) error {
	msg := map[string]any{
		"type": "publish",
		"data": map[string]any{
			"channel": channel,
			"data":    data,
		},
	}
	return c.writeJSON(msg)
}

// SendAuth sends a JWT token for mid-connection auth refresh.
func (c *WSClient) SendAuth(token string) error {
	msg := map[string]any{
		"type": "auth",
		"data": map[string]any{
			"token": token,
		},
	}
	return c.writeJSON(msg)
}

// ReadMessage reads the next message from the server.
func (c *WSClient) ReadMessage() (*ServerMessage, error) {
	data, err := wsutil.ReadServerText(c.conn)
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	var msg ServerMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}

	return &msg, nil
}

// Close closes the WebSocket connection. It is safe to call multiple times.
func (c *WSClient) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		if c.conn != nil {
			if err := c.conn.Close(); err != nil {
				closeErr = fmt.Errorf("close websocket: %w", err)
			}
		}
	})
	return closeErr
}

func (c *WSClient) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	if err := wsutil.WriteClientText(c.conn, data); err != nil {
		return fmt.Errorf("write message: %w", err)
	}

	return nil
}
