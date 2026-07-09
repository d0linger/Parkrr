package auth

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/models"
)

// ErrWebAuthnInternal marks a backend/storage failure (credential load/save,
// user lookup) — as opposed to a failed cryptographic verification. Handlers
// must NOT count it as a brute-force attempt: transient DB errors should not
// consume a client's rate-limit budget.
var ErrWebAuthnInternal = errors.New("webauthn backend error")

// internalErr tags a backend error as ErrWebAuthnInternal while preserving the
// original for logging (errors.Is/As still see both).
func internalErr(err error) error {
	if err == nil {
		return nil
	}
	return errors.Join(ErrWebAuthnInternal, err)
}

// WebAuthnService implements passkey (WebAuthn) registration and login. It is
// nil/disabled unless a Relying Party ID is configured.
type WebAuthnService struct {
	wa   *webauthn.WebAuthn
	pool *pgxpool.Pool
}

// NewWebAuthnService builds the service. It returns (nil, nil) when rpID is
// empty, meaning passkeys are disabled.
func NewWebAuthnService(pool *pgxpool.Pool, rpID, rpName string, origins []string) (*WebAuthnService, error) {
	if rpID == "" {
		return nil, nil
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: rpName,
		RPOrigins:     origins,
	})
	if err != nil {
		return nil, err
	}
	return &WebAuthnService{wa: wa, pool: pool}, nil
}

// Enabled reports whether passkey support is active.
func (s *WebAuthnService) Enabled() bool { return s != nil && s.wa != nil }

// --- webauthn.User adapter ---

type webAuthnUser struct {
	id    int64
	name  string
	creds []webauthn.Credential
}

func userHandle(id int64) []byte {
	b := make([]byte, 8)
	// #nosec G115 -- user id is a positive bigserial; bit-preserving encoding of
	// the opaque 8-byte WebAuthn user handle.
	binary.BigEndian.PutUint64(b, uint64(id))
	return b
}

func handleToID(b []byte) int64 {
	if len(b) != 8 {
		return 0
	}
	// #nosec G115 -- exact inverse of userHandle; ids originate from int64.
	return int64(binary.BigEndian.Uint64(b))
}

func (u *webAuthnUser) WebAuthnID() []byte                         { return userHandle(u.id) }
func (u *webAuthnUser) WebAuthnName() string                       { return u.name }
func (u *webAuthnUser) WebAuthnDisplayName() string                { return u.name }
func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }
func (u *webAuthnUser) WebAuthnIcon() string                       { return "" }

// --- ceremonies ---

// BeginRegistration starts enrolling a new passkey for the authenticated user.
// A resident (discoverable) key with user verification is required so the user
// can later sign in without typing a username.
func (s *WebAuthnService) BeginRegistration(ctx context.Context, u *models.User) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	creds, err := s.loadCredentials(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	wu := &webAuthnUser{id: u.ID, name: u.Username, creds: creds}
	exclude := make([]protocol.CredentialDescriptor, 0, len(creds))
	for _, c := range creds {
		exclude = append(exclude, c.Descriptor())
	}
	sel := protocol.AuthenticatorSelection{
		ResidentKey:      protocol.ResidentKeyRequirementRequired,
		UserVerification: protocol.VerificationRequired,
	}
	return s.wa.BeginRegistration(wu,
		webauthn.WithAuthenticatorSelection(sel),
		webauthn.WithExclusions(exclude),
	)
}

// FinishRegistration verifies the attestation response and stores the credential.
func (s *WebAuthnService) FinishRegistration(ctx context.Context, u *models.User, sd webauthn.SessionData, r *http.Request, name string) error {
	creds, err := s.loadCredentials(ctx, u.ID)
	if err != nil {
		return internalErr(err)
	}
	wu := &webAuthnUser{id: u.ID, name: u.Username, creds: creds}
	// Only the library step is a verification of attacker-supplied input; the
	// surrounding load/save are backend operations.
	cred, err := s.wa.FinishRegistration(wu, sd, r)
	if err != nil {
		return err
	}
	if err := s.saveCredential(ctx, u.ID, cred, name); err != nil {
		return internalErr(err)
	}
	return nil
}

// BeginLogin starts a usernameless (discoverable) passkey login.
func (s *WebAuthnService) BeginLogin() (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return s.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
}

