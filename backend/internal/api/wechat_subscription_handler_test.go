package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
)

type subscriptionStoreMock struct {
	identity                                        *dbop.WechatIdentity
	subscription                                    *dbop.WechatSubscription
	recordErr                                       error
	userID                                          int64
	recorded                                        bool
	result, requestID, templateID, subscriptionType string
}

func (m *subscriptionStoreMock) GetWechatIdentityByUser(_ context.Context, userID int64, appID string) (*dbop.WechatIdentity, error) {
	m.userID = userID
	if appID != "wx-test" {
		return nil, errors.New("wrong app")
	}
	return m.identity, nil
}
func (m *subscriptionStoreMock) GetWechatSubscription(_ context.Context, userID int64, appID, templateID string) (*dbop.WechatSubscription, error) {
	m.userID = userID
	return m.subscription, nil
}
func (m *subscriptionStoreMock) RecordWechatSubscription(_ context.Context, userID int64, appID, templateID, result, requestID, subscriptionType string) error {
	m.recorded = true
	m.userID = userID
	m.templateID = templateID
	m.result = result
	m.requestID = requestID
	m.subscriptionType = subscriptionType
	return m.recordErr
}
func subscriptionRequest(method, body string) *http.Request {
	return httptest.NewRequest(method, "/api/account/wechat-subscription", strings.NewReader(body)).WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: 8}))
}
func TestWechatSubscriptionHTTPUsesAuthenticatedIdentity(t *testing.T) {
	s := &Server{Cfg: &config.Config{WechatAppID: "wx-test"}, WechatMessages: &notificationSenderMock{}}
	store := &subscriptionStoreMock{identity: &dbop.WechatIdentity{UserID: 8}, subscription: &dbop.WechatSubscription{Remaining: 2}}
	response := httptest.NewRecorder()
	s.handleWechatSubscriptionWithStore(response, subscriptionRequest(http.MethodPost, `{"templateId":"template","result":"accept","requestId":"one-prompt","openid":"attacker-openid","userId":9}`), store)
	// Caller-supplied identities cannot change the authenticated account.
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.recorded && store.userID != 8 {
		t.Fatalf("used client account: %d", store.userID)
	}
	response = httptest.NewRecorder()
	s.handleWechatSubscriptionWithStore(response, subscriptionRequest(http.MethodPost, `{"templateId":"template","result":"accept","requestId":"one-prompt"}`), store)
	if response.Code != 200 || store.userID != 8 || !store.recorded || store.subscriptionType != "once" || store.requestID != "one-prompt" {
		t.Fatalf("store=%+v status=%d body=%s", store, response.Code, response.Body)
	}
	var status WechatSubscriptionStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || !status.HasWechatIdentity || status.Remaining != 2 || status.TemplateID != "template" {
		t.Fatalf("status=%+v", status)
	}
	if strings.Contains(response.Body.String(), "openid") {
		t.Fatal("identity leaked")
	}
}
func TestWechatSubscriptionHTTPRejectsWrongTemplateAndResult(t *testing.T) {
	s := &Server{Cfg: &config.Config{WechatAppID: "wx-test"}, WechatMessages: &notificationSenderMock{}}
	for _, body := range []string{`{"templateId":"other","result":"accept","requestId":"prompt"}`, `{"templateId":"template","result":"accepted","requestId":"prompt"}`, `{"templateId":"template","result":"accept","requestId":""}`} {
		store := &subscriptionStoreMock{}
		response := httptest.NewRecorder()
		s.handleWechatSubscriptionWithStore(response, subscriptionRequest(http.MethodPost, body), store)
		if response.Code != 400 || store.recorded {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
	}
}
func TestWechatSubscriptionHTTPConfigurationAndIdentityStates(t *testing.T) {
	t.Run("not_configured", func(t *testing.T) {
		s := &Server{Cfg: &config.Config{WechatAppID: "wx-test"}}
		store := &subscriptionStoreMock{identity: &dbop.WechatIdentity{UserID: 8}}
		response := httptest.NewRecorder()
		s.handleWechatSubscriptionWithStore(response, subscriptionRequest(http.MethodGet, ""), store)
		var status WechatSubscriptionStatus
		_ = json.Unmarshal(response.Body.Bytes(), &status)
		if response.Code != 200 || status.Enabled || !status.HasWechatIdentity {
			t.Fatalf("status=%d payload=%+v", response.Code, status)
		}
		response = httptest.NewRecorder()
		s.handleWechatSubscriptionWithStore(response, subscriptionRequest(http.MethodPost, `{"templateId":"template","result":"accept","requestId":"prompt"}`), store)
		if response.Code != 503 || store.recorded {
			t.Fatalf("status=%d recorded=%v", response.Code, store.recorded)
		}
	})
	t.Run("no_wechat_identity", func(t *testing.T) {
		s := &Server{Cfg: &config.Config{WechatAppID: "wx-test"}, WechatMessages: &notificationSenderMock{}}
		store := &subscriptionStoreMock{recordErr: dbop.ErrWechatIdentityMissing}
		response := httptest.NewRecorder()
		s.handleWechatSubscriptionWithStore(response, subscriptionRequest(http.MethodPost, `{"templateId":"template","result":"accept","requestId":"prompt"}`), store)
		if response.Code != 409 {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
	})
}
func TestWechatSubscriptionRouteRequiresJWT(t *testing.T) {
	s := &Server{Cfg: &config.Config{}, Auth: auth.NewService("secret", time.Hour)}
	response := httptest.NewRecorder()
	NewRouter(s).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/account/wechat-subscription", strings.NewReader(`{}`)))
	if response.Code != 401 {
		t.Fatalf("status=%d", response.Code)
	}
}
