package service

import (
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatePluginCompatibility(t *testing.T) {
	manifest := testPluginManifest(nil)
	host := PluginHostInfo{Version: "0.1.179", BuildType: "release"}

	result := EvaluatePluginCompatibility(manifest, host)
	require.True(t, result.Compatible)
	assert.True(t, result.Tested)
	assert.Equal(t, "compatible", result.Status)

	manifest.Requires.TestedSub2APIVersions = []string{"0.1.178"}
	result = EvaluatePluginCompatibility(manifest, host)
	require.True(t, result.Compatible)
	assert.False(t, result.Tested)
	assert.Equal(t, "untested", result.Status)

	manifest.Requires.Sub2API = ">=0.2.0 <0.3.0"
	result = EvaluatePluginCompatibility(manifest, host)
	assert.False(t, result.Compatible)
	assert.Equal(t, "incompatible", result.Status)
}

func TestEvaluatePluginCompatibilityRejectsProtocolMismatch(t *testing.T) {
	manifest := testPluginManifest(nil)
	manifest.Requires.PluginProtocol = pluginv1.ProtocolVersion + 1

	result := EvaluatePluginCompatibility(manifest, PluginHostInfo{Version: "0.1.179"})

	assert.False(t, result.Compatible)
	assert.Equal(t, "incompatible", result.Status)
}

func TestMatchesSemverRange(t *testing.T) {
	assert.True(t, matchesSemverRange("0.1.179", ">=0.1.170, <0.2.0"))
	assert.True(t, matchesSemverRange("v1.2.3", "=1.2.3"))
	assert.False(t, matchesSemverRange("0.1.169", ">=0.1.170 <0.2.0"))
	assert.False(t, matchesSemverRange("dev", ">=0.1.0"))
	assert.False(t, matchesSemverRange("0.1.179", "^0.1.0"))
}

// Fork releases are X.Y.Z-custom.N. A plugin packaged for that host declares
// ">=X.Y.Z <X.(Y+1).0"; strict semver would sort the host below X.Y.Z and make
// the official plugin permanently uninstallable on the release it shipped with.
func TestMatchesSemverRangeTreatsForkBuildsAsTheirReleaseLine(t *testing.T) {
	const constraint = ">=0.2.9 <0.3.0"

	assert.True(t, matchesSemverRange("0.2.9-custom.1", constraint))
	assert.True(t, matchesSemverRange("0.2.9-custom.12+build.7", constraint))
	assert.True(t, matchesSemverRange("0.2.9", constraint))
	assert.False(t, matchesSemverRange("0.2.8-custom.5", constraint))
	assert.False(t, matchesSemverRange("0.3.0-custom.1", constraint))
	assert.False(t, matchesSemverRange("0.3.0", constraint))

	// A bound that itself names a prerelease is still compared exactly.
	assert.True(t, matchesSemverRange("0.2.9-custom.1", "=0.2.9-custom.1"))
	assert.False(t, matchesSemverRange("0.2.9-custom.2", "=0.2.9-custom.1"))

	manifest := testPluginManifest(nil)
	manifest.Requires.Sub2API = constraint
	manifest.Requires.TestedSub2APIVersions = []string{"0.2.9-custom.1"}
	result := EvaluatePluginCompatibility(manifest, PluginHostInfo{Version: "0.2.9-custom.1", BuildType: "release"})
	require.True(t, result.Compatible)
	assert.Equal(t, "compatible", result.Status)
}
