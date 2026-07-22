package iavl

import (
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
