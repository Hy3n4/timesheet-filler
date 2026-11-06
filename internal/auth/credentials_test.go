package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$"))
	assert.True(t, VerifyPassword(hash, "correct horse battery"))
	assert.False(t, VerifyPassword(hash, "correct horse batterY"))
	assert.False(t, VerifyPassword("garbage", "correct horse battery"))

	other, err := HashPassword("correct horse battery")
	require.NoError(t, err)
	assert.NotEqual(t, hash, other, "salt must differ")
}

func TestValidatePassword(t *testing.T) {
	assert.ErrorIs(t, ValidatePassword("short"), ErrWeakPassword)
	assert.ErrorIs(t, ValidatePassword(strings.Repeat("x", 257)), ErrWeakPassword)
	assert.NoError(t, ValidatePassword("dvanáct znak"))
}

func TestMatchTOTPAllowsOneStepOfSkew(t *testing.T) {
	key, err := newTOTPKey(totpIssuer, "coach@example.com")
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0)
	code := func(at time.Time) string {
		c, err := totp.GenerateCodeCustom(key.Secret(), at, totp.ValidateOpts{
			Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		require.NoError(t, err)
		return c
	}

	step, ok := matchTOTP(key.Secret(), code(now), now)
	assert.True(t, ok)
	assert.Equal(t, now.Unix()/totpPeriod, step)

	step, ok = matchTOTP(key.Secret(), code(now.Add(-30*time.Second)), now)
	assert.True(t, ok)
	assert.Equal(t, now.Unix()/totpPeriod-1, step)

	_, ok = matchTOTP(key.Secret(), code(now.Add(-90*time.Second)), now)
	assert.False(t, ok)
}

func TestOTPKeyRebuildsSameSecret(t *testing.T) {
	key, err := newTOTPKey(totpIssuer, "coach@example.com")
	require.NoError(t, err)
	again, err := otpKey(totpIssuer, "coach@example.com", key.Secret())
	require.NoError(t, err)
	assert.Equal(t, key.Secret(), again.Secret())
	assert.Equal(t, "coach@example.com", again.AccountName())
	assert.Equal(t, totpIssuer, again.Issuer())
}

func TestRecoveryCodeNormalization(t *testing.T) {
	code := newRecoveryCode()
	assert.Len(t, code, 19)
	assert.Equal(t, hashRecoveryCode(code), hashRecoveryCode(" "+strings.ToLower(strings.ReplaceAll(code, "-", ""))+" "))
	assert.NotEqual(t, hashRecoveryCode(code), hashRecoveryCode(newRecoveryCode()))
}

func TestRandomDigits(t *testing.T) {
	d := randomDigits(6)
	assert.Len(t, d, 6)
	assert.Empty(t, strings.Trim(d, "0123456789"))
}
