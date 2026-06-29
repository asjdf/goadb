package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
)

const TokenSize = 20

type KeyPair struct {
	PrivateKey *rsa.PrivateKey
	PublicKey  string
}

func LoadKeyPair(privateKeyPath string) (*KeyPair, error) {
	privateBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key %q: %w", privateKeyPath, err)
	}
	privateKey, err := parsePrivateKey(privateBytes)
	if err != nil {
		return nil, err
	}

	publicBytes, err := os.ReadFile(privateKeyPath + ".pub")
	if err != nil {
		return nil, fmt.Errorf("read public key %q: %w", privateKeyPath+".pub", err)
	}

	return &KeyPair{
		PrivateKey: privateKey,
		PublicKey:  strings.TrimSpace(string(publicBytes)),
	}, nil
}

func (k *KeyPair) SignToken(token []byte) ([]byte, error) {
	if len(token) != TokenSize {
		return nil, fmt.Errorf("adb auth token length %d, want %d", len(token), TokenSize)
	}
	return rsa.SignPKCS1v15(rand.Reader, k.PrivateKey, crypto.SHA1, token)
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("private key is not PEM encoded")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is %T, want *rsa.PrivateKey", parsed)
	}
	return key, nil
}