// FinishLogin verifies the assertion and returns the id of the user it belongs
// to. The sign counter is advanced to detect cloned authenticators.
func (s *WebAuthnService) FinishLogin(ctx context.Context, sd webauthn.SessionData, r *http.Request) (int64, error) {
	var uid int64
	// handlerErr captures backend failures from the user-lookup callback so they
	// can be distinguished from a genuine assertion-verification failure below.
	var handlerErr error
	handler := func(_, rawUserHandle []byte) (webauthn.User, error) {
		uid = handleToID(rawUserHandle)
		u, err := s.userByID(ctx, uid)
		if err != nil {
			handlerErr = err
			return nil, err
		}
		creds, err := s.loadCredentials(ctx, uid)
		if err != nil {
			handlerErr = err
			return nil, err
		}
		return &webAuthnUser{id: uid, name: u.Username, creds: creds}, nil
	}
	cred, err := s.wa.FinishDiscoverableLogin(handler, sd, r)
	if err != nil {
		if handlerErr != nil {
			// A backend/lookup failure (or stale credential), not a forged
			// assertion — don't let it count against the rate limit.
			return 0, internalErr(handlerErr)
		}
		return 0, err
	}
	// The login already succeeded; a failed counter write must not fail it, but
	// log it since a stale counter degrades cloned-authenticator detection.
	if err := s.updateSignCount(ctx, cred.ID, cred.Authenticator.SignCount); err != nil {
		slog.Warn("failed to update passkey sign count", "user_id", uid, "err", err)
	}
	return uid, nil
}

// --- credential store ---

// CredentialInfo is the UI-facing view of a stored passkey.
type CredentialInfo struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func (s *WebAuthnService) loadCredentials(ctx context.Context, userID int64) ([]webauthn.Credential, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT credential_id, public_key, attestation_type, aaguid, sign_count,
		        transports, backup_eligible, backup_state
		 FROM webauthn_credentials WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []webauthn.Credential
	for rows.Next() {
		var (
			c          webauthn.Credential
			transports string
			signCount  int64
			beligible  bool
			bstate     bool
		)
		if err := rows.Scan(&c.ID, &c.PublicKey, &c.AttestationType, &c.Authenticator.AAGUID,
			&signCount, &transports, &beligible, &bstate); err != nil {
			return nil, err
		}
		c.Authenticator.SignCount = uint32(signCount) // #nosec G115 -- counter fits uint32 per spec
		c.Flags.BackupEligible = beligible
		c.Flags.BackupState = bstate
		for _, tr := range strings.Split(transports, ",") {
			if tr = strings.TrimSpace(tr); tr != "" {
				c.Transport = append(c.Transport, protocol.AuthenticatorTransport(tr))
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *WebAuthnService) saveCredential(ctx context.Context, userID int64, c *webauthn.Credential, name string) error {
	transports := make([]string, 0, len(c.Transport))
	for _, tr := range c.Transport {
		transports = append(transports, string(tr))
	}
	if strings.TrimSpace(name) == "" {
		name = "Passkey"
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO webauthn_credentials
		   (user_id, credential_id, public_key, attestation_type, aaguid, sign_count,
		    transports, backup_eligible, backup_state, name)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		userID, c.ID, c.PublicKey, c.AttestationType, c.Authenticator.AAGUID,
		int64(c.Authenticator.SignCount), strings.Join(transports, ","),
		c.Flags.BackupEligible, c.Flags.BackupState, strings.TrimSpace(name))
	return err
}

func (s *WebAuthnService) updateSignCount(ctx context.Context, credID []byte, count uint32) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE webauthn_credentials SET sign_count=$1, last_used_at=now() WHERE credential_id=$2`,
		int64(count), credID)
	return err
}

func (s *WebAuthnService) userByID(ctx context.Context, id int64) (*models.User, error) {
	var u models.User
	err := s.pool.QueryRow(ctx,
		`SELECT id, username FROM users WHERE id=$1`, id).Scan(&u.ID, &u.Username)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ListCredentials returns passkey metadata for display.
func (s *WebAuthnService) ListCredentials(ctx context.Context, userID int64) ([]CredentialInfo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, created_at, last_used_at
		 FROM webauthn_credentials WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CredentialInfo{}
	for rows.Next() {
		var ci CredentialInfo
		if err := rows.Scan(&ci.ID, &ci.Name, &ci.CreatedAt, &ci.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, ci)
	}
	return out, rows.Err()
}

// DeleteCredential removes one passkey belonging to the user.
func (s *WebAuthnService) DeleteCredential(ctx context.Context, userID, id int64) (int64, error) {
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM webauthn_credentials WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
