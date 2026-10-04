package config

import "testing"

func TestLoadMySQLTLSSettings(t *testing.T) {
	t.Setenv("MYSQL_HOST", "38.76.183.142")
	t.Setenv("MYSQL_TLS_CA_FILE", "artifacts/mysql/ca.pem")
	t.Setenv("MYSQL_TLS_SERVER_NAME", "")
	cfg := Load()
	if cfg.DB.TLSCAFile != "artifacts/mysql/ca.pem" || cfg.DB.TLSServerName != "38.76.183.142" {
		t.Fatalf("unexpected TLS settings: CA=%q, server=%q", cfg.DB.TLSCAFile, cfg.DB.TLSServerName)
	}
	t.Setenv("MYSQL_TLS_SERVER_NAME", "mysql.example.test")
	if got := Load().DB.TLSServerName; got != "mysql.example.test" {
		t.Fatalf("TLS server name override = %q", got)
	}
}

func TestLoadBackgroundWorkersEnabled(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"", true}, {"true", true}, {"false", false}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("BACKGROUND_WORKERS_ENABLED", tc.value)
			if got := Load().BackgroundWorkersEnabled; got != tc.want {
				t.Fatalf("BackgroundWorkersEnabled = %t, want %t", got, tc.want)
			}
		})
	}
}
