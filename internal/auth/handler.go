package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/logging"
	"github.com/sassinzz13/billiards-online/platform/security"
)

// Handler exposes auth over HTTP.
//
// secureCookies is false only in local development, where there is no TLS to attach the Secure
// attribute to. It is derived from APP_ENV, so production cannot accidentally ship insecure
// cookies.
type Handler struct {
	svc           *Service
	secureCookies bool
	limiter       *security.RateLimiter
}

func NewHandler(svc *Service, secureCookies bool, limiter *security.RateLimiter) *Handler {
	return &Handler{svc: svc, secureCookies: secureCookies, limiter: limiter}
}

type signupRequest struct {
	Email    string `json:"email"    binding:"required"`
	Handle   string `json:"handle"   binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginRequest struct {
	Email    string `json:"email"    binding:"required"`
	Password string `json:"password" binding:"required"`
}

// sessionResponse is what the client receives. It carries the account only.
//
// The token is NOT here: it goes out as an HttpOnly cookie, which is what keeps it unreadable by
// any script that manages to run on the page. Putting it in the body would defeat that entirely.
type sessionResponse struct {
	User      users.Public `json:"user"`
	Email     string       `json:"email"`
	ExpiresAt time.Time    `json:"expiresAt"`
}

func (h *Handler) signup(c *gin.Context) {
	if !h.allow(c, "signup") {
		return
	}

	var req signupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "auth.invalid_request", "Email, handle, and password are required.")
		return
	}

	res, err := h.svc.Signup(c.Request.Context(), req.Email, req.Handle, req.Password)
	if err != nil {
		h.writeSignupError(c, err)
		return
	}

	h.setSessionCookie(c, res)
	c.JSON(http.StatusCreated, sessionResponse{
		User:      res.User.Public(),
		Email:     res.User.Email,
		ExpiresAt: res.ExpiresAt,
	})
}

func (h *Handler) login(c *gin.Context) {
	if !h.allow(c, "login") {
		return
	}

	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "auth.invalid_request", "Email and password are required.")
		return
	}

	res, err := h.svc.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{
				"code":    "auth.invalid_credentials",
				"message": "Invalid email or password.",
			}})
			return
		}
		internalError(c, "login failed", err)
		return
	}

	// A successful login clears the attempt counter, so someone who mistyped a few times is not
	// left rate limited afterwards.
	h.limiter.Reset(limitKey(c, "login"))

	h.setSessionCookie(c, res)
	c.JSON(http.StatusOK, sessionResponse{
		User:      res.User.Public(),
		Email:     res.User.Email,
		ExpiresAt: res.ExpiresAt,
	})
}

// logout revokes the current session and clears the cookie.
//
// It returns 204 whether or not a session was present. Reporting "you were not logged in" would
// tell an attacker with a stolen cookie whether it was still live.
func (h *Handler) logout(c *gin.Context) {
	if identity, ok := IdentityFrom(c.Request.Context()); ok {
		if err := h.svc.Logout(c.Request.Context(), identity.SessionID); err != nil {
			internalError(c, "logout failed", err)
			return
		}
	}
	h.clearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

// session returns the current account. Behind RequireAuth, so reaching it means a live session.
func (h *Handler) session(c *gin.Context) {
	identity, ok := IdentityFrom(c.Request.Context())
	if !ok {
		internalError(c, "session handler reached without identity", errors.New("missing identity"))
		return
	}

	user, err := h.svc.users.ByID(c.Request.Context(), identity.UserID)
	if err != nil {
		internalError(c, "load session user", err)
		return
	}
	c.JSON(http.StatusOK, sessionResponse{
		User:      user.Public(),
		Email:     user.Email,
		ExpiresAt: identity.ExpiresAt,
	})
}

// logoutAll revokes every session for the user, on every device.
func (h *Handler) logoutAll(c *gin.Context) {
	identity, ok := IdentityFrom(c.Request.Context())
	if !ok {
		internalError(c, "logoutAll reached without identity", errors.New("missing identity"))
		return
	}

	n, err := h.svc.LogoutAll(c.Request.Context(), identity.UserID)
	if err != nil {
		internalError(c, "revoke all sessions", err)
		return
	}
	h.clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"revoked": n})
}

func (h *Handler) setSessionCookie(c *gin.Context, res Result) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:  CookieName,
		Value: string(res.Token),
		Path:  "/",
		// No Domain: the cookie stays scoped to the exact host that set it. Path-based routing puts
		// the app and API on one host, so this is sufficient and does not widen scope to any
		// sibling subdomain (MEMORY.md §10a).
		Expires:  res.ExpiresAt,
		MaxAge:   int(time.Until(res.ExpiresAt).Seconds()),
		HttpOnly: true, // unreadable from JavaScript, so XSS cannot exfiltrate the session
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// allow applies the rate limit, writing a 429 and returning false when exceeded.
func (h *Handler) allow(c *gin.Context, action string) bool {
	if h.limiter.Allow(limitKey(c, action)) {
		return true
	}
	logging.Logger(c.Request.Context()).Warn("rate limited",
		"action", action, "clientIp", c.ClientIP())
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{
		"code":    "auth.rate_limited",
		"message": "Too many attempts. Please wait and try again.",
	}})
	return false
}

// limitKey buckets by client IP and action, so exhausting the login limit does not also block
// signup from the same address.
func limitKey(c *gin.Context, action string) string {
	return action + "|" + c.ClientIP()
}

func (h *Handler) writeSignupError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, users.ErrEmailTaken):
		// This does disclose that an address is registered. It is unavoidable — the account cannot
		// be created — and every signup form has the same property. Login stays uniform, which is
		// where the oracle would actually be useful to an attacker.
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{
			"code":    "auth.email_taken",
			"message": "That email is already registered.",
		}})
	case errors.Is(err, users.ErrHandleTaken):
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{
			"code":    "auth.handle_taken",
			"message": "That handle is already taken.",
		}})
	case errors.Is(err, users.ErrInvalidEmail):
		badRequest(c, "auth.invalid_email", "Enter a valid email address.")
	case errors.Is(err, users.ErrInvalidHandle):
		badRequest(c, "auth.invalid_handle", users.ErrInvalidHandle.Error())
	case errors.Is(err, security.ErrPasswordTooShort), errors.Is(err, security.ErrPasswordTooLong):
		badRequest(c, "auth.invalid_password", err.Error())
	default:
		internalError(c, "signup failed", err)
	}
}

func badRequest(c *gin.Context, code, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": code, "message": message}})
}

// internalError logs the cause and returns an opaque response.
//
// The error is never echoed to the client: pgx errors name tables, columns, and constraints, and
// that is exactly the internal structure §42 and §51 require staying server-side.
func internalError(c *gin.Context, msg string, err error) {
	logging.Logger(c.Request.Context()).Error(msg, "error", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
		"code":    "internal",
		"message": "An internal error occurred.",
	}})
}
