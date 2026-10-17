package awsutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-secretsmanager-caching-go/secretcache"
	"github.com/pkg/errors"
)

// secretsManagerArnPrefix marks an env value that is a reference to a secret
// rather than the secret itself.
const secretsManagerArnPrefix = "arn:aws:secretsmanager"

// SecretsBundleEnv is the env name every service reads to find the bundle
// Secrets Manager ARN. SC's Pulumi renderer writes exactly this env name
// when `cloudExtras.secretsBundle` is set — see
// `forge/docs/design/2026-10-17/sc-secrets-bundle-mode/README.md` §3.3.
// Services that want a different env name can call LoadSecretBundleFromEnv.
const SecretsBundleEnv = "FORGE_SECRETS_BUNDLE_ARN"

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

// LoadSecretBundle resolves the Secrets Manager ARN carried by
// FORGE_SECRETS_BUNDLE_ARN (the SecretsBundleEnv constant), parses it as a
// JSON object of {envName:string → value:string}, and os.Setenv's each key.
//
// Services that opt into bundle mode (SC `cloudExtras.secretsBundle`,
// design 2026-10-17) call this ONCE from main() BEFORE any code reads a
// secret env. After it returns, every GetEnvOrSecret("TAVILY_API_KEY")
// (and every plain os.Getenv) returns the loaded value — the rest of the
// fleet continues to use the same env-read contract it already uses.
//
// Design decisions worth knowing:
//
//   - A missing SecretsBundleEnv is a NO-OP (nil error). Legacy per-key
//     ARN envs continue to work via GetEnvOrSecret unchanged. This lets
//     the SDK pin update roll out before any service opts in.
//
//   - A set-but-not-ARN SecretsBundleEnv value is a hard error — the
//     operator typo'd something; failing loudly beats loading garbage.
//
//   - The underlying fetch rides the SAME secretcache.Cache that
//     GetEnvOrSecret uses, so no second AWS session is built. On Yandex
//     Cloud (no AWS creds) the cache stays unbuilt because the bundle
//     env isn't set either.
//
//   - os.Setenv NEVER overwrites a value already set in the environment
//     (bundle values yield to anything the operator pinned via `env:`
//     in client.yaml). This matches the fleet-wide expectation that
//     literal env wins.
//
//   - JSON values that are not strings are rejected (an operator who
//     seeds a nested object by accident gets a loud error, not silent
//     flattening into Go's default string format).
func LoadSecretBundle(ctx context.Context) error {
	return LoadSecretBundleFromEnv(ctx, SecretsBundleEnv)
}

// LoadSecretBundleFromEnv is the explicit-env-name variant for services
// that need a different bundle env name (e.g. staged migrations). Shares
// LoadSecretBundle's semantics otherwise.
func LoadSecretBundleFromEnv(ctx context.Context, envName string) error {
	_ = ctx // kept on the signature so a future retry/timeout wrap doesn't break callers.

	arn := os.Getenv(envName)
	if arn == "" {
		return nil
	}
	if !strings.HasPrefix(arn, secretsManagerArnPrefix) {
		return fmt.Errorf("%s is set but is not a Secrets Manager ARN (got prefix %q)", envName, firstN(arn, 32))
	}

	cache, err := getSecretCache()
	if err != nil {
		return errors.Wrapf(err, "load secret bundle: get cache")
	}
	payload, err := cache.GetSecretString(arn)
	if err != nil {
		return errors.Wrapf(err, "load secret bundle: fetch %s", envName)
	}

	// Parse as JSON object of string values. Non-string values are a
	// loud error — see the doc comment.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return errors.Wrapf(err, "load secret bundle: decode JSON")
	}

	for key, msg := range raw {
		var value string
		if err := json.Unmarshal(msg, &value); err != nil {
			return fmt.Errorf("load secret bundle: value for %q is not a string", key)
		}
		if _, present := os.LookupEnv(key); present {
			// Operator-set env wins. Skip silently — the operator pinned
			// a literal in client.yaml deliberately.
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return errors.Wrapf(err, "load secret bundle: setenv %q", key)
		}
	}
	return nil
}

// firstN returns the first n runes of s (safe on short strings), used for
// bounded error messages that must not leak a long ARN or payload.
func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
