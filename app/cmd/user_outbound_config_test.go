package cmd

import (
	"github.com/apernet/hysteria/core/v2/server"
	"github.com/spf13/viper"
	"os"
	"path/filepath"
	"testing"
)

func TestUserOutboundConfigPreservesAuthIDCase(t *testing.T) {
	v := viper.New()
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte("outbounds:\n  - name: DIRECT\n    type: direct\nuserOutbounds:\n  TESTsss: DIRECT\n  testsss: DIRECT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	var config serverConfig
	if err := decodeServerConfig(v, &config); err != nil {
		t.Fatal(err)
	}
	if config.UserOutbounds["TESTsss"] != "DIRECT" {
		t.Fatalf("auth ID was altered while reading YAML: %#v", config.UserOutbounds)
	}
	if len(config.UserOutbounds) != 2 {
		t.Fatal("distinct identities merged")
	}
	var hy server.Config
	if err := config.fillOutboundConfig(&hy); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"TESTsss", "testsss"} {
		name, outbound, ok := hy.OutboundSelector.Select(id)
		if !ok || name != "DIRECT" || outbound == nil {
			t.Fatalf("DIRECT lookup failed for %q", id)
		}
	}
	if _, _, ok := hy.OutboundSelector.Select("TESTSSS"); ok {
		t.Fatal("unbound identity must remain rejected")
	}
}

func TestUserOutboundJSONAndInvalidFieldCase(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		invalid    bool
	}{
		{"json", `{"userOutbounds":{"TESTsss":"DIRECT"}}`, false},
		{"invalid-field", `{"UserOutbounds":{"TESTsss":"DIRECT"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.json")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			v := viper.New()
			v.SetConfigFile(path)
			if err := v.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			var config serverConfig
			err := decodeServerConfig(v, &config)
			if tc.invalid {
				if err == nil {
					t.Fatal("identity map must not be silently discarded")
				}
				return
			}
			if err != nil || config.UserOutbounds["TESTsss"] != "DIRECT" {
				t.Fatalf("JSON identity not preserved: %v %#v", err, config.UserOutbounds)
			}
		})
	}
}
