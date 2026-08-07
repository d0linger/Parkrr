package auth

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"image/png"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// totpPeriod is the TOTP time-step in seconds (pquerna's default, matching
// GenerateTOTP). A code's identity for replay purposes is unixTime / totpPeriod.
const totpPeriod = 30

// TOTPIssuer is the label shown in authenticator apps.
const TOTPIssuer = "Parkrr"

// GenerateTOTP creates a new TOTP secret key for the given account name.
func GenerateTOTP(account string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{
		Issuer:      TOTPIssuer,
		AccountName: account,
	})
}

// ValidateTOTP checks a 6-digit code against the secret.
func ValidateTOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}

// ValidateTOTPStep checks a code against the secret across the accepted ±1 skew
// window (matching ValidateTOTP) and, on success, returns the 30-second time-step
// it matched. The step lets the caller reject replays: accept a code only if its
// step is newer than the last one already consumed for that user.
func ValidateTOTPStep(secret, code string, now time.Time) (int64, bool) {
	for _, skew := range []int64{0, -1, 1} {
		t := now.Add(time.Duration(skew*totpPeriod) * time.Second)
		want, err := totp.GenerateCode(secret, t)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return t.Unix() / totpPeriod, true
		}
	}
	return 0, false
}

// QRCodeDataURI renders the key's provisioning URI as a PNG data URI so the
// frontend can display it without any external QR library.
func QRCodeDataURI(key *otp.Key) (string, error) {
	img, err := key.Image(220, 220)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
