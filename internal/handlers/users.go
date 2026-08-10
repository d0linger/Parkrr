package handlers

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// ListUsers returns all users (admin only).
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, username, email, is_admin, role, totp_enabled, created_at, updated_at
		 FROM users ORDER BY username`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	users := []models.User{}
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.IsAdmin, &u.Role,
			&u.TOTPEnabled, &u.CreatedAt, &u.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

type userRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func normalizeRole(role string) (string, bool) {
	role = trim(role)
	if role == "" {
		role = models.RoleEditor
	}
	if !models.ValidRoles[role] {
		return "", false
	}
	return role, true
}

// CreateUser adds a new user (admin only).
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req userRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Username = trim(req.Username)
	req.Email = trim(req.Email)
	if !validUsernameLength(req.Username) {
		writeError(w, http.StatusBadRequest, "username is required and must be at most 100 bytes")
		return
	}
	if !validPasswordLength(req.Password) {
		writeError(w, http.StatusBadRequest, "password must be between 8 and 72 bytes")
		return
	}
	if !validEmailLength(req.Email) {
		writeError(w, http.StatusBadRequest, "email is too long")
		return
	}
	if h.rejectBreachedPassword(w, r, req.Password) {
		return
	}
	role, ok := normalizeRole(req.Role)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid role")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	var u models.User
	err = h.Pool.QueryRow(r.Context(),
		`INSERT INTO users (username, email, password_hash, role, is_admin)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, username, email, is_admin, role, totp_enabled, created_at, updated_at`,
		req.Username, req.Email, hash, role, role == models.RoleAdmin,
	).Scan(&u.ID, &u.Username, &u.Email, &u.IsAdmin, &u.Role, &u.TOTPEnabled,
		&u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}
	h.audit(r, "create", "user", u.ID, "created user "+u.Username+" ("+u.Role+")")
	writeJSON(w, http.StatusCreated, u)
}

// UpdateUser edits a user; password is only changed when provided (admin only).
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req userRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Username = trim(req.Username)
	req.Email = trim(req.Email)
	if !validUsernameLength(req.Username) {
		writeError(w, http.StatusBadRequest, "username is required and must be at most 100 bytes")
		return
	}
	if !validEmailLength(req.Email) {
		writeError(w, http.StatusBadRequest, "email is too long")
		return
	}
	role, ok := normalizeRole(req.Role)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid role")
		return
	}

	// Validate an optional password change up front, before any database
	// work (an empty password means "keep the current one").
	if req.Password != "" {
		if !validPasswordLength(req.Password) {
			writeError(w, http.StatusBadRequest, "password must be between 8 and 72 bytes")
			return
		}
		if h.rejectBreachedPassword(w, r, req.Password) {
			return
		}
	}

	// Prevent demoting the last admin.
	if role != models.RoleAdmin && h.isLastAdmin(r, id) {
		writeError(w, http.StatusConflict, "cannot demote the last remaining admin")
		return
	}

	var old models.User
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT id, username, email, role, is_admin FROM users WHERE id=$1`, id).
		Scan(&old.ID, &old.Username, &old.Email, &old.Role, &old.IsAdmin); err != nil {
		// A missing user must 404, not silently "succeed": the UPDATE below would
		// affect 0 rows yet still return 200 with a fabricated audit entry.
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	isAdmin := role == models.RoleAdmin
	if req.Password != "" {
		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not hash password")
			return
		}
		_, err = h.Pool.Exec(r.Context(),
			`UPDATE users SET username=$1, email=$2, role=$3, is_admin=$4, password_hash=$5, updated_at=now()
			 WHERE id=$6`, req.Username, req.Email, role, isAdmin, hash, id)
		if err != nil {
			handleUserUpdateErr(w, err)
			return
		}
		// An admin-forced password change must sign the user out everywhere so
		// the old credentials can no longer ride an existing session.
		_, _ = h.Pool.Exec(r.Context(), `DELETE FROM sessions WHERE user_id=$1`, id)
	} else {
		_, err := h.Pool.Exec(r.Context(),
			`UPDATE users SET username=$1, email=$2, role=$3, is_admin=$4, updated_at=now()
			 WHERE id=$5`, req.Username, req.Email, role, isAdmin, id)
		if err != nil {
			handleUserUpdateErr(w, err)
			return
		}
	}
	newU := old
	newU.Username, newU.Email, newU.Role, newU.IsAdmin = req.Username, req.Email, role, isAdmin
	changes := diffFields(old, newU, "created_at", "updated_at", "id", "totp_enabled", "is_admin")
	if req.Password != "" {
		if changes == nil {
			changes = map[string]any{}
		}
		changes["password"] = map[string]any{"old": "•••", "new": "•••"}
	}
	h.auditChange(r, "update", "user", id, "updated user "+req.Username+" ("+role+")", changes)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteUser removes a user (admin only); refuses to delete self or last admin.
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if cur, ok := auth.UserFrom(r.Context()); ok && cur.ID == id {
		writeError(w, http.StatusConflict, "you cannot delete your own account")
		return
	}
	if h.isLastAdmin(r, id) {
		writeError(w, http.StatusConflict, "cannot delete the last remaining admin")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete user")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	h.audit(r, "delete", "user", id, "deleted user")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ResetUserTOTP disables a user's 2FA and removes their recovery codes so they
// can re-enroll (admin only). Useful when a user loses their authenticator.
func (h *Handler) ResetUserTOTP(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var username string
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT username FROM users WHERE id=$1`, id).Scan(&username); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE users SET totp_enabled=FALSE, totp_secret='', updated_at=now() WHERE id=$1`,
		id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not reset two-factor")
		return
	}
	_, _ = h.Pool.Exec(r.Context(), `DELETE FROM totp_backup_codes WHERE user_id=$1`, id)
	h.audit(r, "update", "user", id, "reset 2FA for "+username)
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

// isLastAdmin reports whether the given id is an admin and the only one.
func (h *Handler) isLastAdmin(r *http.Request, id int64) bool {
	var count int
	var isAdmin bool
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT (SELECT count(*) FROM users WHERE is_admin),
		        COALESCE((SELECT is_admin FROM users WHERE id=$1), false)`, id,
	).Scan(&count, &isAdmin); err != nil {
		return false
	}
	return isAdmin && count <= 1
}

func handleUserUpdateErr(w http.ResponseWriter, err error) {
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "username already exists")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "could not update user")
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
