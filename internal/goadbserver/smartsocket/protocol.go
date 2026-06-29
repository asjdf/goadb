package smartsocket

import (
	"fmt"
	"io"
	"strconv"
)

const MaxProtocolStringLength = 4096

func ReadProtocolString(r io.Reader) (string, error) {
	lengthBytes := make([]byte, 4)
	if _, err := io.ReadFull(r, lengthBytes); err != nil {
		return "", err
	}

	length, err := strconv.ParseUint(string(lengthBytes), 16, 16)
	if err != nil {
		return "", fmt.Errorf("invalid protocol string length %q: %w", string(lengthBytes), err)
	}
	if length > MaxProtocolStringLength {
		return "", fmt.Errorf("protocol string length %d exceeds maximum %d", length, MaxProtocolStringLength)
	}

	payload := make([]byte, int(length))
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", err
	}
	return string(payload), nil
}

func WriteProtocolString(w io.Writer, payload string) error {
	if len(payload) > MaxProtocolStringLength {
		return fmt.Errorf("protocol string length %d exceeds maximum %d", len(payload), MaxProtocolStringLength)
	}
	_, err := fmt.Fprintf(w, "%04x%s", len(payload), payload)
	return err
}

func WriteOKAY(w io.Writer) error {
	_, err := io.WriteString(w, "OKAY")
	return err
}

func WriteFAIL(w io.Writer, message string) error {
	if _, err := io.WriteString(w, "FAIL"); err != nil {
		return err
	}
	return WriteProtocolString(w, message)
}
