package tts

import (
	"context"
	"os"

	"golang.org/x/oauth2/google"
)

// GoogleServiceAccountToken lê GOOGLE_APPLICATION_CREDENTIALS (JSON de service
// account) e devolve um gerador de token OAuth para a API de TTS.
func GoogleServiceAccountToken(ctx context.Context, path string) (func(context.Context) (string, error), error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	creds, err := google.CredentialsFromJSON(ctx, b, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, err
	}
	return func(context.Context) (string, error) {
		t, err := creds.TokenSource.Token()
		if err != nil {
			return "", err
		}
		return t.AccessToken, nil
	}, nil
}
