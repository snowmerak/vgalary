package backend

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HTTPOptions controls deployment access, separate from model connection settings.
type HTTPOptions struct {
	PublicOrigin string
	PasswordEnv  string
}

type webAccess struct {
	origin       string
	host         string
	passwordHash [32]byte
	required     bool
	secure       bool
	mu           sync.Mutex
	failed       int
	reset        time.Time
}

func newWebAccess(options HTTPOptions) (*webAccess, error) {
	access := &webAccess{}
	if options.PublicOrigin != "" {
		parsed, err := url.Parse(options.PublicOrigin)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return nil, errors.New("public-origin must be an HTTPS origin without a path or credentials")
		}
		access.origin = parsed.Scheme + "://" + parsed.Host
		access.host = parsed.Host
		access.secure = true
		if options.PasswordEnv == "" {
			options.PasswordEnv = "VGALARY_WEB_PASSWORD"
		}
	}
	if options.PasswordEnv != "" {
		password, err := envValue(options.PasswordEnv, "")
		if err != nil {
			return nil, err
		}
		if len(password) < 16 {
			return nil, errors.New("web password must contain at least 16 bytes")
		}
		access.passwordHash = sha256.Sum256([]byte(password))
		access.required = true
	}
	return access, nil
}

const sessionCookie = "vgalary_session"
const sessionLifetime = 12 * time.Hour

func (a *webAccess) sign(message string) string {
	mac := hmac.New(sha256.New, a.passwordHash[:])
	_, _ = mac.Write([]byte(a.origin + "|" + message))
	return hex.EncodeToString(mac.Sum(nil))
}
func (a *webAccess) authenticated(r *http.Request) bool {
	if !a.required {
		return true
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expiry <= time.Now().Unix() || expiry > time.Now().Add(sessionLifetime+time.Minute).Unix() {
		return false
	}
	expected := a.sign(parts[0] + "." + parts[1])
	return hmac.Equal([]byte(parts[2]), []byte(expected))
}
func (a *webAccess) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"authenticated": a.authenticated(r), "auth_required": a.required})
}
func (a *webAccess) login(w http.ResponseWriter, r *http.Request) {
	if !a.required {
		writeJSON(w, 200, map[string]bool{"authenticated": true})
		return
	}
	a.mu.Lock()
	if time.Now().After(a.reset) {
		a.failed = 0
		a.reset = time.Now().Add(time.Minute)
	}
	limited := a.failed >= 5
	if !limited {
		a.failed++
	}
	a.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "60")
		apiError(w, 429, errors.New("too many login attempts; try again in a minute"))
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		apiError(w, 400, err)
		return
	}
	got := sha256.Sum256([]byte(body.Password))
	if subtle.ConstantTimeCompare(got[:], a.passwordHash[:]) != 1 {
		apiError(w, 401, errors.New("password does not match"))
		return
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		apiError(w, 500, errors.New("could not create session"))
		return
	}
	expiry := time.Now().Add(sessionLifetime)
	message := strconv.FormatInt(expiry.Unix(), 10) + "." + hex.EncodeToString(nonce[:])
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: message + "." + a.sign(message), Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, Expires: expiry, MaxAge: int(sessionLifetime.Seconds())})
	a.mu.Lock()
	a.failed = 0
	a.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"authenticated": true})
}
func (a *webAccess) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(204)
}

func (a *webAccess) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" && r.URL.Path != "/api/session" && r.URL.Path != "/api/login" && r.URL.Path != "/api/logout" && !a.authenticated(r) {
			apiError(w, 401, errors.New("login required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
