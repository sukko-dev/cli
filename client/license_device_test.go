package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// deviceServer scripts the license device endpoints: authorize returns a fixed
// pending request; token returns the queued responses in order (one per poll).
type deviceServer struct {
	srv          *httptest.Server
	lastClientID string
	tokenReplies []func(w http.ResponseWriter)
	tokenCalls   int
}

func newDeviceServer(t *testing.T, tokenReplies ...func(w http.ResponseWriter)) *deviceServer {
	t.Helper()
	ds := &deviceServer{tokenReplies: tokenReplies}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/device/authorize", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ClientID string `json:"client_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ds.lastClientID = body.ClientID
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "dev-code-xyz",
			"user_code":                 "BCDF-GHJK",
			"verification_uri":          "https://sukko.dev/device",
			"verification_uri_complete": "https://sukko.dev/device?code=BCDF-GHJK",
			"expires_in":                600,
			"interval":                  5,
		})
	})
	mux.HandleFunc("POST /api/v1/device/token", func(w http.ResponseWriter, _ *http.Request) {
		i := ds.tokenCalls
		ds.tokenCalls++
		if i < len(ds.tokenReplies) {
			ds.tokenReplies[i](w)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	ds.srv = httptest.NewServer(mux)
	t.Cleanup(ds.srv.Close)
	return ds
}

func errReply(status int, code string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": "x"})
	}
}

func okReply(key, edition string, exp int64) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"license_key": key, "edition": edition, "exp": exp})
	}
}

func TestLicenseDeviceClient_Authorize(t *testing.T) {
	t.Parallel()
	ds := newDeviceServer(t)
	c := NewLicenseDeviceClient(ds.srv.URL)

	auth, err := c.Authorize(context.Background())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if auth.DeviceCode != "dev-code-xyz" || auth.UserCode != "BCDF-GHJK" {
		t.Errorf("codes = %+v", auth)
	}
	if auth.VerificationURI != "https://sukko.dev/device" || auth.VerificationURIComplete != "https://sukko.dev/device?code=BCDF-GHJK" {
		t.Errorf("uris = %+v", auth)
	}
	if auth.ExpiresIn != 600 || auth.Interval != 5 {
		t.Errorf("expires_in/interval = %d/%d", auth.ExpiresIn, auth.Interval)
	}
	if ds.lastClientID != deviceClientID {
		t.Errorf("client_id sent = %q, want %q", ds.lastClientID, deviceClientID)
	}
}

func TestLicenseDeviceClient_PollToken_States(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		reply func(http.ResponseWriter)
		want  error
	}{
		{"pending", errReply(http.StatusBadRequest, "AUTHORIZATION_PENDING"), ErrDeviceAuthorizationPending},
		{"slow_down", errReply(http.StatusBadRequest, "SLOW_DOWN"), ErrDeviceSlowDown},
		{"expired", errReply(http.StatusBadRequest, "EXPIRED_TOKEN"), ErrDeviceExpired},
		{"denied", errReply(http.StatusBadRequest, "ACCESS_DENIED"), ErrDeviceAccessDenied},
		{"invalid grant", errReply(http.StatusBadRequest, "INVALID_GRANT"), ErrDeviceInvalidGrant},
		{"invalid client", errReply(http.StatusBadRequest, "INVALID_CLIENT"), ErrDeviceInvalidClient},
		{"rate limited (429)", errReply(http.StatusTooManyRequests, ""), ErrDeviceRateLimited},
		{"server error (5xx)", errReply(http.StatusServiceUnavailable, ""), ErrDeviceTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ds := newDeviceServer(t, tc.reply)
			c := NewLicenseDeviceClient(ds.srv.URL)
			_, err := c.PollToken(context.Background(), "dev-code-xyz")
			if !errors.Is(err, tc.want) {
				t.Fatalf("PollToken err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLicenseDeviceClient_Authorize_NonCreatedIsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	if _, err := NewLicenseDeviceClient(srv.URL).Authorize(context.Background()); err == nil {
		t.Fatal("expected an error on a non-201 authorize response")
	}
}

func TestLicenseDeviceClient_Authorize_IncompleteResponseIsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		// Missing device_code + verification_uri — a drifted/garbled upstream.
		_ = json.NewEncoder(w).Encode(map[string]any{"user_code": "BCDF-GHJK"})
	}))
	t.Cleanup(srv.Close)
	if _, err := NewLicenseDeviceClient(srv.URL).Authorize(context.Background()); err == nil {
		t.Fatal("expected an error on an incomplete authorize response")
	}
}

func TestLicenseDeviceClient_PollToken_EmptyKeyIsError(t *testing.T) {
	t.Parallel()
	ds := newDeviceServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"license_key": "", "edition": "community"})
	})
	if _, err := NewLicenseDeviceClient(ds.srv.URL).PollToken(context.Background(), "dev-code"); err == nil {
		t.Fatal("expected an error when the license service returns an empty key")
	}
}

func TestLicenseDeviceClient_PollToken_TransportErrorIsTransient(t *testing.T) {
	t.Parallel()
	// A closed server → transport error → retryable transient (not a terminal).
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	if _, err := NewLicenseDeviceClient(url).PollToken(context.Background(), "dev-code"); !errors.Is(err, ErrDeviceTransient) {
		t.Fatalf("err = %v, want ErrDeviceTransient", err)
	}
}

func TestLicenseDeviceClient_PollToken_Malformed200IsError(t *testing.T) {
	t.Parallel()
	ds := newDeviceServer(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	})
	if _, err := NewLicenseDeviceClient(ds.srv.URL).PollToken(context.Background(), "dev-code"); err == nil {
		t.Fatal("expected a decode error on a non-JSON 200 body")
	}
}

func TestLicenseDeviceClient_PollToken_Success(t *testing.T) {
	t.Parallel()
	ds := newDeviceServer(t, okReply("payload.sig", "community", 123))
	c := NewLicenseDeviceClient(ds.srv.URL)

	tok, err := c.PollToken(context.Background(), "dev-code-xyz")
	if err != nil {
		t.Fatalf("PollToken: %v", err)
	}
	if tok.LicenseKey != "payload.sig" || tok.Edition != "community" || tok.Exp != 123 {
		t.Fatalf("token = %+v", tok)
	}
}
