package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/stretchr/testify/require"
)

type tlsFingerprintProfileRepositoryStub struct {
	profiles map[int64]*model.TLSFingerprintProfile
	updates  int
	deletes  int
}

func (r *tlsFingerprintProfileRepositoryStub) List(context.Context) ([]*model.TLSFingerprintProfile, error) {
	result := make([]*model.TLSFingerprintProfile, 0, len(r.profiles))
	for _, profile := range r.profiles {
		result = append(result, profile)
	}
	return result, nil
}

func (r *tlsFingerprintProfileRepositoryStub) GetByID(_ context.Context, id int64) (*model.TLSFingerprintProfile, error) {
	return r.profiles[id], nil
}

func (r *tlsFingerprintProfileRepositoryStub) Create(_ context.Context, profile *model.TLSFingerprintProfile) (*model.TLSFingerprintProfile, error) {
	return profile, nil
}

func (r *tlsFingerprintProfileRepositoryStub) Update(_ context.Context, profile *model.TLSFingerprintProfile) (*model.TLSFingerprintProfile, error) {
	r.updates++
	return profile, nil
}

func (r *tlsFingerprintProfileRepositoryStub) Delete(_ context.Context, id int64) error {
	r.deletes++
	delete(r.profiles, id)
	return nil
}

func TestTLSFingerprintProfileServiceProtectsCapturedBuiltin(t *testing.T) {
	repo := &tlsFingerprintProfileRepositoryStub{profiles: map[int64]*model.TLSFingerprintProfile{
		7: {ID: 7, Name: codexDebianTLSFingerprintProfileName},
	}}
	service := NewTLSFingerprintProfileService(repo, nil)

	_, err := service.Update(context.Background(), &model.TLSFingerprintProfile{ID: 7, Name: "renamed"})
	require.ErrorIs(t, err, ErrTLSFingerprintProfileImmutable)
	require.Equal(t, 0, repo.updates)

	err = service.Delete(context.Background(), 7)
	require.ErrorIs(t, err, ErrTLSFingerprintProfileImmutable)
	require.Equal(t, 0, repo.deletes)
}

func TestResolveTLSProfileDoesNotReplaceMissingSelection(t *testing.T) {
	service := &TLSFingerprintProfileService{localCache: map[int64]*model.TLSFingerprintProfile{}}
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": 404,
		},
	}

	require.Nil(t, service.ResolveTLSProfile(account))
}

func TestTLSFingerprintProfileServiceExcludesInvalidProfilesFromRuntimeCache(t *testing.T) {
	repo := &tlsFingerprintProfileRepositoryStub{profiles: map[int64]*model.TLSFingerprintProfile{
		1: {ID: 1, Name: "valid", SupportedVersions: []uint16{0x0304, 0x0303}},
		2: {ID: 2, Name: "weak", SupportedVersions: []uint16{0x0301}},
	}}
	profileService := NewTLSFingerprintProfileService(repo, nil)

	require.NotNil(t, profileService.GetProfileByID(1))
	require.Nil(t, profileService.GetProfileByID(2))
}
