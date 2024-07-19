package retry

import (
	"fmt"
)

type Config[T any] struct {
	Action                 func() (T, error)
	MaxRetries             int
	AttemptErrorCallback   func(int, error)
	NoMoreAttemptsCallback func(error)
}

func With[T any](in Config[T]) (*T, error) {
	if in.Action == nil {
		return nil, fmt.Errorf("action is nil")
	}
	var res T
	var err error
	for attempt := 1; attempt <= in.MaxRetries; attempt++ {
		res, err = in.Action()
		if err == nil {
			return &res, nil
		}
		if in.AttemptErrorCallback != nil {
			in.AttemptErrorCallback(attempt, err)
		}
		if attempt >= in.MaxRetries {
			if in.NoMoreAttemptsCallback != nil {
				in.NoMoreAttemptsCallback(err)
			}
			return nil, err
		}
	}
	return &res, nil
}
