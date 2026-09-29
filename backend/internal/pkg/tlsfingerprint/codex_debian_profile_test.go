package tlsfingerprint

import (
	"testing"

	utls "github.com/refraction-networking/utls"
	"github.com/stretchr/testify/require"
)

func codexDebianProfileForTest() *Profile {
	return &Profile{
		Name:                "Codex CLI 0.152.0 / Debian 13 / x86_64",
		CipherSuites:        []uint16{4866, 4867, 4865, 49196, 49200, 159, 52393, 52392, 52394, 49195, 49199, 158, 49188, 49192, 107, 49187, 49191, 103, 49162, 49172, 57, 49161, 49171, 51, 157, 156, 61, 60, 53, 47},
		Curves:              []uint16{4588, 29, 23, 30, 24, 25, 256, 257},
		PointFormats:        []uint16{0},
		SignatureAlgorithms: []uint16{2309, 2310, 2308, 1027, 1283, 1539, 2055, 2056, 2074, 2075, 2076, 2057, 2058, 2059, 2052, 2053, 2054, 1025, 1281, 1537, 771, 769, 770, 1026, 1282, 1538},
		SupportedVersions:   []uint16{772, 771},
		KeyShareGroups:      []uint16{4588, 29},
		PSKModes:            []uint16{1},
		Extensions:          []uint16{65281, 0, 11, 10, 35, 22, 23, 13, 43, 45, 51},
	}
}

func TestCodexDebianProfileBuildsCapturedClientHelloShape(t *testing.T) {
	profile := codexDebianProfileForTest()
	spec := buildClientHelloSpecFromProfile(profile)
	require.Equal(t, profile.CipherSuites, spec.CipherSuites)
	require.Len(t, spec.Extensions, len(profile.Extensions))

	extensionIDs := make([]uint16, 0, len(spec.Extensions))
	for _, extension := range spec.Extensions {
		switch value := extension.(type) {
		case *utls.RenegotiationInfoExtension:
			extensionIDs = append(extensionIDs, 65281)
		case *utls.SNIExtension:
			extensionIDs = append(extensionIDs, 0)
		case *utls.SupportedPointsExtension:
			extensionIDs = append(extensionIDs, 11)
		case *utls.SupportedCurvesExtension:
			extensionIDs = append(extensionIDs, 10)
		case *utls.SessionTicketExtension:
			extensionIDs = append(extensionIDs, 35)
		case *utls.ExtendedMasterSecretExtension:
			extensionIDs = append(extensionIDs, 23)
		case *utls.SignatureAlgorithmsExtension:
			extensionIDs = append(extensionIDs, 13)
		case *utls.SupportedVersionsExtension:
			extensionIDs = append(extensionIDs, 43)
		case *utls.PSKKeyExchangeModesExtension:
			extensionIDs = append(extensionIDs, 45)
		case *utls.KeyShareExtension:
			extensionIDs = append(extensionIDs, 51)
		case *utls.GenericExtension:
			extensionIDs = append(extensionIDs, value.Id)
		default:
			t.Fatalf("unexpected extension type %T", extension)
		}
	}
	require.Equal(t, profile.Extensions, extensionIDs)
}

func TestProfileKeyChangesWithWireFieldsNotDisplayName(t *testing.T) {
	first := codexDebianProfileForTest()
	same := codexDebianProfileForTest()
	same.Name = "renamed"
	require.Equal(t, ProfileKey(first), ProfileKey(same))

	changed := codexDebianProfileForTest()
	changed.KeyShareGroups = []uint16{29}
	require.NotEqual(t, ProfileKey(first), ProfileKey(changed))
}

func TestProfileKeyPreservesSliceBoundaries(t *testing.T) {
	first := &Profile{Curves: []uint16{0xffff}}
	second := &Profile{CipherSuites: []uint16{0xffff}}

	require.NotEqual(t, ProfileKey(first), ProfileKey(second))
}
