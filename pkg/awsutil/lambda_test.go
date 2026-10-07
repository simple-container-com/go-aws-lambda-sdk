package awsutil

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aws/aws-lambda-go/events"
)

// A BUFFERED-mode Lambda renders its response through aws-lambda-go-api-proxy,
// whose writer base64-encodes any body that is not valid UTF-8 and reports it
// via IsBase64Encoded. Dropping the flag during the conversion delivers the
// base64 text itself to the client instead of the bytes, so the round trip has
// to preserve it.
func TestToLambdaFunctionURLResponsePreservesBase64Flag(t *testing.T) {
	// A 1x1 transparent PNG — genuinely non-UTF-8, which is what trips the
	// proxy writer's utf8.Valid branch.
	png := []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89,
	}
	encoded := base64.StdEncoding.EncodeToString(png)

	res := ToLambdaFunctionURLResponse(events.APIGatewayProxyResponse{
		StatusCode:      http.StatusOK,
		Body:            encoded,
		IsBase64Encoded: true,
		MultiValueHeaders: map[string][]string{
			"Content-Type": {"image/png"},
		},
	})

	assert.True(t, res.IsBase64Encoded, "binary response must stay marked as base64")
	assert.Equal(t, encoded, res.Body)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "image/png", res.Headers["Content-Type"])

	decoded, err := base64.StdEncoding.DecodeString(res.Body)
	require.NoError(t, err)
	assert.Equal(t, png, decoded, "the client must be able to recover the exact bytes")
}

func TestToLambdaFunctionURLResponseLeavesTextUnencoded(t *testing.T) {
	res := ToLambdaFunctionURLResponse(events.APIGatewayProxyResponse{
		StatusCode:      http.StatusOK,
		Body:            `{"ok":true}`,
		IsBase64Encoded: false,
		MultiValueHeaders: map[string][]string{
			"Content-Type": {"application/json"},
		},
	})

	assert.False(t, res.IsBase64Encoded)
	assert.Equal(t, `{"ok":true}`, res.Body)
}
