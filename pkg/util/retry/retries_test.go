package retry

import (
	"fmt"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
)

func TestWithRetries(t *testing.T) {
	tests := []struct {
		name               string
		action             func() (string, error)
		maxRetries         int
		wantAttempts       int
		wantFailedAttempts int
		wantReports        int
		wantErr            string
		wantRes            *string
	}{
		{
			name: "should not retry if no error occurs",
			action: func() (string, error) {
				return "happy", nil
			},
			maxRetries:         2,
			wantAttempts:       1,
			wantFailedAttempts: 0,
			wantReports:        0,
			wantRes:            lo.ToPtr("happy"),
		},
		{
			name: "should retry exactly 2 times and return error",
			action: func() (string, error) {
				return "error", fmt.Errorf("some error")
			},
			wantErr:            "some error",
			maxRetries:         2,
			wantAttempts:       2,
			wantFailedAttempts: 2,
			wantReports:        1,
			wantRes:            nil,
		},
		{
			name: "should retry exactly 3 times and return error",
			action: func() (string, error) {
				return "error", fmt.Errorf("some error")
			},
			maxRetries:         3,
			wantErr:            "some error",
			wantAttempts:       3,
			wantFailedAttempts: 3,
			wantReports:        1,
			wantRes:            nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempted := 0
			failedAttempted := 0
			reported := 0
			res, err := With[string](Config[string]{
				Action: func() (string, error) {
					attempted++
					return tt.action()
				},
				MaxRetries: tt.maxRetries,
				AttemptErrorCallback: func(attempt int, err error) {
					failedAttempted++
				},
				NoMoreAttemptsCallback: func(err error) {
					reported++
				},
			})
			if tt.wantErr != "" {
				assert.Error(t, err, tt.wantErr)
			}
			assert.Equal(t, tt.wantRes, res)
			assert.Equal(t, tt.wantAttempts, attempted)
			assert.Equal(t, tt.wantFailedAttempts, failedAttempted)
			assert.Equal(t, tt.wantReports, reported)
		})
	}
}
