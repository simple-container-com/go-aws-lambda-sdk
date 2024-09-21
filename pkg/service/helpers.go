package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pkg/errors"
)

func WithReadBody[T any, R any](ctx context.Context, s Service, c HttpAdapter, action string, callback func(cfg *T) (*R, error)) (*R, bool) {
	var err error
	var res *R
	if model, success := ReadBody[T](ctx, s, c); success {
		res, err = callback(model)
		if err != nil {
			c.JSON(http.StatusInternalServerError, Error{
				Message: fmt.Sprintf("failed to %s: %v", action, err),
				Meta:    s.GetMeta(ctx),
			})
			return res, false
		}
	}
	return res, true
}

func ReadBody[T any](ctx context.Context, s Service, c HttpAdapter) (*T, bool) {
	var runConfig T
	bodyBytes := ReadBytes(c.RequestBody())
	if err := json.Unmarshal(bodyBytes, &runConfig); err != nil {
		if s.IsRequestDebugEnabled() {
			s.Logger().Errorf(ctx, "Failed to unmarshal request body: %v, got body: %q", err, string(bodyBytes))
		} else {
			s.Logger().Errorf(ctx, "Failed to unmarshal request body: %v", err)
		}
		c.JSON(500, Error{
			Message: errors.Wrapf(err, "failed to unmarshal request body to Config").Error(),
		})
		return nil, false
	}
	return &runConfig, true
}
