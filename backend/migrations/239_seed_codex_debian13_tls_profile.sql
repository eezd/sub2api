-- Seed the versioned TLS ClientHello captured from the official Codex CLI.
-- Evidence: internal/pkg/tlsfingerprint/testdata/codex-debian13-x86_64-0.152.0.json

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

INSERT INTO tls_fingerprint_profiles (
    name,
    description,
    enable_grease,
    cipher_suites,
    curves,
    point_formats,
    signature_algorithms,
    alpn_protocols,
    supported_versions,
    key_share_groups,
    psk_modes,
    extensions
) VALUES (
    'Codex CLI 0.152.0 / Debian 13 / x86_64',
    'Official @openai/codex 0.152.0 ClientHello captured on Debian 13.4 x86_64 on 2026-09-19; JA3 0b85eb0d4981e69064e40753e4f0ac5f; no ALPN offered.',
    false,
    '[4866,4867,4865,49196,49200,159,52393,52392,52394,49195,49199,158,49188,49192,107,49187,49191,103,49162,49172,57,49161,49171,51,157,156,61,60,53,47]'::jsonb,
    '[4588,29,23,30,24,25,256,257]'::jsonb,
    '[0]'::jsonb,
    '[2309,2310,2308,1027,1283,1539,2055,2056,2074,2075,2076,2057,2058,2059,2052,2053,2054,1025,1281,1537,771,769,770,1026,1282,1538]'::jsonb,
    '[]'::jsonb,
    '[772,771]'::jsonb,
    '[4588,29]'::jsonb,
    '[1]'::jsonb,
    '[65281,0,11,10,35,22,23,13,43,45,51]'::jsonb
)
ON CONFLICT (name) DO UPDATE SET
    description = EXCLUDED.description,
    enable_grease = EXCLUDED.enable_grease,
    cipher_suites = EXCLUDED.cipher_suites,
    curves = EXCLUDED.curves,
    point_formats = EXCLUDED.point_formats,
    signature_algorithms = EXCLUDED.signature_algorithms,
    alpn_protocols = EXCLUDED.alpn_protocols,
    supported_versions = EXCLUDED.supported_versions,
    key_share_groups = EXCLUDED.key_share_groups,
    psk_modes = EXCLUDED.psk_modes,
    extensions = EXCLUDED.extensions,
    updated_at = NOW();
