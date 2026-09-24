package transmission

import "testing"

// TestURLDerivesWebUI: the link-out must point at the web client, not the
// RPC endpoint — browsing to /rpc answers 409 (the CSRF handshake), which
// looks like a broken link. The web UI lives at the same host with /rpc
// swapped for /web/.
func TestURLDerivesWebUI(t *testing.T) {
	tests := []struct {
		name string
		rpc  string
		want string
	}{
		{"standard rpc path", "http://100.64.0.1:9091/transmission/rpc", "http://100.64.0.1:9091/transmission/web/"},
		{"bare rpc root", "http://host:9091/rpc", "http://host:9091/web/"},
		{"no rpc suffix", "http://host:9091/", "http://host:9091/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(tt.rpc).URL(); got != tt.want {
				t.Errorf("URL() = %q, want %q", got, tt.want)
			}
		})
	}
}
