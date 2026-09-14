package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestDial_EmptyURL(t *testing.T) {
	t.Parallel()

	_, err := Dial(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestDial_InvalidURL(t *testing.T) {
	t.Parallel()

	_, err := Dial(context.Background(), "not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestDial_ContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Dial(ctx, "ws://localhost:59999")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestWSClient_CloseIdempotent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := ws.HTTPUpgrader{}
		conn, _, _, err := upgrader.Upgrade(r, w)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, err := wsutil.ReadClientText(conn); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:] // http -> ws
	client, err := Dial(context.Background(), wsURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Close multiple times — should not panic
	if err := client.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("second close should be no-op, got: %v", err)
	}
}

func TestWSClient_SubscribeAndRead(t *testing.T) {
	t.Parallel()

	serverMsg := ServerMessage{
		Type:    "message",
		Channel: "test.channel",
		Data:    json.RawMessage(`{"price":42}`),
		Mid:     "9c5b1f0a3d2e6c47-2-1234",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := ws.HTTPUpgrader{}
		conn, _, _, err := upgrader.Upgrade(r, w)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read subscribe message from client
		if _, err := wsutil.ReadClientText(conn); err != nil {
			return
		}

		// Send a message back
		resp, _ := json.Marshal(serverMsg)
		_ = wsutil.WriteServerText(conn, resp)
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:]
	client, err := Dial(context.Background(), wsURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := client.Subscribe([]string{"test.channel"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Type != "message" {
		t.Errorf("type = %q, want message", msg.Type)
	}
	if msg.Channel != "test.channel" {
		t.Errorf("channel = %q, want test.channel", msg.Channel)
	}
	if msg.Mid != "9c5b1f0a3d2e6c47-2-1234" {
		t.Errorf("mid = %q, want 9c5b1f0a3d2e6c47-2-1234", msg.Mid)
	}
}

// TestServerMessage_MidJSONRoundTrip — mid is the stable message identity
// (opaque, ≤64 chars, identical across live/replay/history copies). The CLI
// must carry it through 'sukko subscribe --output json' re-serialization
// (printJSON re-marshals the decoded ServerMessage) and omit it when the
// server did not send one.
func TestServerMessage_MidJSONRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("mid present survives round-trip", func(t *testing.T) {
		t.Parallel()

		wire := []byte(`{"type":"message","channel":"acme.trades","data":{"px":1},"mid":"4f8a2e6b0c9d1735-0-99"}`)
		var msg ServerMessage
		if err := json.Unmarshal(wire, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Mid != "4f8a2e6b0c9d1735-0-99" {
			t.Fatalf("mid = %q, want 4f8a2e6b0c9d1735-0-99", msg.Mid)
		}

		out, err := json.Marshal(&msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var round map[string]any
		if err := json.Unmarshal(out, &round); err != nil {
			t.Fatalf("unmarshal round-trip: %v", err)
		}
		if round["mid"] != "4f8a2e6b0c9d1735-0-99" {
			t.Errorf("re-serialized mid = %v, want 4f8a2e6b0c9d1735-0-99", round["mid"])
		}
	})

	t.Run("mid absent is omitted", func(t *testing.T) {
		t.Parallel()

		msg := ServerMessage{Type: "message", Channel: "acme.trades", Data: json.RawMessage(`{"px":1}`)}
		out, err := json.Marshal(&msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var round map[string]any
		if err := json.Unmarshal(out, &round); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := round["mid"]; ok {
			t.Errorf("empty mid must be omitted from JSON output, got %s", out)
		}
	})
}

func TestWSClient_Publish(t *testing.T) {
	t.Parallel()

	receivedCh := make(chan map[string]any, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := ws.HTTPUpgrader{}
		conn, _, _, err := upgrader.Upgrade(r, w)
		if err != nil {
			return
		}
		defer conn.Close()

		data, err := wsutil.ReadClientText(conn)
		if err != nil {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(data, &msg)
		receivedCh <- msg
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:]
	client, err := Dial(context.Background(), wsURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := client.Publish("ch1", json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case received := <-receivedCh:
		if received["type"] != "publish" {
			t.Errorf("type = %v, want publish", received["type"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server to receive message")
	}
}

func TestWSClient_SendAuth(t *testing.T) {
	t.Parallel()

	receivedCh := make(chan map[string]any, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := ws.HTTPUpgrader{}
		conn, _, _, err := upgrader.Upgrade(r, w)
		if err != nil {
			return
		}
		defer conn.Close()

		data, err := wsutil.ReadClientText(conn)
		if err != nil {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(data, &msg)
		receivedCh <- msg
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:]
	client, err := Dial(context.Background(), wsURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := client.SendAuth("new-token"); err != nil {
		t.Fatalf("send auth: %v", err)
	}

	select {
	case received := <-receivedCh:
		if received["type"] != "auth" {
			t.Errorf("type = %v, want auth", received["type"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server to receive message")
	}
}

func TestWSClient_Unsubscribe(t *testing.T) {
	t.Parallel()

	receivedCh := make(chan map[string]any, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := ws.HTTPUpgrader{}
		conn, _, _, err := upgrader.Upgrade(r, w)
		if err != nil {
			return
		}
		defer conn.Close()

		data, err := wsutil.ReadClientText(conn)
		if err != nil {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(data, &msg)
		receivedCh <- msg
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:]
	client, err := Dial(context.Background(), wsURL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := client.Unsubscribe([]string{"ch1", "ch2"}); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	select {
	case received := <-receivedCh:
		if received["type"] != "unsubscribe" {
			t.Errorf("type = %v, want unsubscribe", received["type"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server to receive message")
	}
}

func TestWithToken(t *testing.T) {
	t.Parallel()

	headerCh := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerCh <- r.Header.Get("Authorization")
		upgrader := ws.HTTPUpgrader{}
		conn, _, _, err := upgrader.Upgrade(r, w)
		if err != nil {
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:]
	client, err := Dial(context.Background(), wsURL, WithToken("my-jwt"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	select {
	case h := <-headerCh:
		if h != "Bearer my-jwt" {
			t.Errorf("auth header = %q, want %q", h, "Bearer my-jwt")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestWithAPIKey(t *testing.T) {
	t.Parallel()

	headerCh := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerCh <- r.Header.Get("X-API-Key")
		upgrader := ws.HTTPUpgrader{}
		conn, _, _, err := upgrader.Upgrade(r, w)
		if err != nil {
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	wsURL := "ws" + srv.URL[4:]
	client, err := Dial(context.Background(), wsURL, WithAPIKey("key-123"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	select {
	case h := <-headerCh:
		if h != "key-123" {
			t.Errorf("api key header = %q, want %q", h, "key-123")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}
