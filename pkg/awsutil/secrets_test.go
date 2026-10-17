package awsutil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEnvOrSecretReturnsLiteralWithoutTouchingAws(t *testing.T) {
	// The cache must stay unbuilt for a literal value: on Yandex Cloud no env
	// value is ever an ARN, and there are no AWS credentials to build a session
	// from, so constructing one on every cold start buys nothing.
	t.Setenv("TEST_SECRET_ENV", "plain-value")

	value, err := GetEnvOrSecret("TEST_SECRET_ENV")

	require.NoError(t, err)
	assert.Equal(t, "plain-value", value)
	assert.Nil(t, secretCache, "secrets manager cache must not be built for a non-ARN value")
}

func TestGetEnvOrSecretReturnsEmptyForUnsetVariable(t *testing.T) {
	value, err := GetEnvOrSecret("TEST_SECRET_ENV_THAT_IS_NOT_SET")

	require.NoError(t, err)
	assert.Empty(t, value)
}

// TestLoadSecretBundleUnsetIsNoop pins the "SDK pin can roll out before any
// service opts in" contract: an unset bundle env must not error and must not
// touch the Secrets Manager cache.
func TestLoadSecretBundleUnsetIsNoop(t *testing.T) {
	// Explicitly unset (t.Setenv then unset is safe for test isolation).
	t.Setenv(SecretsBundleEnv, "")
	require.NoError(t, LoadSecretBundle(context.Background()))
}

// TestLoadSecretBundleRejectsNonArn — operator typo'd a plain string into
// the bundle env; fail loudly so the misconfig surfaces instead of being
// silently ignored.
func TestLoadSecretBundleRejectsNonArn(t *testing.T) {
	t.Setenv(SecretsBundleEnv, "oops-not-an-arn")
	err := LoadSecretBundle(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a Secrets Manager ARN")
}

// TestLoadSecretBundleFromEnvRespectsExplicitEnvName lets a service pick
// a bundle env name that isn't FORGE_SECRETS_BUNDLE_ARN (useful for staged
// migrations). Default remains the constant for the common path.
func TestLoadSecretBundleFromEnvRespectsExplicitEnvName(t *testing.T) {
	t.Setenv("MY_OTHER_BUNDLE", "")
	require.NoError(t, LoadSecretBundleFromEnv(context.Background(), "MY_OTHER_BUNDLE"))
}

// TestFirstN pins the error-message truncation helper (used in the
// non-ARN rejection path to avoid leaking a long value into the log).
func TestFirstN(t *testing.T) {
	assert.Equal(t, "abc", firstN("abc", 10))
	assert.Equal(t, "abcde", firstN("abcde", 5))
	assert.Equal(t, "abc...", firstN("abcdefghij", 3))
}
