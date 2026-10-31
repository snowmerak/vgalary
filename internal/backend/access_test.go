package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicOriginRequiresPassword(t *testing.T) {
	t.Setenv("VGALARY_WEB_PASSWORD", "")
	if _, err := newWebAccess(HTTPOptions{PublicOrigin: "https://vgalary.saturday.ne.kr"}); err == nil {
		t.Fatal("public deployment started without password")
	}
	for _, origin := range []string{"http://vgalary.saturday.ne.kr", "https://user:secret@vgalary.saturday.ne.kr", "https://vgalary.saturday.ne.kr/path"} {
		if _, err := newWebAccess(HTTPOptions{PublicOrigin: origin}); err == nil {
			t.Fatalf("accepted unsafe origin %s", origin)
		}
	}
}

func TestDomainSessionProtectsAllMediaAndSettings(t *testing.T) {
	t.Setenv("VGALARY_WEB_PASSWORD", "a-long-test-password-123")
	app, err := New(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	handler, err := app.HandlerWithOptions(HTTPOptions{PublicOrigin: "https://vgalary.saturday.ne.kr"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "https://vgalary.saturday.ne.kr"+path, strings.NewReader(body))
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	for _, path := range []string{"/api/settings", "/api/assets", "/api/assets/any/download", "/api/jobs", "/api/sources"} {
		if got := request("GET", path, "", nil, "").Code; got != 401 {
			t.Fatalf("unprotected %s: %d", path, got)
		}
	}
	if got := request("GET", "/api/health", "", nil, "").Code; got != 200 {
		t.Fatal("health probe requires login")
	}
	login := request("POST", "/api/login", `{"password":"a-long-test-password-123"}`, nil, "https://vgalary.saturday.ne.kr")
	if login.Code != 200 {
		t.Fatalf("login failed: %s", login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie flags are unsafe")
	}
	cookie := cookies[0]
	if got := request("GET", "/api/settings", "", cookie, "").Code; got != 200 {
		t.Fatalf("valid session failed: %d", got)
	}
	var body map[string]any
	session := request("GET", "/api/session", "", cookie, "")
	if err := json.Unmarshal(session.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["authenticated"] != true {
		t.Fatal("session not authenticated")
	}
	if got := request("POST", "/api/login", `{"password":"a-long-test-password-123"}`, nil, "https://evil.example").Code; got != 403 {
		t.Fatal("cross-origin login accepted")
	}
	tampered := *cookie
	tampered.Value += "bad"
	if got := request("GET", "/api/settings", "", &tampered, "").Code; got != 401 {
		t.Fatal("tampered session accepted")
	}
	logout := request("POST", "/api/logout", "", cookie, "https://vgalary.saturday.ne.kr")
	if logout.Code != 204 || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	hostile := httptest.NewRequest("GET", "https://attacker.example/api/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, hostile)
	if response.Code != 403 {
		t.Fatal("unconfigured Host accepted")
	}
}

func TestLoginRateLimitAndLocalCompatibility(t *testing.T) {
	t.Setenv("VGALARY_WEB_PASSWORD", "a-long-test-password-123")
	access, err := newWebAccess(HTTPOptions{PasswordEnv: "VGALARY_WEB_PASSWORD"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		response := httptest.NewRecorder()
		access.login(response, httptest.NewRequest("POST", "http://localhost/api/login", strings.NewReader(`{"password":"wrong"}`)))
		expected := 401
		if i == 5 {
			expected = 429
		}
		if response.Code != expected {
			t.Fatalf("login attempt %d: %d", i, response.Code)
		}
	}
	local, err := newWebAccess(HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !local.authenticated(httptest.NewRequest("GET", "http://localhost/api/settings", nil)) {
		t.Fatal("default local access changed")
	}
}
