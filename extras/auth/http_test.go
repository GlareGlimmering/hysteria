package auth

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHTTPAuthenticator(t *testing.T) {
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req httpAuthRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := httpAuthResponse{}
		if req.Addr == "123.123.123.123:5566" && req.Auth == "wahaha" && req.Tx == 12345 {
			resp.OK, resp.ID = true, "some_unique_id"
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer callback.Close()
	auth := NewHTTPAuthenticator(callback.URL, false)

	ok, id := auth.Authenticate(&net.UDPAddr{
		IP:   net.ParseIP("1.2.3.4"),
		Port: 34567,
	}, "idk", 123)
	assert.False(t, ok)
	assert.Equal(t, "", id)

	ok, id = auth.Authenticate(&net.UDPAddr{
		IP:   net.ParseIP("123.123.123.123"),
		Port: 5566,
	}, "wahaha", 12345)
	assert.True(t, ok)
	assert.Equal(t, "some_unique_id", id)
	auth.RequireDeviceLimit = true
	ok, _ = auth.Authenticate(&net.UDPAddr{IP: net.ParseIP("123.123.123.123"), Port: 5566}, "wahaha", 12345)
	assert.False(t, ok, "managed mode must reject callbacks without protocol 2 fields")
}
