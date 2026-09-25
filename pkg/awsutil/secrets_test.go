package awsutil

import (
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
