package worker

import (
	"errors"
	"strings"
)

func Process(input Input) (Result, error) {
	value := strings.TrimSpace(input.Value)
	if value == "" {
		return Result{}, errors.New("value is required")
	}
	return Result{Value: value}, nil
}
