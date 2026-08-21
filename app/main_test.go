package main

import (
	"net"
	"strings"
	"testing"
)

// TestEphemeralPortHandshake exercises the actual ADR 0015 / F3-design.md
// §1,§8 T2 mechanism end to end at the net.Listen level: binding
// "127.0.0.1:0" must yield a real, nonzero OS-assigned port via
// ln.Addr(), the handshake line built from that address must be
// well-formed and carry that same port, and wsOriginPatterns derived from
// it must reflect the *resolved* port (never the literal "0" that was
// requested) — exactly the property the webview/Tauri shell depends on to
// find and be authorized against the right port.
func TestEphemeralPortHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on 127.0.0.1:0: %v", err)
	}
	defer ln.Close()

	actualAddr := ln.Addr().String()
	host, port, err := net.SplitHostPort(actualAddr)
	if err != nil {
		t.Fatalf("split resolved addr %q: %v", actualAddr, err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("resolved host = %q, want 127.0.0.1", host)
	}
	if port == "" || port == "0" {
		t.Fatalf("resolved port = %q, want a real nonzero port", port)
	}

	line := handshakeLine(actualAddr)
	want := "DEXEL_LISTENING http://127.0.0.1:" + port
	if line != want {
		t.Fatalf("handshakeLine(%q) = %q, want %q", actualAddr, line, want)
	}

	patterns := wsOriginPatterns(actualAddr)
	wantPatterns := []string{"127.0.0.1:" + port, "localhost:" + port}
	if len(patterns) != len(wantPatterns) || patterns[0] != wantPatterns[0] || patterns[1] != wantPatterns[1] {
		t.Fatalf("wsOriginPatterns(%q) = %v, want %v", actualAddr, patterns, wantPatterns)
	}
}

// TestHandshakeLineFixedPort covers the non-ephemeral default (-addr
// 127.0.0.1:8080, unchanged from before this change): the handshake line
// must show the real requested port, not just work for :0.
func TestHandshakeLineFixedPort(t *testing.T) {
	got := handshakeLine("127.0.0.1:8080")
	want := "DEXEL_LISTENING http://127.0.0.1:8080"
	if got != want {
		t.Fatalf("handshakeLine(\"127.0.0.1:8080\") = %q, want %q", got, want)
	}
}

// TestWsExtraOriginPatterns covers -allow-origin's two accepted input
// shapes (bare host[:port], and a full origin URL from which the host is
// extracted — see ADR 0015/F3-design.md §2's two real Tauri v2 origins),
// comma-separated accumulation via originList, and that a wildcard or
// unparseable value is rejected rather than silently degrading the origin
// check into another -insecure-origin.
func TestWsExtraOriginPatterns(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{
			name: "bare host",
			in:   []string{"tauri.localhost"},
			want: []string{"tauri.localhost"},
		},
		{
			name: "full origin URL, scheme host only (macOS/Linux Tauri origin)",
			in:   []string{"tauri://localhost"},
			want: []string{"localhost"},
		},
		{
			name: "full origin URL with host (Windows Tauri origin)",
			in:   []string{"http://tauri.localhost"},
			want: []string{"tauri.localhost"},
		},
		{
			name: "multiple values",
			in:   []string{"tauri.localhost", "tauri://localhost"},
			want: []string{"tauri.localhost", "localhost"},
		},
		{
			name:    "wildcard is never allowed",
			in:      []string{"*"},
			wantErr: true,
		},
		{
			name:    "wildcard host inside a URL is never allowed",
			in:      []string{"http://*"},
			wantErr: true,
		},
		{
			name:    "hostless URL is rejected",
			in:      []string{"file://"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := wsExtraOriginPatterns(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("wsExtraOriginPatterns(%v) = %v, nil; want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("wsExtraOriginPatterns(%v) unexpected error: %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("wsExtraOriginPatterns(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("wsExtraOriginPatterns(%v) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

// TestOriginListSetAccumulatesAndSplitsCommas covers the -allow-origin
// flag.Value itself: repeated flag occurrences must accumulate (unlike
// flag.String, which keeps only the last one), and a single occurrence
// may list several comma-separated origins at once.
func TestOriginListSetAccumulatesAndSplitsCommas(t *testing.T) {
	var o originList
	if err := o.Set("tauri.localhost, tauri://localhost"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := o.Set("http://tauri.localhost"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	want := []string{"tauri.localhost", "tauri://localhost", "http://tauri.localhost"}
	if len(o) != len(want) {
		t.Fatalf("originList = %v, want %v", []string(o), want)
	}
	for i := range o {
		if o[i] != want[i] {
			t.Fatalf("originList = %v, want %v", []string(o), want)
		}
	}
	if !strings.Contains(o.String(), "tauri.localhost") {
		t.Fatalf("String() = %q, want it to contain the set values", o.String())
	}
}
