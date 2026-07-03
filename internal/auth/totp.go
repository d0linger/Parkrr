package auth

import (
	"bytes"
	"encoding/base64"
	"image/png"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

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
