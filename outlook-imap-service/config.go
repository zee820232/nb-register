package main

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultTokenDir  = "tokens"
	refreshTokenName = "outlook_refresh_token"
)

type Config struct {
	RefreshToken     string
	TokenDir         string
	RefreshTokenFile string
	ListenAddr       string
}

func LoadConfig() *Config {
	tokenDir := strings.TrimSpace(os.Getenv("TOKEN_DIR"))
	if tokenDir == "" {
		tokenDir = defaultTokenDir
	}
	refreshTokenFile := filepath.Join(tokenDir, refreshTokenName)
	refreshToken := readRefreshTokenFile(refreshTokenFile)

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":50051"
	}

	return &Config{
		RefreshToken:     refreshToken,
		TokenDir:         tokenDir,
		RefreshTokenFile: refreshTokenFile,
		ListenAddr:       listenAddr,
	}
}

func readRefreshTokenFile(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
