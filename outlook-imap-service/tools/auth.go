package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ClientID = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
	Scope    = "openid profile email offline_access https://graph.microsoft.com/Mail.Read"
)

func defaultTokenDir() string {
	if v := strings.TrimSpace(os.Getenv("TOKEN_DIR")); v != "" {
		return v
	}
	return "tokens"
}

func tokenFileFromDir(dir string) string {
	return filepath.Join(dir, "outlook_refresh_token")
}

func writeTokenFile(path string, token string) error {
	if path == "" || token == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}

type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

type TokenResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
}

func main() {
	tokenDir := flag.String("token-dir", defaultTokenDir(), "directory to persist refresh token")
	tokenFile := flag.String("token-file", "", "optional full path to persist refresh token")
	printToken := flag.Bool("print-token", false, "print refresh token to stdout")
	flag.Parse()
	if strings.TrimSpace(*tokenFile) == "" {
		*tokenFile = tokenFileFromDir(*tokenDir)
	}

	fmt.Println("Starting Microsoft OAuth2 Device Flow...")

	// 1. Request device code
	resp, err := http.PostForm("https://login.microsoftonline.com/common/oauth2/v2.0/devicecode", url.Values{
		"client_id": {ClientID},
		"scope":     {Scope},
	})
	if err != nil {
		fmt.Printf("Failed to request device code: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var dcResp DeviceCodeResponse
	json.Unmarshal(body, &dcResp)

	if dcResp.DeviceCode == "" {
		fmt.Printf("Error: %s\n", string(body))
		return
	}

	fmt.Println("======================================================")
	fmt.Println(dcResp.Message)
	fmt.Println("======================================================")
	fmt.Println("Waiting for authorization...")

	// 2. Poll for token
	interval := time.Duration(dcResp.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second
	}

	for {
		time.Sleep(interval)

		tokenReq, _ := http.NewRequest("POST", "https://login.microsoftonline.com/common/oauth2/v2.0/token", strings.NewReader(url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {ClientID},
			"device_code": {dcResp.DeviceCode},
		}.Encode()))
		tokenReq.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		tResp, err := http.DefaultClient.Do(tokenReq)
		if err != nil {
			continue
		}

		tBody, _ := io.ReadAll(tResp.Body)
		tResp.Body.Close()

		var tokenResp TokenResponse
		json.Unmarshal(tBody, &tokenResp)

		if tokenResp.Error == "authorization_pending" {
			fmt.Print(".")
			continue
		} else if tokenResp.Error != "" {
			fmt.Printf("\nError: %s (%s)\n", tokenResp.Error, tokenResp.ErrorDescription)
			return
		}

		fmt.Println("\n\nSUCCESS! Authorization complete.")
		fmt.Println("======================================================")
		if err := writeTokenFile(*tokenFile, tokenResp.RefreshToken); err != nil {
			fmt.Printf("Failed to write refresh token: %v\n", err)
			return
		}
		fmt.Printf("Refresh token written to %s\n", *tokenFile)
		if *printToken {
			fmt.Println()
			fmt.Println(tokenResp.RefreshToken)
		}
		fmt.Println("======================================================")
		break
	}
}
