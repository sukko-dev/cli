package context

import "testing"

func TestIsLocal(t *testing.T) {
	tests := []struct {
		name string
		ctx  *Context
		want bool
	}{
		{name: "nil context", ctx: nil, want: false},
		{name: "type local", ctx: &Context{Type: TypeLocal}, want: true},
		{name: "type remote", ctx: &Context{Type: TypeRemote}, want: false},
		{name: "empty type is non-local", ctx: &Context{Type: ""}, want: false},
		{name: "unknown type is non-local", ctx: &Context{Type: "staging"}, want: false},
		// Type, not the URLs, decides: a "remote" context with loopback URLs is still remote.
		{name: "remote with loopback urls stays remote", ctx: &Context{Type: TypeRemote, ProvisioningURL: "http://localhost:8080"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ctx.IsLocal(); got != tt.want {
				t.Errorf("IsLocal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "localhost http", url: "http://localhost:8080", want: true},
		{name: "localhost ws", url: "ws://localhost:3000", want: true},
		{name: "uppercase LOCALHOST", url: "http://LOCALHOST:8080", want: true},
		{name: "dot-localhost subdomain", url: "http://api.localhost:8080", want: true},
		{name: "127.0.0.1", url: "http://127.0.0.1:8080", want: true},
		{name: "127.0.0.5 (whole /8 block)", url: "http://127.0.0.5", want: true},
		{name: "ipv6 loopback", url: "http://[::1]:8080", want: true},
		{name: "public host", url: "https://api.example.com", want: false},
		{name: "public ws host", url: "wss://ws.example.com:3000", want: false},
		{name: "private but non-loopback ip", url: "http://10.0.0.1", want: false},
		{name: "empty string", url: "", want: false},
		{name: "no host", url: "not-a-url", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLoopback(tt.url); got != tt.want {
				t.Errorf("IsLoopback(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
