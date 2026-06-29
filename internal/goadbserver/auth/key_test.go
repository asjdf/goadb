package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadKeyPairReadsPrivateKeyAndPublicKeyFile(t *testing.T) {
	privatePath, privateKey := writeTestKeyPair(t, "adb-public-key material")

	keyPair, err := LoadKeyPair(privatePath)
	if err != nil {
		t.Fatalf("LoadKeyPair returned error: %v", err)
	}
	if keyPair.PublicKey != "adb-public-key material" {
		t.Fatalf("PublicKey = %q, want public key file contents", keyPair.PublicKey)
	}
	if keyPair.PrivateKey.N.Cmp(privateKey.N) != 0 {
		t.Fatal("loaded private key modulus does not match fixture")
	}
}

func TestSignTokenSignsTwentyByteAuthTokenWithRSASHA1(t *testing.T) {
	privatePath, privateKey := writeTestKeyPair(t, "pub")
	keyPair, err := LoadKeyPair(privatePath)
	if err != nil {
		t.Fatalf("LoadKeyPair returned error: %v", err)
	}

	token := []byte("12345678901234567890")
	signature, err := keyPair.SignToken(token)
	if err != nil {
		t.Fatalf("SignToken returned error: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(&privateKey.PublicKey, crypto.SHA1, token, signature); err != nil {
		t.Fatalf("signature did not verify as RSA/SHA1 over token: %v", err)
	}
}

func TestSignTokenRejectsNonTwentyByteToken(t *testing.T) {
	privatePath, _ := writeTestKeyPair(t, "pub")
	keyPair, err := LoadKeyPair(privatePath)
	if err != nil {
		t.Fatalf("LoadKeyPair returned error: %v", err)
	}

	if _, err := keyPair.SignToken([]byte("short")); err == nil {
		t.Fatal("SignToken succeeded, want token length error")
	}
}

func TestLoadKeyPairRejectsMissingPublicKeyFile(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	privatePath := filepath.Join(dir, "adbkey")
	writePEMPrivateKey(t, privatePath, key)

	if _, err := LoadKeyPair(privatePath); err == nil {
		t.Fatal("LoadKeyPair succeeded, want missing public key error")
	}
}

func writeTestKeyPair(t *testing.T, publicKey string) (string, *rsa.PrivateKey) {
	t.Helper()
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	privatePath := filepath.Join(dir, "adbkey")
	writePEMPrivateKey(t, privatePath, key)
	if err := os.WriteFile(privatePath+".pub", []byte(publicKey), 0o600); err != nil {
		t.Fatalf("WriteFile public key returned error: %v", err)
	}
	return privatePath, key
}

func writePEMPrivateKey(t *testing.T, path string, key *rsa.PrivateKey) {
	t.Helper()
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("WriteFile private key returned error: %v", err)
	}
}
