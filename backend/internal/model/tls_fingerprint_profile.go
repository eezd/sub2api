// Package model 定义服务层使用的数据模型。
package model

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// TLSFingerprintProfile TLS 指纹配置模板
// 包含完整的 ClientHello 参数，用于模拟特定客户端的 TLS 握手特征
type TLSFingerprintProfile struct {
	ID                  int64     `json:"id"`
	Name                string    `json:"name"`
	Description         *string   `json:"description"`
	EnableGREASE        bool      `json:"enable_grease"`
	CipherSuites        []uint16  `json:"cipher_suites"`
	Curves              []uint16  `json:"curves"`
	PointFormats        []uint16  `json:"point_formats"`
	SignatureAlgorithms []uint16  `json:"signature_algorithms"`
	ALPNProtocols       []string  `json:"alpn_protocols"`
	SupportedVersions   []uint16  `json:"supported_versions"`
	KeyShareGroups      []uint16  `json:"key_share_groups"`
	PSKModes            []uint16  `json:"psk_modes"`
	Extensions          []uint16  `json:"extensions"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

const (
	maxTLSFingerprintProfileNameRunes        = 100
	maxTLSFingerprintProfileDescriptionRunes = 4096
	maxTLSFingerprintProfileListValues       = 64
)

// Validate verifies that a profile is bounded and can produce a supported,
// TLS 1.2+ HTTP/1.1 ClientHello. Empty slices retain the built-in defaults.
func (p *TLSFingerprintProfile) Validate() error {
	if p == nil {
		return &ValidationError{Field: "profile", Message: "profile is required"}
	}
	if strings.TrimSpace(p.Name) == "" {
		return &ValidationError{Field: "name", Message: "name is required"}
	}
	if utf8.RuneCountInString(p.Name) > maxTLSFingerprintProfileNameRunes {
		return &ValidationError{Field: "name", Message: fmt.Sprintf("name must not exceed %d characters", maxTLSFingerprintProfileNameRunes)}
	}
	if p.Description != nil && utf8.RuneCountInString(*p.Description) > maxTLSFingerprintProfileDescriptionRunes {
		return &ValidationError{Field: "description", Message: fmt.Sprintf("description must not exceed %d characters", maxTLSFingerprintProfileDescriptionRunes)}
	}

	if err := validateTLSProfileUint16List("cipher_suites", p.CipherSuites, maxTLSFingerprintProfileListValues, func(value uint16) bool { return value != 0 }); err != nil {
		return err
	}
	if err := validateTLSProfileUint16List("curves", p.Curves, 32, func(value uint16) bool { return value != 0 }); err != nil {
		return err
	}
	if err := validateTLSProfileUint16List("point_formats", p.PointFormats, 3, func(value uint16) bool { return value <= 2 }); err != nil {
		return err
	}
	if err := validateTLSProfileUint16List("signature_algorithms", p.SignatureAlgorithms, maxTLSFingerprintProfileListValues, func(value uint16) bool { return value != 0 }); err != nil {
		return err
	}
	if err := validateTLSProfileALPN(p.ALPNProtocols); err != nil {
		return err
	}
	if err := validateTLSProfileUint16List("supported_versions", p.SupportedVersions, 2, func(value uint16) bool {
		return value == 0x0303 || value == 0x0304
	}); err != nil {
		return err
	}
	if err := validateTLSProfileUint16List("key_share_groups", p.KeyShareGroups, 8, isSupportedTLSKeyShareGroup); err != nil {
		return err
	}
	if err := validateTLSProfileUint16List("psk_modes", p.PSKModes, 1, func(value uint16) bool { return value == 1 }); err != nil {
		return err
	}
	if err := validateTLSProfileUint16List("extensions", p.Extensions, maxTLSFingerprintProfileListValues, isSupportedTLSProfileExtension); err != nil {
		return err
	}
	if len(p.Extensions) > 0 && !containsTLSProfileValue(p.Extensions, 0) {
		return &ValidationError{Field: "extensions", Message: "server_name extension 0 is required"}
	}
	return nil
}

func validateTLSProfileALPN(protocols []string) error {
	if len(protocols) == 0 {
		return nil
	}
	if len(protocols) != 1 || protocols[0] != "http/1.1" {
		return &ValidationError{Field: "alpn_protocols", Message: "only http/1.1 ALPN is supported by the current TLS ClientHello transport"}
	}
	return nil
}

func validateTLSProfileUint16List(field string, values []uint16, maxValues int, allowed func(uint16) bool) error {
	if len(values) > maxValues {
		return &ValidationError{Field: field, Message: fmt.Sprintf("must not contain more than %d values", maxValues)}
	}
	for index, value := range values {
		if !allowed(value) {
			return &ValidationError{Field: field, Message: fmt.Sprintf("unsupported value %d", value)}
		}
		for previous := range index {
			if values[previous] == value {
				return &ValidationError{Field: field, Message: fmt.Sprintf("duplicate value %d", value)}
			}
		}
	}
	return nil
}

func isSupportedTLSKeyShareGroup(value uint16) bool {
	switch value {
	case 23, 24, 25, 29, 4588:
		return true
	default:
		return false
	}
}

func isSupportedTLSProfileExtension(value uint16) bool {
	if value&0x0f0f == 0x0a0a && value>>8 == value&0xff {
		return true
	}
	switch value {
	case 0, 5, 10, 11, 13, 16, 18, 22, 23, 35, 43, 45, 50, 51, 65037, 65281:
		return true
	default:
		return false
	}
}

func containsTLSProfileValue(values []uint16, expected uint16) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// ToTLSProfile 将领域模型转换为运行时使用的 tlsfingerprint.Profile
// 空切片字段会在 dialer 中 fallback 到内置默认值
func (p *TLSFingerprintProfile) ToTLSProfile() *tlsfingerprint.Profile {
	return &tlsfingerprint.Profile{
		Name:                p.Name,
		EnableGREASE:        p.EnableGREASE,
		CipherSuites:        p.CipherSuites,
		Curves:              p.Curves,
		PointFormats:        p.PointFormats,
		SignatureAlgorithms: p.SignatureAlgorithms,
		ALPNProtocols:       p.ALPNProtocols,
		SupportedVersions:   p.SupportedVersions,
		KeyShareGroups:      p.KeyShareGroups,
		PSKModes:            p.PSKModes,
		Extensions:          p.Extensions,
	}
}
