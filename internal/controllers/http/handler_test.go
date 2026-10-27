package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	for _, tc := range []struct {
		name       string
		path       string
		dbErr      error
		wantStatus int
	}{
		{"live does not depend on database", "/health/live", errors.New("offline"), http.StatusOK},
		{"ready with database", "/health/ready", nil, http.StatusOK},
		{"not ready without database", "/health/ready", errors.New("offline"), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewHandler(func(ctx context.Context) error {
				return tc.dbErr
			}, nil, nil, slog.Default())
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tc.wantStatus)
			}
		})
	}
}
