package model

import "testing"

func TestTLSFingerprintProfileValidateRejectsHTTP2ALPN(t *testing.T) {
	profile := &TLSFingerprintProfile{
		Name:          "browser-h2",
		ALPNProtocols: []string{"h2", "http/1.1"},
	}

	err := profile.Validate()
	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("Validate() error = %T %v, want *ValidationError", err, err)
	}
	if validationErr.Field != "alpn_protocols" {
		t.Fatalf("Validate() field = %q, want alpn_protocols", validationErr.Field)
	}
}

func TestTLSFingerprintProfileValidateAllowsHTTP1ALPN(t *testing.T) {
	profile := &TLSFingerprintProfile{
		Name:          "codex-http1",
		ALPNProtocols: []string{"http/1.1"},
	}

	if err := profile.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestTLSFingerprintProfileValidateRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name    string
		profile *TLSFingerprintProfile
		field   string
	}{
		{name: "blank name", profile: &TLSFingerprintProfile{Name: "   "}, field: "name"},
		{name: "TLS 1.0", profile: &TLSFingerprintProfile{Name: "weak", SupportedVersions: []uint16{0x0301}}, field: "supported_versions"},
		{name: "arbitrary ALPN", profile: &TLSFingerprintProfile{Name: "bad-alpn", ALPNProtocols: []string{"acme-secret-protocol"}}, field: "alpn_protocols"},
		{name: "PSK without forward secrecy", profile: &TLSFingerprintProfile{Name: "weak-psk", PSKModes: []uint16{0}}, field: "psk_modes"},
		{name: "unsupported key share", profile: &TLSFingerprintProfile{Name: "bad-key-share", KeyShareGroups: []uint16{1}}, field: "key_share_groups"},
		{name: "duplicate cipher", profile: &TLSFingerprintProfile{Name: "duplicate", CipherSuites: []uint16{0x1301, 0x1301}}, field: "cipher_suites"},
		{name: "unsupported extension", profile: &TLSFingerprintProfile{Name: "bad-extension", Extensions: []uint16{0, 12345}}, field: "extensions"},
		{name: "missing SNI", profile: &TLSFingerprintProfile{Name: "no-sni", Extensions: []uint16{10, 43}}, field: "extensions"},
		{name: "oversized list", profile: &TLSFingerprintProfile{Name: "large", CipherSuites: make([]uint16, maxTLSFingerprintProfileListValues+1)}, field: "cipher_suites"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.profile.Validate()
			validationErr, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("Validate() error = %T %v, want *ValidationError", err, err)
			}
			if validationErr.Field != test.field {
				t.Fatalf("Validate() field = %q, want %q", validationErr.Field, test.field)
			}
		})
	}
}

func TestTLSFingerprintProfileValidateAllowsCapturedSecureProfile(t *testing.T) {
	description := "captured ClientHello"
	profile := &TLSFingerprintProfile{
		Name:                "Codex secure profile",
		Description:         &description,
		CipherSuites:        []uint16{4866, 4867, 4865, 49196},
		Curves:              []uint16{4588, 29, 23, 30, 24, 25, 256, 257},
		PointFormats:        []uint16{0},
		SignatureAlgorithms: []uint16{2309, 2310, 2308, 1027},
		SupportedVersions:   []uint16{0x0304, 0x0303},
		KeyShareGroups:      []uint16{4588, 29},
		PSKModes:            []uint16{1},
		Extensions:          []uint16{65281, 0, 11, 10, 35, 22, 23, 13, 43, 45, 51},
	}

	if err := profile.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
