package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

type TokenResponse struct {
	Count int    `json:"count"`
	Value string `json:"value"`
}

const (
	EnvTokenRequestURL   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	EnvTokenRequestToken = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

var ErrNotGHA = errors.New("missing ACTIONS_* environment variables. Not inside github actions")

// Token returns the GitHub Actions ID token from the current GHA context
// It returns ErrNotGHA if it is not running in a GitHub Actions environment.
func Token(aud string) (string, error) {
	idTokenRequestURL := os.Getenv(EnvTokenRequestURL)
	idTokenRequestToken := os.Getenv(EnvTokenRequestToken)
	if idTokenRequestURL == "" {
		return "", ErrNotGHA
	}
	targetURL := fmt.Sprintf("%s&audience=%s", idTokenRequestURL, aud)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", idTokenRequestToken))
	req.Header.Add("User-Agent", "actions/oidc-client")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	var tokenRes TokenResponse
	err = json.Unmarshal(body, &tokenRes)
	if err != nil {
		return "", err
	}
	return tokenRes.Value, nil
}
