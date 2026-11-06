package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"image/png"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/argon2"
)

// Argon2id parameters from the OWASP password storage cheat sheet.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks password against a hash from HashPassword, reading
// the parameters from the hash so they can be raised later.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when the account does not exist, so a wrong
// email takes as long as a wrong password.
var dummyHash, _ = HashPassword("timesheet-filler-dummy-password")

func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < minPasswordLength || n > maxPasswordLength {
		return ErrWeakPassword
	}
	return nil
}

const (
	totpPeriod = 30
	totpIssuer = "Timesheet Filler"
)

func newTOTPKey(issuer, account string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account, Period: totpPeriod})
}

func otpKey(issuer, account, secret string) (*otp.Key, error) {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("period", strconv.Itoa(totpPeriod))
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	u := url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + issuer + ":" + account, RawQuery: q.Encode()}
	return otp.NewKeyFromURL(u.String())
}

func equalMAC(a, b []byte) bool { return hmac.Equal(a, b) }

// totpQR renders the otpauth:// URI of key as a PNG data URI.
func totpQR(key *otp.Key) (string, error) {
	img, err := key.Image(240, 240)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// matchTOTP returns the time step the code is valid for, allowing one step of
// clock skew either way. Callers reject steps already used to stop replays.
func matchTOTP(secret, code string, now time.Time) (int64, bool) {
	for _, skew := range []int64{0, -1, 1} {
		at := now.Add(time.Duration(skew*totpPeriod) * time.Second)
		want, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
			Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return at.Unix() / totpPeriod, true
		}
	}
	return 0, false
}

func randomDigits(n int) string {
	var b strings.Builder
	for range n {
		d, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			panic(fmt.Sprintf("auth: random: %v", err))
		}
		b.WriteByte(byte('0' + d.Int64()))
	}
	return b.String()
}

// Recovery codes carry 80 bits each, so a plain SHA-256 is enough to store them.
func newRecoveryCode() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("auth: random: %v", err))
	}
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return s[:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:]
}

func normalizeCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	return strings.NewReplacer(" ", "", "-", "").Replace(code)
}

func hashRecoveryCode(code string) []byte {
	sum := sha256.Sum256([]byte(normalizeCode(code)))
	return sum[:]
}

func (s *Service) emailCodeMAC(userID int64, code string) []byte {
	m := hmac.New(sha256.New, s.keys.MACKey("email-code"))
	fmt.Fprintf(m, "%d:%s", userID, code)
	return m.Sum(nil)
}

func totpAAD(userID int64) []byte { return fmt.Appendf(nil, "totp:%d", userID) }
