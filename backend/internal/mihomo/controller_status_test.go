package mihomo

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestManagedControllerUsesManagerSecret(t *testing.T) {
	m := New(t.TempDir())
	defer m.Close()
	m.mu.Lock()
	m.saved.Secret = "test-only"
	m.controllerURL = "http://127.0.0.1:12345"
	m.mu.Unlock()
	controller, secret, ok := ManagedController()
	require.True(t, ok)
	require.Equal(t, "http://127.0.0.1:12345", controller)
	require.Equal(t, "test-only", secret)
	m.Close()
	_, _, ok = ManagedController()
	require.False(t, ok)
}
