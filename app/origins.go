// origins.go — WebSocket Origin configuration: the loopback host patterns
// derived from -addr (wsOriginPatterns), the stdout handshake line, the
// repeatable -allow-origin flag.Value (originList), and the extra literal
// origins it contributes (wsExtraOriginPatterns).
package main

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
)

// wsOriginPatterns derives the WebSocket Origin patterns handleWS should
// accept from -addr's port (B1: the server used to accept literally any
// Origin — InsecureSkipVerify: true — which let any web page a user had
// open in another tab pop a cross-origin WS to this loopback server and
// read activity state or mutate the save). A same-origin browser tab
// always sends an Origin that is either empty (accepted unconditionally
// by nhooyr.io/websocket — see handlers.go) or exactly matches r.Host
// (also accepted unconditionally); these two patterns exist ONLY to cover
// the other loopback hostname the frontend might be addressed by when
// that differs from -addr's own host (127.0.0.1 vs. localhost) — never a
// non-loopback host, regardless of what -addr itself was bound to,
// because this process is meant to be reached over loopback only.
//
// -addr must have a port (flag.String's default does; a malformed
// override is a startup misconfiguration worth failing loudly on rather
// than silently guessing a port that would authorize the wrong Origin).
func wsOriginPatterns(addr string) []string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		log.Fatalf("bad -addr %q: %v (want host:port, e.g. 127.0.0.1:8080)", addr, err)
	}
	return []string{"127.0.0.1:" + port, "localhost:" + port}
}

// handshakeLine formats the one stable, machine-readable line this
// process prints to stdout once its listener is bound (ADR 0015 /
// docs/plan/F3-design.md §1,§8 T2): "DEXEL_LISTENING http://<addr>". A
// future parent process (the Tauri shell's T1) reads this exact prefix
// off the sidecar's stdout to learn which port an ephemeral
// `-addr 127.0.0.1:0` bind actually landed on, before it can open a
// window pointed at this server — so the shape must stay stable; this is
// factored out purely so the shape itself is unit-testable without
// spinning up main()'s whole server.
func handshakeLine(addr string) string {
	return "DEXEL_LISTENING http://" + addr
}

// originList is a repeatable flag.Value for -allow-origin: each
// occurrence (and each comma-separated part within one occurrence)
// appends to the slice, unlike flag.String which would keep only the
// last occurrence and silently drop earlier ones.
type originList []string

func (o *originList) String() string { return strings.Join(*o, ",") }

func (o *originList) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			*o = append(*o, part)
		}
	}
	return nil
}

// wsExtraOriginPatterns converts each -allow-origin flag value into a
// host pattern to append to wsOriginPatterns's result. nhooyr.io/
// websocket's OriginPatterns is matched against the parsed Origin
// header's *host* only (see its accept.go authenticateOrigin), not the
// full origin string, so a bare "tauri://localhost" would never match
// anything if appended verbatim — hence each value here may be given
// either as a bare host[:port] (e.g. "tauri.localhost") or as a full
// origin URL (e.g. "tauri://localhost", "http://tauri.localhost" — the
// two real per-platform Tauri v2 origins docs/plan/F3-design.md §2
// names), from which the host is extracted.
//
// This is deliberately tight, additive insurance (§2: "specific literal
// origins... never *"), not a second -insecure-origin: a wildcard or an
// unparseable/hostless value is a startup misconfiguration worth failing
// loudly on, exactly like wsOriginPatterns's own -addr validation, rather
// than silently degrading the origin check.
func wsExtraOriginPatterns(origins []string) ([]string, error) {
	patterns := make([]string, 0, len(origins))
	for _, o := range origins {
		host := o
		if strings.Contains(o, "://") {
			u, err := url.Parse(o)
			if err != nil {
				return nil, fmt.Errorf("bad -allow-origin %q: %w", o, err)
			}
			host = u.Host
		}
		if host == "" {
			return nil, fmt.Errorf("bad -allow-origin %q: empty host", o)
		}
		if strings.Contains(host, "*") {
			return nil, fmt.Errorf("bad -allow-origin %q: wildcard origins are never allowed", o)
		}
		patterns = append(patterns, host)
	}
	return patterns, nil
}
