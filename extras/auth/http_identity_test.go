package auth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPAuthenticatorPreservesMixedCaseIdentity(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
		id   string
	}{
		{`{"ok":true,"id":"TESTsss"}`, true, "TESTsss"},
		{`{"ok":false,"id":""}`, false, ""},
		{`not-json`, false, ""},
	} {
		t.Run(tc.body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			a := NewHTTPAuthenticator(s.URL, false)
			ok, id := a.Authenticate(&net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 1234}, "test-only", 0)
			if ok != tc.ok || id != tc.id {
				t.Fatalf("got %v %q", ok, id)
			}
		})
	}
}
