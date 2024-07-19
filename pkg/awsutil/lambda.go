package awsutil

import (
	"encoding/base64"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/samber/lo"
)

func ToLambdaFunctionURLResponse(res events.APIGatewayProxyResponse) events.LambdaFunctionURLResponse {
	return events.LambdaFunctionURLResponse{
		Headers: lo.MapValues(res.MultiValueHeaders, func(value []string, key string) string {
			if len(value) == 1 {
				return value[0]
			} else if len(value) > 1 {
				return strings.Join(value, "; ")
			}
			return ""
		}),
		Body:       res.Body,
		StatusCode: res.StatusCode,
	}
}

func ToAPIGatewayRequest(request events.LambdaFunctionURLRequest) events.APIGatewayProxyRequest {
	body := request.Body
	if request.IsBase64Encoded {
		data, err := base64.StdEncoding.DecodeString(body)
		if err == nil {
			body = string(data)
		}
	}
	return events.APIGatewayProxyRequest{
		Path:                  request.RequestContext.HTTP.Path,
		HTTPMethod:            request.RequestContext.HTTP.Method,
		Headers:               request.Headers,
		QueryStringParameters: request.QueryStringParameters,
		RequestContext: events.APIGatewayProxyRequestContext{
			AccountID:    request.RequestContext.AccountID,
			DomainName:   request.RequestContext.DomainName,
			DomainPrefix: request.RequestContext.DomainPrefix,
			RequestID:    request.RequestContext.RequestID,
			Protocol:     request.RequestContext.HTTP.Protocol,
			ResourcePath: request.RequestContext.HTTP.Path,
			Identity: events.APIGatewayRequestIdentity{
				AccountID: request.RequestContext.AccountID,
				SourceIP:  request.RequestContext.HTTP.SourceIP,
				UserAgent: request.RequestContext.HTTP.UserAgent,
			},
			Path:             request.RequestContext.HTTP.Path,
			HTTPMethod:       request.RequestContext.HTTP.Method,
			RequestTime:      request.RequestContext.Time,
			RequestTimeEpoch: request.RequestContext.TimeEpoch,
			APIID:            request.RequestContext.APIID,
		},
		Body: body,
	}
}
