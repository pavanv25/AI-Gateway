package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsRetriable_Nil(t *testing.T) {
	if IsRetriable(nil) {
		t.Error("nil error should not be retriable")
	}
}

func TestIsRetriable_ContextCanceled(t *testing.T) {
	if IsRetriable(context.Canceled) {
		t.Error("context.Canceled should not be retriable")
	}
}

func TestIsRetriable_ContextDeadlineExceeded(t *testing.T) {
	if IsRetriable(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded should not be retriable")
	}
}

func TestIsRetriable_ContextCanceled_Wrapped(t *testing.T) {
	err := fmt.Errorf("outer: %w", context.Canceled)
	if IsRetriable(err) {
		t.Error("wrapped context.Canceled should not be retriable")
	}
}

func TestIsRetriable_ProviderError_429(t *testing.T) {
	err := &ProviderError{StatusCode: 429, Cause: errors.New("rate limited")}
	if !IsRetriable(err) {
		t.Error("429 should be retriable")
	}
}

func TestIsRetriable_ProviderError_500(t *testing.T) {
	err := &ProviderError{StatusCode: 500, Cause: errors.New("server error")}
	if !IsRetriable(err) {
		t.Error("500 should be retriable")
	}
}

func TestIsRetriable_ProviderError_503(t *testing.T) {
	err := &ProviderError{StatusCode: 503, Cause: errors.New("unavailable")}
	if !IsRetriable(err) {
		t.Error("503 should be retriable")
	}
}

func TestIsRetriable_ProviderError_400(t *testing.T) {
	err := &ProviderError{StatusCode: 400, Cause: errors.New("bad request")}
	if IsRetriable(err) {
		t.Error("400 should not be retriable")
	}
}

func TestIsRetriable_ProviderError_401(t *testing.T) {
	err := &ProviderError{StatusCode: 401, Cause: errors.New("unauthorized")}
	if IsRetriable(err) {
		t.Error("401 should not be retriable")
	}
}

func TestIsRetriable_ProviderError_404(t *testing.T) {
	err := &ProviderError{StatusCode: 404, Cause: errors.New("not found")}
	if IsRetriable(err) {
		t.Error("404 should not be retriable")
	}
}

func TestIsRetriable_UnknownError(t *testing.T) {
	err := errors.New("connection refused")
	if !IsRetriable(err) {
		t.Error("unknown/network error should be retriable")
	}
}

func TestProviderError_Unwrap(t *testing.T) {
	cause := errors.New("original")
	pe := &ProviderError{StatusCode: 500, Cause: cause}
	if !errors.Is(pe, cause) {
		t.Error("errors.Is should traverse through ProviderError.Unwrap")
	}
}

func TestProviderError_WrappedInFmtErrorf(t *testing.T) {
	inner := &ProviderError{StatusCode: 503, Cause: errors.New("svc unavail")}
	wrapped := fmt.Errorf("provider call: %w", inner)
	if !IsRetriable(wrapped) {
		t.Error("ProviderError wrapped in fmt.Errorf should still be retriable")
	}
}
