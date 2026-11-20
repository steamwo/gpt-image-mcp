package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

type InputError struct {
	Parameter string
	Message   string
}

func (e *InputError) Error() string {
	if e.Parameter == "" {
		return e.Message
	}
	return e.Parameter + ": " + e.Message
}

type APIError struct {
	Status  int
	Message string
	Type    string
	Code    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("image API returned HTTP %d: %s", e.Status, e.Message)
}

type ToolErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Parameter string `json:"parameter,omitempty"`
	Status    int    `json:"status,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

func classifyToolError(err error) ToolErrorDetail {
	if err == nil {
		return ToolErrorDetail{Code: "unknown_error", Message: "unknown error"}
	}

	var inputErr *InputError
	if errors.As(err, &inputErr) {
		return ToolErrorDetail{
			Code:      "invalid_input",
			Message:   inputErr.Message,
			Parameter: inputErr.Parameter,
			Hint:      "Correct the input and call the tool again.",
		}
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		detail := ToolErrorDetail{Message: apiErr.Message, Status: apiErr.Status}
		switch apiErr.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			detail.Code = "authentication_failed"
			detail.Hint = "Check the configured API key or custom authentication headers."
		case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
			detail.Code = "provider_busy"
			detail.Retryable = true
			detail.Hint = "Retry the request later."
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			detail.Code = "invalid_request"
			detail.Hint = "Adjust the request or choose a provider profile matching the configured endpoint."
		default:
			if apiErr.Status >= 500 {
				detail.Code = "provider_error"
				detail.Retryable = true
				detail.Hint = "Retry the request later."
			} else {
				detail.Code = "provider_error"
			}
		}
		return detail
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return ToolErrorDetail{Code: "timeout", Message: err.Error(), Retryable: true, Hint: "Retry, or increase GPT_IMAGE_TIMEOUT."}
	}
	if errors.Is(err, context.Canceled) {
		return ToolErrorDetail{Code: "canceled", Message: err.Error(), Retryable: true}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return ToolErrorDetail{Code: "network_error", Message: err.Error(), Retryable: true, Hint: "Check the provider endpoint and network connection, then retry."}
	}

	message := err.Error()
	if strings.Contains(message, "outside GPT_IMAGE_ALLOWED_DIRS") || strings.Contains(message, "private or local network") {
		return ToolErrorDetail{Code: "access_denied", Message: message, Hint: "Use an allowed image source or update the MCP security configuration."}
	}

	return ToolErrorDetail{Code: "internal_error", Message: message}
}
