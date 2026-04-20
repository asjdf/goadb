package adb

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/asjdf/goadb/internal/errors"
)

var (
	whitespaceRegex = regexp.MustCompile(`^\s*$`)
)

func containsWhitespace(str string) bool {
	return strings.ContainsAny(str, " \t\v")
}

func isBlank(str string) bool {
	return whitespaceRegex.MatchString(str)
}

func wrapClientError(err error, client interface{}, operation string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	clientType := reflect.TypeOf(client)
	message := fmt.Sprintf("error performing %s on %s", fmt.Sprintf(operation, args...), clientType)

	if wrappedErr, ok := err.(*errors.Err); ok {
		return &errors.Err{
			Code:    wrappedErr.Code,
			Cause:   err,
			Message: message,
			Details: client,
		}
	}

	return &errors.Err{
		Code:    errors.NetworkError,
		Cause:   err,
		Message: message,
		Details: client,
	}
}
