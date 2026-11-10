package auth

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/apernet/hysteria/core/v2/server"
)

const (
	httpAuthTimeout = 10 * time.Second
)

var _ server.Authenticator = &HTTPAuthenticator{}

var errInvalidStatusCode = errors.New("invalid status code")

type HTTPAuthenticator struct {
	Client             *http.Client
	URL                string
	RequireDeviceLimit bool
}

func NewHTTPAuthenticator(url string, insecure bool) *HTTPAuthenticator {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: insecure,
	}
	return &HTTPAuthenticator{
		Client: &http.Client{
			Transport: tr,
			Timeout:   httpAuthTimeout,
		},
		URL: url,
	}
}

type httpAuthRequest struct {
	Addr string `json:"addr"`
	Auth string `json:"auth"`
	Tx   uint64 `json:"tx"`
}

type httpAuthResponse struct {
	OK          bool                    `json:"ok"`
	ID          string                  `json:"id"`
	DeviceLimit *int                    `json:"deviceLimit"`
	AccountID   int64                   `json:"accountId"`
	Bandwidth   *server.BandwidthPolicy `json:"bandwidth,omitempty"`
}

func (a *HTTPAuthenticator) post(req *httpAuthRequest) (*httpAuthResponse, error) {
	bs, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := a.Client.Post(a.URL, "application/json", bytes.NewReader(bs))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errInvalidStatusCode
	}
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var authResp httpAuthResponse
	err = json.Unmarshal(respData, &authResp)
	if err != nil {
		return nil, err
	}
	return &authResp, nil
}

func (a *HTTPAuthenticator) Authenticate(addr net.Addr, auth string, tx uint64) (ok bool, id string) {
	ok, id, _, _ = a.AuthenticateWithLimit(addr, auth, tx)
	return
}

func (a *HTTPAuthenticator) AuthenticateWithLimit(addr net.Addr, auth string, tx uint64) (ok bool, id string, limit int, accountID int64) {
	ok, id, limit, accountID, _ = a.AuthenticateWithBandwidth(addr, auth, tx)
	return
}

func (a *HTTPAuthenticator) AuthenticateWithBandwidth(addr net.Addr, auth string, tx uint64) (ok bool, id string, limit int, accountID int64, policy *server.BandwidthPolicy) {
	req := &httpAuthRequest{
		Addr: addr.String(),
		Auth: auth,
		Tx:   tx,
	}
	resp, err := a.post(req)
	if err != nil {
		return false, "", -1, 0, nil
	}
	limit = -1
	if a.RequireDeviceLimit && (resp.DeviceLimit == nil || resp.AccountID <= 0) {
		return false, "", -1, 0, nil
	}
	if resp.DeviceLimit != nil {
		if *resp.DeviceLimit < 1 {
			return false, "", -1, 0, nil
		}
		limit = *resp.DeviceLimit
	}
	if resp.Bandwidth != nil && (!resp.Bandwidth.Valid() || resp.AccountID <= 0) {
		return false, "", -1, 0, nil
	}
	return resp.OK, resp.ID, limit, resp.AccountID, resp.Bandwidth
}
