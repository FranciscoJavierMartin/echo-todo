package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var cheap = PasswordParams{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

func TestHashAndVerify(t *testing.T) {
	h, err := cheap.Hash("correct horse battery")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "$argon2id$v=19$m=1024,t=1,p=1$"))

	ok, rehash, err := VerifyPassword(h, "correct horse battery", cheap)
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, rehash)

	ok, _, err = VerifyPassword(h, "wrong", cheap)
	require.NoError(t, err)
	require.False(t, ok)

	h2, _ := cheap.Hash("correct horse battery")
	require.NotEqual(t, h, h2, "per-hash random salt")
}

func TestDefaultParamsAreOWASP(t *testing.T) {
	require.Equal(t, uint32(64*1024), DefaultPasswordParams.MemoryKiB)
	require.Equal(t, uint32(3), DefaultPasswordParams.Iterations)
	require.Equal(t, uint8(2), DefaultPasswordParams.Parallelism)
}

func TestVerifyFlagsRehash(t *testing.T) {
	h, _ := cheap.Hash("pw-pw-pw-pw")
	stronger := cheap
	stronger.Iterations = 2
	ok, rehash, err := VerifyPassword(h, "pw-pw-pw-pw", stronger)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, rehash)
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"plain",
		"$bcrypt$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=18$m=1024,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=x,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=1024,t=1,p=1$!!$aGFzaA",
		"$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$!!",
		"$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$",
	} {
		_, _, err := VerifyPassword(bad, "x", cheap)
		require.ErrorIs(t, err, ErrMalformedHash, bad)
	}
}
