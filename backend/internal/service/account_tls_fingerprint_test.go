package service

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestTLSFingerprintEligibilityIsExplicitAndIndependentFromCodexIdentity(t *testing.T) {
	enabled := map[string]any{
		"enable_tls_fingerprint":     true,
		"tls_fingerprint_profile_id": 42,
	}
	openAIOAuth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: enabled}
	require.True(t, openAIOAuth.SupportsTLSFingerprintProfile())
	require.True(t, openAIOAuth.IsTLSFingerprintEnabled())

	openAISetup := &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Extra: enabled}
	require.True(t, openAISetup.IsTLSFingerprintEnabled())

	applicationOnly := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		codexFingerprintModeExtraKey: "full",
		codexFingerprintSeedExtraKey: testCodexFingerprintSeed,
	}}
	require.Equal(t, codexFingerprintFull, applicationOnly.GetCodexFingerprintMode())
	require.False(t, applicationOnly.IsTLSFingerprintEnabled())

	tlsOnly := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: enabled}
	require.Equal(t, codexFingerprintOff, tlsOnly.GetCodexFingerprintMode())
	require.True(t, tlsOnly.IsTLSFingerprintEnabled())

	apiKey := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: enabled}
	require.True(t, apiKey.SupportsTLSFingerprintProfile())
	require.True(t, apiKey.IsTLSFingerprintEnabled())
}

func TestOpenAIWSHandshakeCompatibilityIncludesTLSProfile(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	headers := http.Header{"OpenAI-Beta": {"responses_websockets=2026-02-06"}}
	first := &tlsfingerprint.Profile{Name: "same display name", CipherSuites: []uint16{0x1301}}
	second := &tlsfingerprint.Profile{Name: "same display name", CipherSuites: []uint16{0x1302}}

	firstKey := normalizeOpenAIWSHandshakeCompatibility(account, headers, first)
	secondKey := normalizeOpenAIWSHandshakeCompatibility(account, headers, second)
	require.NotEqual(t, firstKey, secondKey)
	require.Equal(t, firstKey, normalizeOpenAIWSHandshakeCompatibility(account, headers, first))
}
