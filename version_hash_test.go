package iavl

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"

	dbm "github.com/cosmos/iavl/db"
)

func TestImmutableTreeVersionHashResolvesSavedRoot(t *testing.T) {
	db := dbm.NewMemDB()
	tree := NewMutableTree(db, 0, false, NewNopLogger())
	_, err := tree.Set([]byte("key"), []byte("value"))
	require.NoError(t, err)
	want, version, err := tree.SaveVersion()
	require.NoError(t, err)

	reader := NewImmutableTree(db, 0, false, NewNopLogger())
	got, err := reader.VersionHash(version)
	require.NoError(t, err)
	require.Equal(t, want, got)
	got[0] ^= 0xff
	again, err := reader.VersionHash(version)
	require.NoError(t, err)
	require.Equal(t, want, again)

	_, err = reader.VersionHash(version + 1)
	require.ErrorIs(t, err, ErrVersionDoesNotExist)
}

func TestImmutableTreeVersionHashBypassesStaleNodeCache(t *testing.T) {
	db := dbm.NewMemDB()
	hash, body := makeLegacyInspectionNode(t, 0, 1, 1, []byte("key"), []byte("value"), nil)
	require.NoError(t, db.Set(legacyRootKeyFormat.Key(int64(1)), hash))
	require.NoError(t, db.Set(legacyNodeKeyFormat.Key(hash), body))

	reader := NewImmutableTree(db, 100, false, NewNopLogger())
	_, err := reader.VersionHash(1)
	require.NoError(t, err)
	require.NoError(t, db.Delete(legacyNodeKeyFormat.Key(hash)))

	_, err = reader.VersionHash(1)
	require.ErrorContains(t, err, "Value missing")
}

func TestImmutableTreeVersionHashResolvesEmptyRoot(t *testing.T) {
	db := dbm.NewMemDB()
	tree := NewMutableTree(db, 0, false, NewNopLogger())
	want, version, err := tree.SaveVersion()
	require.NoError(t, err)
	require.Equal(t, sha256.New().Sum(nil), want)

	reader := NewImmutableTree(db, 0, false, NewNopLogger())
	got, err := reader.VersionHash(version)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
