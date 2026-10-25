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

func TestValidateURLSecurity(t *testing.T) {
	tests := []struct {
		name    string
		ctx     *Context
		wantErr bool
	}{
		{
			name: "local context with loopback URLs is fine",
			ctx:  &Context{Type: TypeLocal, ProvisioningURL: "http://localhost:18080", GatewayURL: "ws://localhost:13000"},
		},
		{
			// The gate is by URL, not Type: hand-editing Type to "local" must NOT excuse a
			// plaintext remote endpoint (the one-field bypass).
			name:    "local type with a non-loopback plaintext URL is still rejected",
			ctx:     &Context{Type: TypeLocal, ProvisioningURL: "http://devbox.internal:18080"},
			wantErr: true,
		},
		{
			name: "remote with https/wss is fine",
			ctx:  &Context{Type: TypeRemote, ProvisioningURL: "https://api.example.com", GatewayURL: "wss://ws.example.com"},
		},
		{
			name: "remote with loopback plaintext is fine (tunnel/port-forward)",
			ctx:  &Context{Type: TypeRemote, ProvisioningURL: "http://localhost:18080", GatewayURL: "ws://127.0.0.1:13000"},
		},
		{
			name:    "remote with plaintext provisioning is rejected",
			ctx:     &Context{Type: TypeRemote, ProvisioningURL: "http://api.example.com", GatewayURL: "wss://ws.example.com"},
			wantErr: true,
		},
		{
			name:    "remote with plaintext ws gateway is rejected",
			ctx:     &Context{Type: TypeRemote, ProvisioningURL: "https://api.example.com", GatewayURL: "ws://ws.example.com"},
			wantErr: true,
		},
		{
			name:    "remote with plaintext tester is rejected",
			ctx:     &Context{Type: TypeRemote, ProvisioningURL: "https://api.example.com", GatewayURL: "wss://ws.example.com", TesterURL: "http://tester.example.com"},
			wantErr: true,
		},
		{
			name:    "untyped (legacy) context is treated as non-local",
			ctx:     &Context{ProvisioningURL: "http://api.example.com"},
			wantErr: true,
		},
		{
			name: "empty optional URL is skipped",
			ctx:  &Context{Type: TypeRemote, ProvisioningURL: "https://api.example.com", GatewayURL: "wss://ws.example.com", TesterURL: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ctx.ValidateURLSecurity()
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURLSecurity() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRequireSecureURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https", url: "https://api.example.com", wantErr: false},
		{name: "wss", url: "wss://ws.example.com", wantErr: false},
		{name: "loopback http", url: "http://localhost:18080", wantErr: false},
		{name: "loopback ws", url: "ws://127.0.0.1:13000", wantErr: false},
		{name: "remote http", url: "http://api.example.com", wantErr: true},
		{name: "remote ws", url: "ws://ws.example.com", wantErr: true},
		{name: "uppercase scheme is normalized then rejected", url: "HTTP://api.example.com", wantErr: true},
		// Fail closed: a security check must not answer "secure" on garbage.
		{name: "unparseable fails closed", url: "http://127.0.0.1 /x", wantErr: true},
		{name: "no host fails closed", url: "not-a-url", wantErr: true},
		{name: "unknown scheme fails closed", url: "ftp://api.example.com", wantErr: true},
		// userinfo tricks: Hostname() is what matters, so @ cannot smuggle a loopback label.
		{name: "loopback userinfo on remote host is rejected", url: "http://localhost@evil.com/", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequireSecureURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("RequireSecureURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
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
