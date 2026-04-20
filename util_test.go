package adb

import (
	"errors"
	"testing"

	internalErrors "github.com/asjdf/goadb/internal/errors"
	"github.com/stretchr/testify/assert"
)

func TestContainsWhitespaceYes(t *testing.T) {
	assert.True(t, containsWhitespace("hello world"))
}

func TestContainsWhitespaceNo(t *testing.T) {
	assert.False(t, containsWhitespace("hello"))
}

func TestIsBlankWhenEmpty(t *testing.T) {
	assert.True(t, isBlank(""))
}

func TestIsBlankWhenJustWhitespace(t *testing.T) {
	assert.True(t, isBlank(" \t"))
}

func TestIsBlankNo(t *testing.T) {
	assert.False(t, isBlank("     h   "))
}

func TestWrapClientErrorWrapsGenericErrorsAsNetworkError(t *testing.T) {
	client := &Device{}
	cause := errors.New("boom")

	err := wrapClientError(cause, client, "RunCommand")

	assert.Error(t, err)
	assert.True(t, HasErrCode(err, NetworkError))
	assert.Equal(t, cause, err.(*internalErrors.Err).Cause)
}
