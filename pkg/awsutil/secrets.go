package awsutil

import (
	"os"
	"strings"
	"sync"

	"github.com/pkg/errors"

	"github.com/aws/aws-secretsmanager-caching-go/secretcache"
)

// secretsManagerArnPrefix marks an env value that is a reference to a secret
// rather than the secret itself.
const secretsManagerArnPrefix = "arn:aws:secretsmanager"

var (
	secretCacheOnce sync.Once
	secretCache     *secretcache.Cache
	secretCacheErr  error
)

// getSecretCache builds the Secrets Manager cache on first use.
//
// This was a package-level `var secretCache, _ = secretcache.New()`, which had
// two problems. secretcache.New() builds an aws-sdk-go session, so every cold
// start paid for it whether or not any env value was an ARN — on Yandex Cloud,
// where none ever is and there are no AWS credentials to build a session from,
// that cost buys nothing. And the discarded error left secretCache nil, so a
// later lookup panicked on a nil dereference instead of reporting why the
// session could not be built.
func getSecretCache() (*secretcache.Cache, error) {
	secretCacheOnce.Do(func() {
		secretCache, secretCacheErr = secretcache.New()
	})
	if secretCacheErr != nil {
		return nil, errors.Wrapf(secretCacheErr, "failed to init aws secrets manager cache")
	}
	return secretCache, nil
}

// GetEnvOrSecret reads an environment variable, resolving it through Secrets
// Manager when its value is a secret ARN rather than a literal.
func GetEnvOrSecret(envName string) (string, error) {
	envValue := os.Getenv(envName)
	if !strings.HasPrefix(envValue, secretsManagerArnPrefix) {
		return envValue, nil
	}
	cache, err := getSecretCache()
	if err != nil {
		return envValue, err
	}
	secretValue, err := cache.GetSecretString(envValue)
	if err != nil {
		// Returns the ARN rather than an empty string, preserving the contract
		// callers had before this was made lazy.
		return envValue, err
	}
	return secretValue, nil
}
