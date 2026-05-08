// Package router wires the gateway's HTTP surface: public auth handlers
// (which need cookie handling beyond what grpc-gateway gives us) plus
// the proto-generated REST mux for everything else.
package router

import (
	"encoding/json"
	"net/http"

	userpb "github.com/RAF-SI-2025/Banka-3-Backend/gen/proto/user/v1"
)

// refreshCookieName is the http-only cookie holding the opaque refresh
// token. Scoped to /api/v1/auth so it's not sent on every request.
const refreshCookieName = "refresh_token"

type loginRequestBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponseBody struct {
	AccessToken     string   `json:"accessToken"`
	AccessExpiresIn int64    `json:"accessExpiresIn"`
	UserID          string   `json:"userId"`
	UserKind        string   `json:"userKind"`
	Permissions     []string `json:"permissions"`
}

// LoginHandler takes JSON {email, password}, calls user.Login, sets the
// refresh token as an httpOnly session cookie (no Expires — dies with
// the browser per spec p.10: "ukoliko korisnik zatvori svoj web
// pretraživač potrebno je tražiti da se korisnik ponovno uloguje"), and
// returns the access token + metadata in the body.
func (r *Router) LoginHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body loginRequestBody
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		resp, err := r.Users.Login(req.Context(), &userpb.LoginRequest{
			Email:    body.Email,
			Password: body.Password,
		})
		if err != nil {
			writeGRPCError(w, err)
			return
		}
		setRefreshCookie(w, resp.GetRefreshToken(), r.SecureCookies)
		writeJSON(w, http.StatusOK, loginResponseBody{
			AccessToken:     resp.GetAccessToken(),
			AccessExpiresIn: resp.GetAccessExpiresIn(),
			UserID:          resp.GetUserId(),
			UserKind:        userKindString(resp.GetUserKind()),
			Permissions:     resp.GetPermissions(),
		})
	}
}

type refreshResponseBody struct {
	AccessToken     string `json:"accessToken"`
	AccessExpiresIn int64  `json:"accessExpiresIn"`
}

// RefreshHandler reads the refresh cookie, asks user.Refresh for a new
// token pair, sets a new cookie, and returns the new access token.
func (r *Router) RefreshHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		c, err := req.Cookie(refreshCookieName)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "no refresh token")
			return
		}
		resp, err := r.Users.Refresh(req.Context(), &userpb.RefreshRequest{
			RefreshToken: c.Value,
		})
		if err != nil {
			clearRefreshCookie(w, r.SecureCookies)
			writeGRPCError(w, err)
			return
		}
		setRefreshCookie(w, resp.GetRefreshToken(), r.SecureCookies)
		writeJSON(w, http.StatusOK, refreshResponseBody{
			AccessToken:     resp.GetAccessToken(),
			AccessExpiresIn: resp.GetAccessExpiresIn(),
		})
	}
}

// LogoutHandler revokes the refresh token and clears the cookie.
func (r *Router) LogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if c, err := req.Cookie(refreshCookieName); err == nil && c.Value != "" {
			_, _ = r.Users.Logout(req.Context(), &userpb.LogoutRequest{RefreshToken: c.Value})
		}
		clearRefreshCookie(w, r.SecureCookies)
		w.WriteHeader(http.StatusNoContent)
	}
}

// setRefreshCookie writes a *session* cookie (no Expires/Max-Age) so it
// dies with the browser. The refresh token's server-side expiry still
// caps lifetime — this just enforces re-login on browser close.
func setRefreshCookie(w http.ResponseWriter, value string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearRefreshCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func userKindString(k userpb.UserKind) string {
	switch k {
	case userpb.UserKind_USER_KIND_EMPLOYEE:
		return "employee"
	case userpb.UserKind_USER_KIND_CLIENT:
		return "client"
	}
	return "unknown"
}
