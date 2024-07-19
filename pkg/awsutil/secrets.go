package awsutil

import (
	"os"
	"strings"

	"github.com/aws/aws-secretsmanager-caching-go/secretcache"
)

var secretCache, _ = secretcache.New()

func GetEnvOrSecret(envName string) (string, error) {
	envValue := os.Getenv(envName)
	var err error
	if strings.HasPrefix(envValue, "arn:aws:secretsmanager") {
		envValue, err = secretCache.GetSecretString(envValue)
		if err != nil {
			return envValue, err
		}
	}
	return envValue, err
}
