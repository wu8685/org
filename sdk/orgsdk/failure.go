package orgsdk

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.temporal.io/sdk/temporal"
)

const safeUserFailureType = "org.sdk/user-error"

var safeFailureCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type Failure struct {
	Code          string    `json:"code"`
	Message       string    `json:"message"`
	RuntimeNodeID string    `json:"runtimeNodeId,omitempty"`
	TemplateID    string    `json:"templateId,omitempty"`
	NodeLabel     string    `json:"nodeLabel,omitempty"`
	OccurredAt    time.Time `json:"occurredAt"`
}

type userError struct {
	code    string
	message string
	valid   bool
}

func (e *userError) Error() string {
	if e.valid {
		return e.message
	}
	return "activity failed"
}

func NewUserError(code, message string) error {
	code, message, valid := normalizeFailureText(code, message)
	return &userError{code: code, message: message, valid: valid}
}

type safeFailurePayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func encodeUserError(err error) error {
	var declared *userError
	if !errors.As(err, &declared) {
		return err
	}
	payload := genericActivityFailure()
	if declared.valid {
		payload = safeFailurePayload{Code: declared.code, Message: declared.message}
	}
	return temporal.NewNonRetryableApplicationError(payload.Message, safeUserFailureType, nil, payload)
}

func decodeActivityFailure(err error) safeFailurePayload {
	fallback := genericActivityFailure()
	var applicationError *temporal.ApplicationError
	if !errors.As(err, &applicationError) || applicationError.Type() != safeUserFailureType || !applicationError.HasDetails() {
		return fallback
	}
	var payload safeFailurePayload
	if detailsErr := applicationError.Details(&payload); detailsErr != nil {
		return fallback
	}
	code, message, valid := normalizeFailureText(payload.Code, payload.Message)
	if !valid {
		return fallback
	}
	return safeFailurePayload{Code: code, Message: message}
}

func genericActivityFailure() safeFailurePayload {
	return safeFailurePayload{Code: "activity_failed", Message: "Activity failed. Open advanced diagnostics if authorized."}
}

func genericWorkflowFailure() safeFailurePayload {
	return safeFailurePayload{Code: "workflow_failed", Message: "Workflow failed. Open advanced diagnostics if authorized."}
}

func normalizeFailureText(code, message string) (string, string, bool) {
	code, message = strings.TrimSpace(code), strings.TrimSpace(message)
	if !safeFailureCodePattern.MatchString(code) || message == "" || utf8.RuneCountInString(message) > 300 || strings.Count(message, "\n") > 3 {
		return "", "", false
	}
	for _, value := range message {
		if unicode.IsControl(value) && value != '\n' {
			return "", "", false
		}
	}
	return code, message, true
}
