package secretbox

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func key(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func TestSealOpenRoundTrip(t *testing.T) {
	kr, err := ParseKeyring("v1:" + key(1))
	require.NoError(t, err)
	aad := []byte("edc_connection:7:3")

	sealed, err := kr.Seal([]byte("refresh-token-value"), aad)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(sealed, "v1:"))
	assert.NotContains(t, sealed, "refresh-token-value")

	pt, ver, err := kr.Open(sealed, aad)
	require.NoError(t, err)
	assert.Equal(t, "refresh-token-value", string(pt))
	assert.Equal(t, "v1", ver)
}

func TestSealUsesFreshNonce(t *testing.T) {
	kr, _ := ParseKeyring("v1:" + key(1))
	a, _ := kr.Seal([]byte("x"), nil)
	b, _ := kr.Seal([]byte("x"), nil)
	assert.NotEqual(t, a, b)
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	kr, _ := ParseKeyring("v1:" + key(1))
	sealed, _ := kr.Seal([]byte("secret"), []byte("aad"))
	version, b64, _ := strings.Cut(sealed, ":")
	raw, _ := base64.StdEncoding.DecodeString(b64)
	raw[len(raw)-1] ^= 0xff
	_, _, err := kr.Open(version+":"+base64.StdEncoding.EncodeToString(raw), []byte("aad"))
	assert.ErrorIs(t, err, ErrDecrypt)
}

func TestOpenRejectsWrongAAD(t *testing.T) {
	kr, _ := ParseKeyring("v1:" + key(1))
	sealed, _ := kr.Seal([]byte("secret"), []byte("edc_connection:1:1"))
	_, _, err := kr.Open(sealed, []byte("edc_connection:2:1"))
	assert.ErrorIs(t, err, ErrDecrypt)
}

func TestOpenRejectsUnknownVersionAndMalformed(t *testing.T) {
	kr, _ := ParseKeyring("v1:" + key(1))
	_, _, err := kr.Open("v9:AAAA", nil)
	assert.ErrorIs(t, err, ErrUnknownKeyVersion)
	_, _, err = kr.Open("no-colon", nil)
	assert.ErrorIs(t, err, ErrMalformed)
	_, _, err = kr.Open("v1:!!notbase64", nil)
	assert.ErrorIs(t, err, ErrMalformed)
	_, _, err = kr.Open("v1:AAAA", nil)
	assert.ErrorIs(t, err, ErrMalformed)
}

func TestRotationOldKeyStillOpens(t *testing.T) {
	old, _ := ParseKeyring("v1:" + key(1))
	sealed, _ := old.Seal([]byte("tok"), []byte("a"))

	rotated, err := ParseKeyring("v2:" + key(2) + ", v1:" + key(1))
	require.NoError(t, err)
	assert.Equal(t, "v2", rotated.ActiveVersion())

	pt, ver, err := rotated.Open(sealed, []byte("a"))
	require.NoError(t, err)
	assert.Equal(t, "tok", string(pt))
	assert.Equal(t, "v1", ver)

	resealed, _ := rotated.Seal(pt, []byte("a"))
	assert.True(t, strings.HasPrefix(resealed, "v2:"))
}

func TestParseKeyringErrors(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	cases := map[string]string{
		"empty":         "",
		"no version":    key(1),
		"bad version":   "x1:" + key(1),
		"bad base64":    "v1:***",
		"short key":     "v1:" + short,
		"duplicate ver": "v1:" + key(1) + ",v1:" + key(2),
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseKeyring(spec)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), key(1), "error must not echo key material")
		})
	}
}

func TestMACKeyIsPurposeBound(t *testing.T) {
	kr, err := ParseKeyring("v1:" + key(1))
	require.NoError(t, err)
	assert.Len(t, kr.MACKey("a"), 32)
	assert.Equal(t, kr.MACKey("a"), kr.MACKey("a"))
	assert.NotEqual(t, kr.MACKey("a"), kr.MACKey("b"))
}
