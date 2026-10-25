package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/crypto/ssh"
)

// generateHostKey makes a new gateway host key; tests replace it.
var generateHostKey = func() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return priv, err
}

// loadOrCreateHostKey reads the gateway's SSH host key from path. When the
// file does not exist it generates an ed25519 key and writes it there (0600,
// OpenSSH format, plus path+".pub"), so a host without ssh-keygen at package
// install still starts. created reports whether a new key was written.
func loadOrCreateHostKey(path string) (signer ssh.Signer, created bool, err error) {
	privateBytes, err := os.ReadFile(path)
	if err == nil {
		signer, err = ssh.ParsePrivateKey(privateBytes)
		if err != nil {
			return nil, false, fmt.Errorf("failed to parse host key: %w", err)
		}
		return signer, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, fmt.Errorf("failed to read host key: %w", err)
	}

	priv, err := generateHostKey()
	if err != nil {
		return nil, false, fmt.Errorf("generate host key: %w", err)
	}
	signer, err = ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, false, fmt.Errorf("load generated host key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "orion-belt-host")
	if err != nil {
		return nil, false, fmt.Errorf("marshal host key: %w", err)
	}

	// O_EXCL: if another process created the key meanwhile, keep theirs.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, fs.ErrExist) {
		return loadOrCreateHostKey(path)
	}
	if err != nil {
		return nil, false, fmt.Errorf("create host key: %w", err)
	}
	_, werr := f.Write(pem.EncodeToMemory(block))
	if err := errors.Join(werr, f.Close()); err != nil {
		os.Remove(path) // never leave a half-written key behind
		return nil, false, fmt.Errorf("write host key: %w", err)
	}

	pub := ssh.MarshalAuthorizedKey(signer.PublicKey())
	pub = append(pub[:len(pub)-1], []byte(" orion-belt-host\n")...)
	if err := os.WriteFile(path+".pub", pub, 0644); err != nil {
		return nil, false, fmt.Errorf("write host public key: %w", err)
	}
	return signer, true, nil
}
