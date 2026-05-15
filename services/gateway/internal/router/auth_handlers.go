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
	// LongLivedSession is set by the mobile app (no cookie jar, spec
	// p.84 no session interval). Absent/false for web → unchanged
	// cookie flow.
	LongLivedSession bool `json:"longLivedSession"`
}

type loginResponseBody struct {
	AccessToken     string   `json:"accessToken"`
	AccessExpiresIn int64    `json:"accessExpiresIn"`
	UserID          string   `json:"userId"`
	UserKind        string   `json:"userKind"`
	Permissions     []string `json:"permissions"`
	FirstName       string   `json:"firstName,omitempty"`
	LastName        string   `json:"lastName,omitempty"`
	// RefreshToken is returned ONLY on the mobile (long-lived) path.
	// omitempty keeps the web response byte-identical to before.
	RefreshToken string `json:"refreshToken,omitempty"`
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
			Email:            body.Email,
			Password:         body.Password,
			LongLivedSession: body.LongLivedSession,
		})
		if err != nil {
			writeGRPCError(w, err)
			return
		}
		out := loginResponseBody{
			AccessToken:     resp.GetAccessToken(),
			AccessExpiresIn: resp.GetAccessExpiresIn(),
			UserID:          resp.GetUserId(),
			UserKind:        userKindString(resp.GetUserKind()),
			Permissions:     resp.GetPermissions(),
			FirstName:       resp.GetFirstName(),
			LastName:        resp.GetLastName(),
		}
		// Web: refresh token in the httpOnly cookie (unchanged).
		// Mobile: in the body (no cookie jar), and we skip the cookie
		// entirely so the web path stays byte-identical.
		if body.LongLivedSession {
			out.RefreshToken = resp.GetRefreshToken()
		} else {
			setRefreshCookie(w, resp.GetRefreshToken(), r.SecureCookies)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

type refreshRequestBody struct {
	RefreshToken     string `json:"refreshToken"`
	LongLivedSession bool   `json:"longLivedSession"`
}

type refreshResponseBody struct {
	AccessToken     string `json:"accessToken"`
	AccessExpiresIn int64  `json:"accessExpiresIn"`
	// Rotated refresh token, mobile path only (omitempty → web JSON
	// unchanged; web gets the rotated token in the cookie instead).
	RefreshToken string `json:"refreshToken,omitempty"`
}

// RefreshHandler rotates the refresh token. Web sends it in the
// httpOnly cookie (unchanged path: new cookie, no body token). Mobile
// has no cookie jar, so when the cookie is absent we accept the token
// in the JSON body and return the rotated one in the body, keeping
// long_lived_session so the new token stays long-lived.
func (r *Router) RefreshHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if c, err := req.Cookie(refreshCookieName); err == nil && c.Value != "" {
			resp, rerr := r.Users.Refresh(req.Context(), &userpb.RefreshRequest{
				RefreshToken: c.Value,
			})
			if rerr != nil {
				clearRefreshCookie(w, r.SecureCookies)
				writeGRPCError(w, rerr)
				return
			}
			setRefreshCookie(w, resp.GetRefreshToken(), r.SecureCookies)
			writeJSON(w, http.StatusOK, refreshResponseBody{
				AccessToken:     resp.GetAccessToken(),
				AccessExpiresIn: resp.GetAccessExpiresIn(),
			})
			return
		}

		// No cookie → mobile body path.
		var body refreshRequestBody
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.RefreshToken == "" {
			writeError(w, http.StatusUnauthorized, "no refresh token")
			return
		}
		resp, err := r.Users.Refresh(req.Context(), &userpb.RefreshRequest{
			RefreshToken:     body.RefreshToken,
			LongLivedSession: body.LongLivedSession,
		})
		if err != nil {
			writeGRPCError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, refreshResponseBody{
			AccessToken:     resp.GetAccessToken(),
			AccessExpiresIn: resp.GetAccessExpiresIn(),
			RefreshToken:    resp.GetRefreshToken(),
		})
	}
}

// LogoutHandler revokes the refresh token and clears the cookie.
func (r *Router) LogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if c, err := req.Cookie(refreshCookieName); err == nil && c.Value != "" {
			_, _ = r.Users.Logout(req.Context(), &userpb.LogoutRequest{RefreshToken: c.Value})
		} else {
			// Mobile path: refresh token in the body so the
			// server-side row is actually revoked on sign-out.
			var body refreshRequestBody
			if json.NewDecoder(req.Body).Decode(&body) == nil && body.RefreshToken != "" {
				_, _ = r.Users.Logout(req.Context(), &userpb.LogoutRequest{RefreshToken: body.RefreshToken})
			}
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
