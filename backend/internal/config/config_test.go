package config

import (
	"os"
	"testing"
)

func TestLoadTencentASRConfiguration(t *testing.T) {
	t.Setenv("TENCENT_ASR_APP_ID", "1250000000")
	t.Setenv("TENCENT_ASR_SECRET_ID", "test-only-id")
	t.Setenv("TENCENT_ASR_SECRET_KEY", "test-only-key")
	t.Setenv("TENCENT_ASR_ENGINE_MODEL", "")
	cfg := Load()
	if cfg.TencentASRAppID != "1250000000" || cfg.TencentASRSecretID != "test-only-id" || cfg.TencentASRSecretKey != "test-only-key" || cfg.TencentASREngineModel != "16k_zh" {
		t.Fatal("ASR credentials/default model were not loaded")
	}
	t.Setenv("TENCENT_ASR_ENGINE_MODEL", "16k_yue")
	if Load().TencentASREngineModel != "16k_yue" {
		t.Fatal("ASR model override was not loaded")
	}
}

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

func TestLoadWechatLoginSettings(t *testing.T) {
	t.Setenv("WECHAT_APP_ID", "wx-test-app")
	t.Setenv("WECHAT_APP_SECRET", "test-only-secret")
	for _, tc := range []struct {
		value   string
		seconds int
	}{{"", 10}, {"5", 5}, {"60", 60}, {"0", 10}, {"61", 10}, {"invalid", 10}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("WECHAT_TIMEOUT_SECONDS", tc.value)
			cfg := Load()
			if cfg.WechatAppID != "wx-test-app" || cfg.WechatAppSecret != "test-only-secret" || int(cfg.WechatTimeout.Seconds()) != tc.seconds {
				t.Fatal("wechat login settings were not loaded correctly")
			}
		})
	}
}

func TestLoadWechatReminderSettings(t *testing.T) {
	t.Setenv("WECHAT_REMINDER_TEMPLATE_ID", "template-test")
	t.Setenv("WECHAT_REMINDER_TITLE_KEY", "thing8")
	t.Setenv("WECHAT_REMINDER_TIME_KEY", "date4")
	t.Setenv("WECHAT_REMINDER_CONTENT_KEY", "thing7")
	t.Setenv("WECHAT_REMINDER_TYPE_KEY", "thing9")
	t.Setenv("WECHAT_REMINDER_SOURCE_KEY", "thing10")
	t.Setenv("WECHAT_MINIPROGRAM_STATE", "trial")
	t.Setenv("WECHAT_REMINDER_SUBSCRIPTION_TYPE", "permanent")
	cfg := Load()
	if cfg.WechatReminderTemplateID != "template-test" || cfg.WechatReminderTitleKey != "thing8" || cfg.WechatReminderTimeKey != "date4" || cfg.WechatReminderContentKey != "thing7" || cfg.WechatMiniprogramState != "trial" || cfg.WechatReminderSubscriptionType != "permanent" {
		t.Fatal("wechat reminder settings were not loaded correctly")
	}
	if cfg.WechatReminderTypeKey != "thing9" || cfg.WechatReminderSourceKey != "thing10" {
		t.Fatal("notification type and source keywords were not loaded")
	}
	for _, key := range []string{"WECHAT_REMINDER_TITLE_KEY", "WECHAT_REMINDER_TIME_KEY", "WECHAT_REMINDER_CONTENT_KEY", "WECHAT_REMINDER_TYPE_KEY", "WECHAT_REMINDER_SOURCE_KEY", "WECHAT_MINIPROGRAM_STATE", "WECHAT_REMINDER_SUBSCRIPTION_TYPE"} {
		t.Setenv(key, "")
	}
	cfg = Load()
	if cfg.WechatReminderTitleKey != "" || cfg.WechatReminderTimeKey != "" || cfg.WechatReminderContentKey != "" || cfg.WechatReminderTypeKey != "" || cfg.WechatReminderSourceKey != "" {
		t.Fatal("explicitly empty keywords must omit fields")
	}
	if cfg.WechatMiniprogramState != "formal" || cfg.WechatReminderSubscriptionType != "once" {
		t.Fatal("default delivery must use formal build and one-time subscription")
	}
	for _, key := range []string{"WECHAT_REMINDER_TITLE_KEY", "WECHAT_REMINDER_TIME_KEY", "WECHAT_REMINDER_CONTENT_KEY", "WECHAT_REMINDER_TYPE_KEY", "WECHAT_REMINDER_SOURCE_KEY"} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	cfg = Load()
	if cfg.WechatReminderTitleKey != "thing1" || cfg.WechatReminderTimeKey != "time2" || cfg.WechatReminderContentKey != "thing3" || cfg.WechatReminderTypeKey != "" || cfg.WechatReminderSourceKey != "" {
		t.Fatal("omitted keywords must use documented defaults")
	}
}
