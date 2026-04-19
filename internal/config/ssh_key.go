package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// EnsureSSHKey guarantees the server has a per-install SSH keypair to
// inject into every VM it launches. If cfg.VMDefaults.SSHPrivateKey is
// already set AND the file still exists, this is a no-op. Otherwise it
// generates a fresh ed25519 pair in the same directory as configPath
// (so the key lives alongside config.json on the app-state PVC or the
// user's ~/.kubego), points VMDefaults at the private key, mirrors the
// public key into cfg.VMDefaults.SSHPublicKey for convenience, and
// saves the config.
//
// ed25519 is chosen over RSA for brevity (44-byte public key embeds
// cleanly into cloud-init) and because every modern Ubuntu cloud image
// accepts it out of the box.
func EnsureSSHKey(cfg *Config, configPath string, logger *slog.Logger) error {
	if cfg.VMDefaults == nil {
		cfg.VMDefaults = &VMDefaults{CPUs: 2, MemoryMB: 2048, DiskGB: 16}
	}

	privPath := cfg.VMDefaults.SSHPrivateKey
	if privPath != "" {
		if _, err := os.Stat(privPath); err == nil {
			// Refresh cached public key if it's missing or out of date.
			if pub, err := readPublicKey(privPath); err == nil {
				if cfg.VMDefaults.SSHPublicKey != pub {
					cfg.VMDefaults.SSHPublicKey = pub
					if err := cfg.Save(configPath); err != nil {
						return fmt.Errorf("save config after public-key refresh: %w", err)
					}
				}
			}
			return nil
		}
		logger.Warn("configured ssh private key is missing; regenerating", "path", privPath)
	}

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	priv := filepath.Join(dir, "id_kubego_ed25519")
	pub := priv + ".pub"

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ed25519 key: %w", err)
	}
	privPEM, err := ssh.MarshalPrivateKey(privKey, "kubego auto-generated")
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	privBytes := pem.EncodeToMemory(privPEM)
	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return fmt.Errorf("build ssh public key: %w", err)
	}
	pubBytes := append(ssh.MarshalAuthorizedKey(sshPub)[:len(ssh.MarshalAuthorizedKey(sshPub))-1],
		[]byte(" kubego@auto\n")...)

	if err := os.WriteFile(priv, privBytes, 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	if err := os.WriteFile(pub, pubBytes, 0o644); err != nil {
		return fmt.Errorf("write public key: %w", err)
	}

	cfg.VMDefaults.SSHPrivateKey = priv
	cfg.VMDefaults.SSHPublicKey = string(pubBytes)
	if err := cfg.Save(configPath); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	logger.Info("generated kubego ssh keypair", "private", priv, "public", pub)
	return nil
}

// readPublicKey loads the .pub sibling of a private key path and
// returns its text form. Used to refresh VMDefaults.SSHPublicKey when
// it has drifted from what's actually on disk.
func readPublicKey(privPath string) (string, error) {
	data, err := os.ReadFile(privPath + ".pub")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
